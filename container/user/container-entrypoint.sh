#!/bin/sh
# Ephemeral user container ENTRYPOINT. See DESIGN.md §5 for the full
# rationale behind each step.
set -e

# Restore /etc from the build-time snapshot: with the container run
# `--read-only` + `--tmpfs /etc` (see DESIGN.md §6/§5), /etc starts
# every session as an empty, writable tmpfs -- this repopulates it with
# the image's baked-in passwd/group/shadow/gshadow (and msmtprc,
# bbs-profile) before anything reads or writes any of it.
#
# hostname/hosts/resolv.conf are skipped -- Docker always bind-mounts
# these three individually (confirmed by testing, even under
# `--network none`), so they already exist as distinct mounts inside
# the otherwise-empty tmpfs, and copying over them fails outright
# ("File exists"); Docker's own per-container values for these are
# what we want anyway, not the image's static originals.
for entry in /etc.orig/*; do
  name=$(basename "$entry")
  case "$name" in
  hostname | hosts | resolv.conf) continue ;;
  esac
  cp -a "$entry" "/etc/$name"
done

WRITE_SOCK=/bbs-sock/api-write.sock
MAIL_SOCK=/bbs-sock/mail/mailsock

# 1. Normalize the callsign: uppercase, strip a trailing AX.25 SSID
#    (e.g. N0CALL-5 -> N0CALL) -- the SSID identifies a station/session,
#    not a distinct BBS user.
CALLSIGN=$(printf '%s' "$SRC_CALLSIGN" |
  tr '[:lower:]' '[:upper:]' |
  sed -E 's/-[0-9]{1,2}$//')

if [ -z "$CALLSIGN" ]; then
  echo "container-entrypoint: SRC_CALLSIGN is empty" >&2
  exit 1
fi

# 2. Look up (or create) this user's account.
LOOKUP_RESPONSE=$(curl -sf --unix-socket "$WRITE_SOCK" \
  -X POST -H 'Content-Type: application/json' \
  -d "{\"callsign\":\"$CALLSIGN\"}" \
  http://localhost/users/lookup)
USERID=$(printf '%s' "$LOOKUP_RESPONSE" | jq -r '.uid')

if [ -z "$USERID" ] || [ "$USERID" = "null" ]; then
  echo "container-entrypoint: lookup failed for $CALLSIGN: $LOOKUP_RESPONSE" >&2
  exit 1
fi

# 3. Register presence on a *separate* connection, held open for the
#    container's lifetime, in a backgrounded subshell -- not the
#    foreground script that will later exec into dash, so the
#    interactive shell never inherits an open fd to api-write.sock.
#    The connection is real HTTP (POST /users/presence): api-service
#    reads this one request, then hijacks the connection and never
#    writes a response -- the held-open connection itself is the
#    protocol (see DESIGN.md §4.1/§4.2).
#
#    `exec 3<>"$WRITE_SOCK"` (a plain shell fd redirection) does NOT
#    work here: it's an open(2) on the socket's inode, and open(2)
#    cannot connect(2) to an AF_UNIX socket -- confirmed by testing
#    ("No such device or address"). socat is required to actually
#    connect. `tail -f /dev/null` supplies an endless, silent input
#    stream after the request bytes so the connection is held open by
#    this backgrounded pipeline's own lifetime, without reading the
#    container's real stdin (which belongs to the interactive session).
(
  body=$(printf '{"callsign":"%s","uid":%s}' "$CALLSIGN" "$USERID")
  {
    printf 'POST /users/presence HTTP/1.1\r\nHost: api-service\r\nContent-Type: application/json\r\nContent-Length: %s\r\n\r\n%s' \
      "${#body}" "$body"
    exec tail -f /dev/null
  } | socat - "UNIX-CONNECT:$WRITE_SOCK" >/dev/null
) &

# 4. Start the outbound relay shim (DESIGN.md §2.2). Backgrounded and
#    allowed to fail quietly if mail-service isn't reachable yet --
#    `mail` will simply fail at send time in that case (§2.3).
socat TCP-LISTEN:2525,bind=127.0.0.1,fork,reuseaddr "UNIX-CONNECT:$MAIL_SOCK" \
  >/dev/null 2>&1 &

# 5. Provision the local account for this container instance, from
#    scratch every time. `api-service` is responsible for mkdir/chown
#    of mail/<uid> and home/<uid> on first login -- this container never
#    creates them itself.
groupadd -g "$USERID" "$CALLSIGN"
useradd -u "$USERID" -g "$USERID" -d "/bbs-data/home/$USERID" -M \
  -K UID_MIN=10000 -K UID_MAX=2147483647 \
  -s /usr/bin/dash "$CALLSIGN"

# 6. Environment for the session.
export MAIL="/bbs-data/mail/$USERID/"
export HOME="/bbs-data/home/$USERID"

# 7. Exec as the provisioned account into dash. This process never had
#    api-write.sock open (step 3 ran in a separate backgrounded
#    subshell), so it inherits nothing it shouldn't.
#
#    `su-exec` only changes uid/gid -- it doesn't chdir(), so without an
#    explicit `cd` here the session would start in the entrypoint's own
#    cwd (`/`, the image's default WORKDIR) despite $HOME being set
#    correctly. The `cd` runs as the already-unprivileged account, which
#    is fine: it owns its own home directory (0700, see §5).
exec su-exec "$CALLSIGN" /usr/bin/dash -c 'cd "$HOME"; . /etc/bbs-profile; exec "$@"' -- "$@"
