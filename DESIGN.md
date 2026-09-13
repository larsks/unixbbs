# unixbbs — Design Plan

Packet-radio BBS built as a set of containers: two persistent core
services (user, mail — chat deferred, see §9) and one ephemeral,
per-connection user container that a user is placed into for the
duration of their session.

This document covers the container topology, the network/IPC model, the
data layout on disk, and the request/response flow for login and mail.
It assumes **Docker** as the container runtime and treats the AX.25
ingress (whatever spawns a container per incoming connection and sets
`$SRC_CALLSIGN`) as an existing, external component — out of scope here.

## 1. Goals / non-goals

**Goals**
- Unqualified-username local mail (`mail -s subject n0call`), both send
  and read, using stock Unix mail tools.
- New callsigns auto-register on first connection.
- Plain, escape-sequence-free, colorless terminal output throughout
  (dumb-terminal / AX.25 constraint).
- **No network access at all from user containers** (`--network none`) —
  all communication with core services happens over Unix sockets on a
  shared filesystem.

**Non-goals (for now)**
- Interactive chat (deferred — see §9; it doesn't fit the socket-only
  model as cleanly as mail/user-lookup do).
- Internet-facing SMTP (no outbound mail, no MX, no relaying to the
  world).
- The AX.25/TNC ingress and the process that runs `docker run` per
  incoming connection (assumed to already exist).
- Full-screen/curses UI of any kind.

## 2. Network & isolation model

Ephemeral user containers run with `--network none` — no network
namespace beyond loopback, full stop. This is stronger than the
isolated-bridge alternative considered earlier, and it's viable *because*
both remaining core services can be reached over Unix-domain sockets on
a shared filesystem instead of TCP. Both mechanisms below have been
verified empirically against real Postfix installs — initially Postfix
3.7 (Debian bookworm), then re-confirmed on Postfix 3.11 (Alpine, the
base image this design now targets — see §2.3 and §5) — not just read
off man pages. See rationale after each.

- **`user-service`**: a small Go HTTP server, listening on **two** Unix
  sockets split by trust level rather than one (see §4.1):
  a root-only `user-write.sock` for lookup/create and presence
  registration, and a world-connectable `user-read.sock` for the
  read-only `users` command. Called from the ephemeral container with
  `curl --unix-socket`, or directly via Go's `net.Dial("unix", ...)`
  from anything we write ourselves. Untested but low-risk — this is our
  own code, not a third-party daemon with undocumented constraints.
- **`mail-service`** (inbound, i.e. `smtpd` accepting a connection):
  Postfix's `master.cf` **can** bind an `smtpd` listener to a Unix-domain
  socket instead of `inet`, confirmed working end-to-end (`EHLO` /
  `MAIL FROM` / `RCPT TO` / `DATA` all function identically to the inet
  case). It comes with two real, non-obvious constraints (see below) —
  do not treat "Unix-socket smtpd" as a drop-in one-liner.
- **`mail-service`** (outbound, i.e. ephemeral container *relaying to*
  mail-service): plain SMTP relay to a Unix-domain-socket next-hop is
  **not supported by Postfix, confirmed, not just undocumented**. A
  small `socat` shim is therefore a required part of the design, not a
  fallback.

### 2.1 Inbound: `smtpd` on a Unix-domain socket — confirmed, with two gotchas

`master(5)` undersells what actually happens: it says a `unix` service's
name is "a pathname relative to the Postfix queue directory," which
reads as if any relative (or even absolute) path works. In practice,
tracing `master`'s own `bind()`/`unlink()` calls (via `strace -f`) showed
it unconditionally prepends `public/` (or `private/`, depending on the
`private` column) to whatever name you give — an absolute path like
`/run/bbs/mail.sock` becomes the literal, broken path
`$queue_directory/public//run/bbs/mail.sock` and fails with `ENOENT`.
The only way to control where the socket lands is to give a name that is
itself a subpath under `public/` (or `private/`), e.g.:

```
# master.cf
bbssock/mailsock unix  n  -  n  -  -  smtpd
```

which places the real listener at
`$queue_directory/public/bbssock/mailsock`. This requires two directories
to exist *before* `master` starts (it does not create them for you):

```sh
install -d -o postfix -g postdrop -m 2710 /var/spool/postfix/public/bbssock
install -d -o root    -g root     -m 0755 /var/spool/postfix/pid/unix.bbssock
```

The second directory is the one that's easy to miss and cost the most
debugging time: `master` also derives a per-instance PID/lock file path
from the service name (`pid/unix.<name>`), and if that directory doesn't
already exist, `smtpd` fails at spawn time with
`fatal: open lock file pid/unix.bbssock/mailsock: cannot create file
exclusively: No such file or directory`, followed by
`master: bad command startup -- throttling`. This failure is
**intermittent-looking** rather than a clean hard failure: `master`
itself starts fine (the listener socket is bound), so `postfix start`
reports success, and the first connection or two can even go through
before throttling kicks in — this looks exactly like a flaky heisenbug
until you read `maillog_file` (or syslog) and see the actual `fatal:`
line. Once both directories are pre-created, 100% of repeated test
connections (5/5 in testing, including full `MAIL FROM`/`RCPT
TO`/`DATA` transactions) succeeded with no throttling.

Practical implication for the design: `mail-service`'s container
entrypoint/init must create both directories under `$queue_directory`
before invoking `postfix start`/`master`, and the shared volume exposed
to ephemeral containers should be scoped to
`/var/spool/postfix/public/bbssock/` specifically (bind-mounted or a
named volume at that exact path) — **not** the whole `public/` directory,
which also holds Postfix's own internal sockets (`pickup`, `qmgr`,
`cleanup`, `showq`, `flush`, `postlog`) that ephemeral containers have no
business seeing.

```
persistent volumes/dirs used purely for IPC (not data):
  $DATADIR/run/user-write.sock           <- user-service, root-only (0700)
  $DATADIR/run/user-read.sock            <- user-service, world-connectable (0666)
  $DATADIR/run/mail-public/mailsock      <- bind-mounted 1:1 onto
                                             mail-service's own
                                             /var/spool/postfix/public/bbssock/
                                             so both sides see the same
                                             directory (needed because
                                             smtpd unlinks + recreates
                                             the socket file on every
                                             restart — bind-mounting the
                                             *file* alone would silently
                                             stop working after the
                                             first restart)
```

### 2.2 Outbound: no `unix:` SMTP relay — confirmed via docs, socat shim is load-bearing

Read directly from `transport(5)`, `smtp(8)`/`lmtp(8)` (one man page,
since they're the same binary), and `smtpd(8)` (Postfix 3.11 docs):

- The `smtp:` transport's next-hop syntax is strictly
  `domainname[:service]` / `[host][:port]` / `[address][:port]` — no
  `unix:` form exists for it.
- The `unix:pathname` next-hop syntax **does** exist, but only for the
  `lmtp:` transport (LMTP *client*).
- Postfix ships no LMTP *server* — `smtpd(8)` is described simply as
  "The SMTP server"; there's nothing on the other end of an `lmtp:`
  next-hop pointed at `mail-service`'s `smtpd`.

So there is no configuration of any Postfix-family MTA (and no
configuration of `msmtp`, see §2.3) that relays outbound mail straight to
a Unix-domain-socket next-hop and lands on our `smtpd`. The fix, verified
working: run `socat` inside the ephemeral container as a local
TCP-loopback-to-Unix-socket adapter, and point the client MTA's relay
host at that loopback port instead:

```sh
socat TCP-LISTEN:2525,bind=127.0.0.1,fork,reuseaddr \
      UNIX-CONNECT:/bbs-sock/mail/mailsock &
```

Confirmed end-to-end: a message submitted this way (SMTP over
127.0.0.1:2525 → socat → the Unix socket → `mail-service`'s `smtpd`)
delivers identically to a message submitted directly on the socket.
`lo` is present and fully functional even under `--network none` (it's
local to the container's own network namespace, not a route out), so
this doesn't reintroduce real networking — it's a protocol adapter, and
it is now a **required** component of every ephemeral container's image,
not an optional fallback.

**Service containers** (`user-service`, `mail-service`) keep normal
network access — the isolation requirement is specifically about the
ephemeral user containers, not the trusted core services.

### 2.3 Client MTA: `msmtp`, not Postfix — meaningfully simpler, one gotcha to work around

The ephemeral container never receives mail and never needs a queue — it
only relays a single outbound message per `mail` invocation to a socket
that's always reachable (a sibling container on the same host). That's
exactly the case a minimal relay-only client MTA is built for, so we use
`msmtp` there instead of a second, cut-down Postfix instance.
(`ssmtp` was the obvious alternative and does the same job, but it's
deprecated and dropped from Debian; `msmtp` is the maintained
equivalent, so there's no reason to pick `ssmtp`.)

**A required extra step, confirmed by testing, not just a packaging
nicety**: installing `msmtp` on Alpine does **not** repoint
`/usr/sbin/sendmail` at it — Alpine has no `update-alternatives`
mechanism, so that path is left as a symlink to busybox's own
`sendmail` stub applet. `mailutils`' `mail` always invokes plain
`sendmail`, never `msmtp` directly, so with the stock symlink left in
place every send silently goes nowhere (`mail` reports "cannot send
message" with no further detail; nothing reaches `mail-service` at
all). The fix is one line in the image build:
`ln -sf /usr/bin/msmtp /usr/sbin/sendmail`, overwriting the busybox
symlink — msmtp ships its own sendmail-compatible CLI specifically for
this.

**Confirmed: this is a real simplification, not a wash.** Dropping
Postfix from the ephemeral image removes an entire daemon tree
(`master`/`qmgr`/`pickup`/`cleanup`/`smtpd`/`trivial-rewrite`) that has
no job to do there (it never receives connections), removes the need for
`postfix`/`postdrop` groups and setgid `postdrop`/`postqueue` binaries,
and removes Postfix's hard requirement for a writable spool directory
tree — `msmtp` is a single non-daemon binary invoked once per message,
with no local queue at all, which fits a container that could otherwise
run with a read-only rootfs. It replaces the "null-client Postfix"
referenced elsewhere in this doc as the outbound path in every ephemeral
container.

**The gotcha, and the fix — revised after further testing.** `msmtp` does
no recipient-address completion of its own — it relays the recipient
exactly as given, and GNU Mailutils' `mail` puts that recipient only in
the message's `To:`/`Cc:`/`Bcc:` headers (it invokes `sendmail -oi -f
<envelope-from> -t`, with nothing on argv). So the literal spec command

```sh
echo "Hello world" | mail -s "test message" n0call
```

produces a message with a bare `To: n0call` header and no qualified
recipient anywhere in what `msmtp` has to work with.

An earlier pass at this design concluded from that observation that the
fix had to live client-side (a custom `sendmail`-wrapper rewriting
message headers before handing off to `msmtp`), on the theory that
Postfix's own address completion (`append_at_myorigin_classes`) only
applies to local command-line submission, never to mail arriving over
network SMTP. **That theory was wrong**, and the client-side wrapper was
unnecessary complexity — the earlier test that seemed to confirm it had
simply never set `myorigin` on the receiving side, so there was nothing
there to do the rewriting.

Retested properly: setting `myorigin = bbs.local` on **`mail-service`**
is sufficient on its own. Address completion for a domain-less envelope
recipient happens unconditionally in `trivial-rewrite`/`cleanup` for
every incoming message regardless of transport — confirmed by sending a
raw SMTP transaction with `RCPT TO:<user1>` directly over the production
Unix-domain socket (not TCP loopback) into a `smtpd` configured with
`myorigin = bbs.local` plus real `virtual_mailbox_domains =
bbs.local`/`virtual_mailbox_maps`, and seeing it accepted, queued, and
delivered:

```
postfix/virtual: 7A8263995309: to=<user1@bbs.local>, orig_to=<user1>,
  relay=virtual, ... status=sent (delivered to maildir)
```

— and then confirmed again with the actual client pipeline (`mail` →
plain unmodified `msmtp` → the `socat` shim → that same socket), which
delivers the literal spec command successfully with no wrapper, no
header rewriting, and no other client-side code at all. (The header
itself stays as the bare `To: n0call` the user typed — only the envelope
recipient gets qualified — which is harmless and arguably nicer to look
at than a synthetic domain the user never typed.)

So: **no custom wrapper is needed.** The only change required versus a
stock `msmtp` install is one extra line in `mail-service`'s `main.cf`:

```
myorigin = bbs.local
```

Net: switching the ephemeral container's MTA to `msmtp` is a
straightforward simplification with no gotcha to work around — a stock
`mailutils` + stock `msmtp` + the `socat` shim from §2.2 is the whole
outbound path. The one real trade-off versus Postfix worth naming
explicitly: `msmtp` has no local queue, so if `mail-service` is briefly
unreachable, `mail` reports the send as failed immediately rather than
silently queueing and retrying. For a loopback socket to an always-up
sibling container this is a minor concern (and arguably more honest than
a retry the user can't observe), but it's a real behavior change from
Postfix worth calling out to anyone integrating against this.

### 2.4 Mail-reading client: `mailutils`, not `mailx` — Maildir support is the deciding factor

Alpine packages both `mailutils` (GNU) and `mailx` (an Alpine package
built from a 2001-vintage BSD/Heirloom-lineage `Mail`, snapshotted in
2022) as candidates for the `mail` command required by the spec. They
**conflict at the package level** — both claim the `mail`/`sendmail`
command names, confirmed via `apk add`:

```
ERROR: unable to select packages:
  mailutils-3.21-r1: conflicts: mailx-8.1.2...[cmd:mail=3.21-r1]
  mailx-8.1.2...: conflicts: mailutils-3.21-r1[cmd:mail=8.1.2...]
```

so exactly one has to be chosen for the image.

`mailx` is far lighter (depends only on `libbsd`/`liblockfile`, ~7.3M
total on a fresh image vs. `mailutils`'s ~18.2M — `mailutils` links a
separate shared library per supported backend: `libmu_mbox`,
`libmu_maildir`, `libmu_mh`, `libmu_pop`, `libmu_imap`, `libmu_sieve`,
`libmu_dotmail`, `libmu_mailer`, most of which — POP/IMAP clients, Sieve
filtering, MH folders — is irrelevant to a local-delivery-only BBS).

But mailbox format support is the deciding factor, confirmed by testing
both against the same mbox file and the same Maildir directory: `mailx`
reads mbox fine but fails outright against a Maildir (`mail:
/tmp/maildir: Is a directory` — no Maildir support at all), while
`mailutils` reads either transparently with zero configuration —
`MAIL=/path/to/mbox-file mail` and `MAIL=/path/to/maildir-dir mail` both
just work, format auto-detected from the path. Since `mail-service`
delivers as Maildir (see §4 — the natural, lock-free result of a
trailing-slash entry in `virtual_mailbox_maps`, Postfix's own convention,
and the format actually verified working during the §2.1/§2.3 testing),
`mailx` is a non-starter and `mailutils` is required.

One thing checked and ruled out as a concern either way: both `mailx`
and `mailutils` invoke `sendmail -t` (recipient only in the piped
message's headers, nothing on argv) — confirmed identically for both by
logging real argv from a fake `sendmail`. So the `myorigin` fix in §2.3
is unaffected by this choice; it was purely a mailbox-format question.

## 3. Data layout

```
$DATADIR/
  bbs.db                  # SQLite; users table. WAL mode.
  bbs.db-wal / bbs.db-shm # WAL sidecar files (same directory)
  run/
    user.sock                  # user-service's HTTP listener
    mail-public/mailsock       # bind-mounted onto mail-service's
                                # /var/spool/postfix/public/bbssock/mailsock
                                # (see §2.1 — must be the directory, not
                                # just the socket file, bind-mounted)
  mail/
    <uid>/                  # Maildir (cur/new/tmp), mode 0700, owned by
                             # <uid>:<uid> — see §4's mail-service section
                             # for why Maildir over mbox
  home/
    <uid>/                  # persistent per-user home dir, mode 0700
  bulletins/                # a Maildir (cur/new/tmp), owned root:root,
                             # no group/other write bit -- see the
                             # "Bulletins" subsection under §4
```

`.envrc` already sets `DATADIR=data`; `UIDFILE=uid` is superseded by
SQLite's own `AUTOINCREMENT` sequence (see below) and can be dropped —
worth confirming that file isn't relied on by something else already in
progress before removing it.

### Schema

```sql
CREATE TABLE users (
  uid        INTEGER PRIMARY KEY AUTOINCREMENT,
  callsign   TEXT UNIQUE NOT NULL COLLATE NOCASE,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
-- prime the sequence so real users start at 10000, clear of any
-- container-image system UID range:
INSERT INTO sqlite_sequence (name, seq) VALUES ('users', 9999);
```

`PRAGMA journal_mode=WAL;` is set on this database — it's what lets
`mail-service`'s Postfix do read-only point lookups against it
concurrently with `user-service` writing new rows, with no locking code
of our own.

Get-or-create, in `user-service`, is a single atomic statement pair
inside one transaction — SQLite's unique constraint does the
concurrency-safety work:

```sql
INSERT INTO users (callsign) VALUES (?) ON CONFLICT (callsign) DO NOTHING;
SELECT uid, callsign, created_at FROM users WHERE callsign = ?1 COLLATE NOCASE;
```

Two stations keying up as new callsigns at the same instant just means
one `INSERT` wins and the other's `SELECT` reads the winner's row — no
UID is ever double-allocated or skipped in a way that matters.

## 4. Service boundaries

- **`user-service`** is the only thing with `bbs.db` mounted read-write.
  It owns account creation and is the only writer.
- **`mail-service`** mounts `bbs.db` **read-only** and queries it
  natively via Postfix's `sqlite:` map type for
  `virtual_mailbox_maps` (resolving `callsign → uid`, and hence mailbox
  path) — no lookup sidecar needed, since this is a stock Postfix lookup
  table type pointed at our schema.
- **Ephemeral (user) containers** never see `bbs.db` at all. They reach
  `user-service` only through its socket API — a privileged call at
  login (get-or-create by callsign, and registering presence), and an
  unprivileged one available to the logged-in shell for the `users`
  command (see §4.1).

### 4.1 `user-service` API: two sockets, split by trust level

The ENTRYPOINT needs to call `user-service` while it's still root, for
operations that either mutate `bbs.db` or claim an identity (presence
registration). The interactive shell, once it drops to the unprivileged
provisioned account, needs to call `user-service` too — but only for
read-only queries (`users`, `users --online`), and it must not be able
to reach the mutating endpoints, since it's an otherwise-unrestricted
`dash` shell (§5) and the only thing standing between "run the `users`
command" and "curl the create-user endpoint with an arbitrary callsign"
would otherwise be nothing.

Rather than enforce that boundary in application code, it's enforced the
same way `mail/`/`home/` isolation is (§5/§6): standard Unix permission
bits on the socket file itself. `connect(2)` on an `AF_UNIX` socket is
permission-checked against the socket inode exactly like `open(2)` on a
regular file, so two socket files with different modes in the same
shared, bind-mounted directory get different reachability for free, with
no code on either side able to get it wrong:

- **`user-write.sock`** — mode `0700`, owned `root:root`. Only a process
  still running as root (i.e. the ENTRYPOINT, before it provisions and
  switches to the session's unprivileged UID) can `connect()` to it at
  all; the provisioned account gets `EACCES` if it tries, even though
  the socket file is visible via `ls` in the shared mount.
  ```
  POST /users/lookup
    { "callsign": "N0CALL" }
    200 OK
    { "callsign": "N0CALL", "uid": 10042, "created": false }

  POST /users/presence   (see §4.2 — held open, not request/response)
  ```
- **`user-read.sock`** — mode `0666`, connectable by anyone, including
  the provisioned account. Handlers here don't accept any input that
  could turn them into a mutation, by construction — there's no code
  path from a crafted request on this socket to a database write.
  ```
  GET /users/list
    200 OK
    [{"callsign":"N0CALL","created_at":"..."}, ...]

  GET /users/online
    200 OK
    [{"callsign":"N0CALL"}, ...]
  ```

`mail-service` calls neither socket; it queries `bbs.db` directly (see
above). Both listeners live in the same Go binary (two `net.Listen("unix",
...)` calls, each followed by an explicit `os.Chmod` — `net.Listen`'s
default mode depends on umask, not something to leave implicit for the
socket that matters). Implemented per repo conventions: embed the
schema/migration SQL via `embed` rather than a string literal, `any` not
`interface{}`, `gofmt` on every change.

### 4.2 Presence tracking and the `users` command

**Goal**: `users` (no args) lists every known callsign; `users -o` /
`users --online` lists only currently-connected sessions — without
polling, and without a heartbeat that could go stale if a container dies
uncleanly.

**Mechanism**: right after the login lookup, while still root, the
ENTRYPOINT opens a second connection to `user-write.sock` and sends one
line registering the session (`{"callsign":"N0CALL","uid":10042}`), then
*holds that connection open* for the rest of the container's life — it's
never a request/response exchange, just a long-lived pipe. `user-service`
adds an entry to an in-memory (deliberately not SQLite — see below) map
keyed by the connection itself, and blocks on a `Read` from it. The
moment that `Read` returns an error — clean shell exit, `docker kill`,
OOM-kill, host crash, anything — the kernel has already torn down the
socket, and `user-service` deletes the entry immediately. There is
nothing to time out or reconcile: the OS-level "this pipe is broken"
signal *is* the liveness signal, and it fires the same way whether the
container exited cleanly or was killed out from under it, which a
heartbeat-file or last-seen-timestamp approach doesn't get for free.

Why in-memory rather than a `sessions` table in `bbs.db`: presence is
only ever meaningful while `user-service` itself is running — a restart
naturally clears it, and correctly so, since every still-live container
would need to re-establish its connection anyway (nothing survives a
`user-service` restart on the container side either). Persisting it would
add write-lock churn to `bbs.db` on every single login/logout, against a
database whose only other writer is the much rarer new-callsign case,
for a table that isn't supposed to outlive the process anyway. Keying by
connection (not by callsign) means two simultaneous sessions from the
same callsign are handled for free — `/users/online` just de-duplicates
by callsign when it lists the map.

**Critical implementation detail — don't leak the fd into the shell**:
the process that holds the presence connection open must be a
*background sibling* of the process that later `exec`s into the user's
`dash` shell, not the same process. If the ENTRYPOINT script itself
opened the connection and then `exec`'d `dash` without closing it, the
interactive shell would inherit that already-open file descriptor —
and an *open* fd is not subject to a fresh permission check, so this
would hand an unprivileged shell a live, privileged pipe into
`user-write.sock` regardless of the socket's `0700` mode. The fix is
ordinary process hygiene, not new mechanism: fork the connection-holder
into its own backgrounded subshell before the `useradd`/privilege-drop
step, e.g.

```sh
( exec 3<>/bbs-sock/user-write.sock
  printf '{"callsign":"%s","uid":%s}\n' "$CALLSIGN" "$UID" >&3
  cat <&3 >/dev/null ) &
```

so the fd lives only in that detached child's own table. The foreground
script — the one that eventually `exec`s into `dash` — never opens fd 3
itself, so it has nothing to leak. When the container is torn down, both
processes die together (same PID namespace), which is exactly the
"connection drops → offline" signal presence tracking depends on.

**`users` command**: a small script in `PATH` (e.g.
`/usr/local/bin/users`) that does nothing but call the read socket and
print plain lines — no color, no escapes, consistent with the
dumb-terminal constraint (§1):

```sh
#!/bin/dash
case "$1" in
  -o|--online) curl -s --unix-socket /bbs-sock/user-read.sock \
                 http://localhost/users/online | jq -r '.[].callsign' ;;
  *)           curl -s --unix-socket /bbs-sock/user-read.sock \
                 http://localhost/users/list   | jq -r '.[].callsign' ;;
esac
```
(exact formatting/columns TBD — `jq` needs to actually be in the image,
or this becomes a few lines of `sed`/`cut` if we'd rather not add the
dependency; not a design-level decision.)

### `mail-service`

**Send path** (`echo hi | mail -s subject n0call`, run inside a user
container):

1. The user container's outbound MTA is stock `msmtp` (see §2.3 — no
   wrapper needed), configured with `host = 127.0.0.1`, `port = 2525` — a
   small `socat` process (started by the container's entrypoint, see
   §2.2) bridges that loopback port to the shared Unix socket. This
   indirection is required, not optional — neither Postfix's `smtp:`
   transport nor `msmtp` has a `unix:`-socket next-hop syntax (confirmed
   against `transport(5)`/`smtp(8)`; only Postfix's `lmtp:` transport
   supports `unix:pathname`, and Postfix ships no LMTP server for it to
   target). Bare recipients (`mail -s subject n0call`) arrive as a bare
   `To: n0call` header and an unqualified `RCPT TO:<n0call>` — qualified
   entirely on the receiving side, by `mail-service`'s `myorigin =
   bbs.local` (see §2.3).
2. `mail-service` runs full Postfix, `smtpd` bound to a Unix-domain
   socket via `master.cf` (`bbssock/mailsock unix n - n - - smtpd`,
   landing at `/var/spool/postfix/public/bbssock/mailsock` — see §2.1 for
   the two directories that must be pre-created for this to work
   reliably), with `myorigin = bbs.local` (required — see §2.3 for why
   this single line is what makes unqualified recipients work at all),
   `bbs.local` configured as a `virtual_mailbox_domain`,
   `virtual_mailbox_maps = sqlite:/etc/postfix/sqlite-virtual-mailbox.cf`
   plus matching `virtual_uid_maps`/`virtual_gid_maps` entries pointed at
   their own small `sqlite:` config files (all three naming `bbs.db`,
   read-only, keyed by the same `callsign → uid` query; see the
   confirmed-by-testing note below step 3 for why two more map files
   beyond the mailbox one are needed). The mailbox map's query returns
   `<uid>/` — the **trailing slash is
   required**: it's Postfix's own convention for "deliver as Maildir"
   in the `virtual` transport, and is what makes step 3 below a Maildir
   delivery rather than mbox — on Alpine this requires the separate
   `postfix-sqlite` package, since map-type support is split into
   subpackages there; confirmed Alpine's base `postfix` package has no
   `hash:` map support at all — Berkeley DB isn't linked in — so any
   local map file needs `lmdb:` instead, and `sqlite:` needs
   `postfix-sqlite`). Unknown recipients bounce at SMTP time with an
   ordinary "user unknown" — no silent drops.
3. On acceptance, Postfix's `virtual` delivery agent writes into
   `mail/<uid>/` (a Maildir — `cur`/`new`/`tmp`, confirmed via testing:
   `status=sent (delivered to maildir)`) under `$DATADIR`, which only
   `mail-service` has mounted read-write across the **whole** tree (it's
   the only container that ever needs to reach any user's mailbox
   regardless of who's currently logged in). Maildir over mbox is
   deliberate, not incidental — see §2.4 for why, and for why that also
   settles `mailutils` vs `mailx` for the read path below.

   **Confirmed by testing, not just assumed from the docs**: the file
   delivered actually lands owned by the *recipient's own* uid:gid, not
   some fixed Postfix-controlled owner. This depends on
   `virtual_uid_maps`/`virtual_gid_maps` genuinely supporting
   **per-recipient dynamic lookup**, not just a single static uid/gid —
   an untested assumption going into this build (earlier design-phase
   testing only ever exercised one uid). Retested properly with two
   distinct users: pointing both maps at `sqlite:` queries keyed by the
   same `%s` recipient callsign (`SELECT uid FROM users WHERE callsign =
   '%s' COLLATE NOCASE`) correctly produced two different uid/gid pairs
   for the two users' delivered files. This is what lets `mail-service`
   deliver correctly-owned mail for *any* user without per-user
   configuration, matching the `mail/<uid>` ownership `user-service`
   already sets up in its provisioning step.

   Also confirmed, and required in the real container (not just a test
   convenience): a minimal Alpine image runs no syslog daemon, so
   Postfix's default syslog-based logging produces no visible output at
   all by default — there's simply nothing listening on `/dev/log`. The
   fix used here is `mail-service`'s entrypoint running busybox's own
   `syslogd -C64` (confirmed working end-to-end with Postfix's logging,
   including the §2.1 lock-file `fatal:` line) rather than Postfix's
   `maillog_file` parameter: `-C` logs to a fixed-size in-memory ring
   buffer, read back with `logread`, so there's no on-disk log file that
   needs rotation or a size cap of its own — the ring buffer's fixed size
   already bounds it.

**Read path** (`mail`, run inside a user container): no socket call
needed. `mail/` and `home/` are bind-mounted **in full** (not per-UID)
into every ephemeral container at fixed paths, e.g. `/bbs-data/mail` and
`/bbs-data/home` — see §5 for why this is safe and how it resolves the
mount-timing problem an earlier draft of this design had. `$MAIL` is set
to `/bbs-data/mail/<uid>/` for the provisioned account — GNU Mailutils'
`mail` auto-detects the Maildir format from that path with zero further
configuration (confirmed by testing; `mailx` cannot read a Maildir at
all, which is why `mailutils` is the required package — see §2.4).

### Bulletins

The bulletin board is **also a Maildir**, not a plain directory of
text files — this reuses `mailutils`/`mail`, already a required
dependency, as the entire bulletin-reading UI for free: header
summaries, per-message reading, all of it, with zero new code beyond a
one-line wrapper script. `/usr/local/bin/bulletins` (installed exactly
like `/usr/local/bin/users`, see §4.1/§4.2) is just
`exec mail -f /bbs-data/bulletins`. Adding a bulletin is then just
dropping an RFC822-formatted file (`From:`/`Subject:`/`Date:`/blank
line/body) into that Maildir's `new/`, owned root — no service, no
database, no code.

**A real, confirmed-by-testing gotcha**: a Docker-level `:ro` bind
mount does **not** work here. GNU Mailutils' Maildir backend always
tries to open the mailbox for read-write first, regardless of intent —
against a true `:ro` mount that open fails outright
(`mu_mailbox_open failed: Read-only file system`, exit 1, no headers,
no message text, nothing usable at all).

The fix is **Unix permissions, not the mount flag**: bind-mount the
bulletins Maildir read-**write** at the Docker level, but own every
file and directory in it `root:root` with no write bit for
group/other. Running as the unprivileged provisioned account, `mail`'s
write-open then fails with `EACCES`, and — confirmed by testing —
Mailutils catches that and gracefully self-downgrades to a read-only
open on its own, printing one line (`mail: mailbox opened
read-only`) and remaining fully functional: header summaries and
message bodies both read correctly. A useful side effect of this over
a real writable Maildir: since it can't write, `mail` never renames a
message from `new/` to `cur/` to mark it read, so a bulletin never
stops showing up as new — the same, unmutated state is what every
session sees, which is arguably the more correct behavior for a
bulletin board anyway (confirmed: re-running `bulletins` twice left
`new/` untouched on the host both times).

## 5. Ephemeral user container

### Why whole-tree mounts instead of per-UID mounts

An earlier version of this design tried to bind-mount only *this
session's* `mail/<uid>` and `home/<uid>` directories into the container —
but that requires knowing the UID at `docker run` time, which means
either the external AX.25 spawner has to do the `user-service` lookup
itself before invoking Docker, or some other machinery has to attach
mounts to an already-running container (which Docker doesn't support).

Mounting the whole `mail/` and `home/` trees sidesteps this entirely: the
mounts are static and identical for every session, decided once when the
image/compose config is written, not per-connection. Isolation between
users is then just standard Unix permission bits — each directory is
`0700`, owned by that user's `<uid>:<uid>` — exactly how `/home` and
`/var/mail` have always worked on a shared multi-user Unix box, and just
as effective: a process running as UID 10042 cannot open UID 10043's
files, whether or not the path is technically mounted into its
namespace. (The one leak this allows is that `ls /bbs-data/mail/` reveals
which numeric UIDs exist, with no further information; tightening that
with a non-listable parent directory is a cheap follow-up if it matters,
not a blocker.)

### Image contents

- Minimal base image — no dev tools, no package manager, no compiler.
  Alpine (`alpine:3`) is the base; both `msmtp` and `postfix` are
  available there, confirmed (Alpine ships Postfix 3.11.x as of this
  writing; the mail-service side needs the separate `postfix-sqlite`
  package for `virtual_mailbox_maps = sqlite:...`, per §4).
- `mailutils` — not `mailx`, which can't read the Maildir format
  `mail-service` delivers into; see §2.4 — for `mail`, + stock `msmtp`
  (for outbound relay — see §2.3 for why `msmtp` instead of Postfix) as
  `/usr/sbin/sendmail`. No wrapper, no extra config beyond `/etc/msmtprc`
  pointing at the `socat` loopback shim — the recipient-qualification
  gap identified in an earlier pass turned out to need only a config
  change on `mail-service` (§2.3), not any code in this image.
- `socat`, required (not optional) for two things: the outbound relay
  shim in §2.2, and — confirmed necessary by testing, not just a style
  choice — actually opening the presence-registration connection in
  step 3 below, since plain shell fd redirection cannot `connect(2)` to
  an `AF_UNIX` socket.
- `dash` as the provisioned account's login shell — plain, POSIX,
  line-oriented, well-behaved with no assumptions about terminal
  capabilities. No custom restricted-shell program; the container itself
  (network=none, read-only rootfs, dropped capabilities, single
  low-privilege UID, curated `PATH`) is the confinement mechanism, not
  the shell.
- A `.profile` that prints a plain-text banner/menu line on login purely
  for UX (e.g. pointing out `mail` and `bulletins`) — cosmetic, not a
  security boundary.
- `curl` (or a small statically-linked Go helper) for the
  `user-service` lookup call, presence registration, and the `users`
  command's read-socket queries.
- The `users` command itself (§4.2) — a small script in `PATH`, plus
  whatever it needs for JSON output formatting (`jq`, or a few lines of
  `sed`/`cut` if we'd rather not add the dependency).

### ENTRYPOINT sequence

1. Read `$SRC_CALLSIGN`. Normalize: uppercase, strip a trailing AX.25 SSID
   (`-N`/`-NN`, e.g. `N0CALL-5` → `N0CALL`) — the SSID identifies a
   station/session, not a distinct BBS user.
2. `curl --unix-socket /bbs-sock/user-write.sock -d '{"callsign":"..."}' http://localhost/users/lookup`
   against the already-mounted socket directory (mounted at container
   start, no per-session coordination needed — see above). Get back
   `{ uid, created }`.
3. Register presence (§4.2) on the same write socket, in a backgrounded
   subshell that holds the connection open for the container's lifetime —
   *not* in the foreground script that will later `exec` into `dash`,
   so the interactive shell never inherits an open fd to the privileged
   socket. Since `user-write.sock` speaks real HTTP even for this
   endpoint (`user-service` reads one `POST /users/presence` request,
   then hijacks the connection and never writes a response — the held-
   open connection itself is the protocol), the request needs proper
   framing, not a bare JSON line.

   **A plain shell fd redirection cannot open this connection at all** —
   confirmed by testing: `exec 3<>/bbs-sock/user-write.sock` fails with
   `No such device or address`, because that's an `open(2)` against the
   socket's inode, and `open(2)` cannot `connect(2)` to an `AF_UNIX`
   socket. `socat` (already a required dependency, see §2.2) is needed
   to actually connect; `tail -f /dev/null` supplies an endless, silent
   input stream after the request bytes so the connection is held open
   by this backgrounded pipeline's own lifetime, without reading the
   container's real stdin (which belongs to the interactive session):
   ```sh
   ( body=$(printf '{"callsign":"%s","uid":%s}' "$CALLSIGN" "$UID")
     { printf 'POST /users/presence HTTP/1.1\r\nHost: user-service\r\nContent-Type: application/json\r\nContent-Length: %s\r\n\r\n%s' \
           "${#body}" "$body"
       exec tail -f /dev/null
     } | socat - "UNIX-CONNECT:/bbs-sock/user-write.sock" >/dev/null ) &
   ```
4. Start the outbound relay shim (§2.2):
   `socat TCP-LISTEN:2525,bind=127.0.0.1,fork,reuseaddr UNIX-CONNECT:/bbs-sock/mail/mailsock &`
   — this must be running before anything in the session tries to send
   mail, and it only needs loopback, which `--network none` still
   provides.
5. Provision the local account for *this* container instance, from
   scratch every time (the container itself is thrown away at session
   end, so nothing here persists in the image):
   ```
   groupadd -g $UID $CALLSIGN
   useradd  -u $UID -g $UID -d /bbs-data/home/$UID -M \
            -s /bin/dash $CALLSIGN
   ```
   `-M` (no home dir creation) because the home directory's *contents*
   live in the already-mounted `/bbs-data/home/$UID`, not in `useradd`'s
   skeleton. If this is the user's first login, `user-service` has
   already `mkdir -p`'d and `chown`'d `mail/<uid>` and `home/<uid>` as
   part of creating the record — the ephemeral container never creates
   these directories itself, only `user-service` does (keeping that
   responsibility in one place).
6. Set `MAIL=/bbs-data/mail/$UID/` (Maildir — trailing slash matches the
   directory, not a flat file; see §2.4/§4), `HOME=/bbs-data/home/$UID`.
7. Exec as that UID into `dash` (login shell), landing the user at a
   `.profile`-printed banner and a shell prompt. This process never had
   `user-write.sock` open (step 3 ran in a separate backgrounded
   subshell), so it inherits nothing it shouldn't; `/bbs-sock/user-read.sock`
   remains reachable throughout the session for the `users` command,
   since its permissive mode doesn't depend on which process is asking.

## 6. Security notes

- `--network none` on every ephemeral container: no possibility of
  reaching anything but the two mounted sockets and the mounted data
  directories, enforced by the container runtime, not by anything that
  could be bypassed from inside a compromised shell. The per-container
  `socat` relay shim (§2.2) only ever binds `127.0.0.1`, which is inside
  that same network namespace — it cannot be used to reach anything
  outside the container regardless of what a user does at the shell.
- Per-user isolation on `mail/` and `home/` is enforced by standard Unix
  permission bits (§5), not by Docker-level mount scoping — this is a
  deliberate choice, not a gap: it's the same model ordinary multi-user
  Unix systems have always used, and it's what lets mounts stay static
  and known before the UID is.
- `--read-only` root filesystem, `--cap-drop=ALL`, and per-container
  resource limits (`--memory`, `--pids-limit`) on every ephemeral
  container — the shell being unrestricted makes these matter more, not
  less, since the container boundary is now the *only* boundary.
- `mail-service` is the one container with the full `mail/` tree
  writable and `bbs.db` at all (read-only) — it's the trust boundary for
  both mail integrity and the user database.
- `user-service` is the only writer to `bbs.db` — everyone else,
  including `mail-service`, only ever reads it.
- `user-service`'s two sockets (§4.1) are the same permission-bits
  pattern as `mail/`/`home/`, applied to IPC instead of data: the
  provisioned shell account can never reach `user-write.sock` (mode
  `0700`, root-owned) regardless of what it runs, so there's no code
  path — buggy or malicious — from an unprivileged session to creating
  bogus user records or forging another callsign's presence. This
  depends on the ENTRYPOINT never leaking an open fd to that socket into
  the process it `exec`s into as the unprivileged user (§4.2) — worth a
  deliberate test once implemented (`ls -la /proc/self/fd` from the
  logged-in shell should show nothing pointing at `user-write.sock`).

## 7. Suggested build order

1. `user-service`: SQLite schema (embedded via `embed`), get-or-create
   endpoint over a Unix socket. Unit-testable in isolation — no
   containers needed yet.
2. Ephemeral container image + ENTRYPOINT, wired to `user-service`'s
   socket, whole-tree `mail`/`home` mounts, `dash` login shell, no mail
   delivery yet — prove login + account provisioning end to end
   (`curl --unix-socket` round-trip, `useradd`, landing at a prompt).
3. `mail-service`: Postfix `virtual_mailbox_maps` against `bbs.db`,
   `myorigin = bbs.local` (§2.3 — required), `smtpd` on the Unix socket
   per §2.1 (remembering both pre-created directories), plus stock
   `msmtp` + `socat` baked into the ephemeral image per §2.2/§2.3. Test
   with the literal command from the spec: `echo "Hello world" | mail -s
   "test message" n0call`.
4. Bulletins: a root-owned, no-write-bit Maildir read via `mail -f`
   through a one-line `/usr/local/bin/bulletins` wrapper — see §4's
   "Bulletins" subsection.

## 8. Open items to confirm during implementation

- **SSID-stripping rule**: assumed to be "strip a trailing `-` + 1-2
  digits"; confirm this matches whatever the AX.25 ingress actually hands
  the container in `$SRC_CALLSIGN`.
- **Mailbox directory listing leak** (§5): whether `0700` subdirectories
  under a listable `mail/`/`home/` parent (which reveals which numeric
  UIDs exist) is acceptable, or worth closing off with a non-listable
  parent mode.
- **`user-write.sock` fd-leak check** (§4.2/§6): confirm empirically,
  once the ENTRYPOINT is implemented, that the interactive shell process
  never inherits an open descriptor to the write socket — this is a
  process-hygiene requirement (background the presence-holder in a
  separate subshell before the `exec` into `dash`), not something the
  socket's `0700` mode enforces on its own.
- **Presence reconnect semantics**: if a station reconnects quickly
  (e.g. a flaky AX.25 link drops and the same callsign reconnects before
  `user-service` has processed the old connection's `EOF`), `users
  --online` may briefly show two entries for one callsign. Harmless for
  display (already de-duplicated), but worth deciding whether that's
  worth suppressing versus just documenting as expected.
- **Alpine map-type packaging for Postfix**: confirmed Alpine's base
  `postfix` package has no `hash:`/Berkeley DB support at all (`lmdb:` is
  the built-in default) and that `sqlite:` support requires the separate
  `postfix-sqlite` package — make sure both `mail-service`'s Dockerfile
  installs `postfix-sqlite` and that `main.cf`/any local map files this
  design references use `lmdb:` rather than `hash:` if a flat-file map is
  ever needed alongside the SQLite one.

## 9. Chat (deferred)

Dropped from this iteration because it doesn't fit the socket-only model
as directly as mail/user-lookup: `shazow/ssh-chat` (the earlier proposal)
is an SSH server, and SSH clients don't have a stock way to dial a Unix
socket the way `curl --unix-socket` or Postfix's `unix:` next-hop do.
When this comes back, the likely options are (a) a `socat`-style
loopback-TCP-to-socket shim per ephemeral container, same trick as the
mail fallback in §2, or (b) giving chat sessions a narrowly-scoped
network path (e.g. a second, still-`--internal`/no-internet network that
*only* chat-enabled containers join) rather than reusing the
`--network none` + socket approach used for mail/user lookups. Worth
revisiting once the mail/login flow is solid rather than guessing now.
