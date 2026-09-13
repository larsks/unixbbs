#!/bin/sh
# mail-service ENTRYPOINT. See DESIGN.md §2.1 for why the two
# directories below must exist under $queue_directory before `master`
# starts: without them, `postfix start` reports success and the first
# connection or two can even succeed before `smtpd` starts throttling on
# a lock-file error visible only in the log.
set -e

# Mode 2710 is Postfix's normal "public" convention (owner postfix,
# group postdrop, no access for anyone else) -- but bbssock/mailsock is
# `private = n` in master.cf (world-connectable, mode 0666, confirmed by
# testing) specifically so that ephemeral user containers -- each a
# throwaway, dynamically-provisioned uid/gid with no relation to
# postfix/postdrop -- can connect to it. Without `+x` for "other" here,
# those containers get "Permission denied" on connect(2) since they lack
# search permission on this directory (confirmed by testing), even
# though the socket file itself is wide open. The extra `x` only grants
# directory traversal, not write, so creating/removing entries here still
# requires being postfix or in the postdrop group.
install -d -o postfix -g postdrop -m 2711 /var/spool/postfix/public/bbssock
install -d -o root -g root -m 0755 /var/spool/postfix/pid/unix.bbssock

# The shared read-only mailbox (DESIGN.md, virtual-shared-mailbox/
# virtual-shared-transport/deliver-shared): owned by the reserved uid/gid
# 5000 the 'shared' pipe(8) transport runs safecat as,
# world-readable/traversable but not world-writable, so every user's own
# uid can read it but only that transport (running as uid/gid 5000) can
# write to it. mail-service is the only container with bbs-data mounted
# read-write, so this is the natural place to create it -- same as
# bbssock above.
#
# /bbs-data/mail is created first, separately and root-owned: `install
# -d` (like Go's os.MkdirAll -- see DESIGN.md §3's EnsureUserDirs bug)
# applies its mode/owner to every directory it has to create along the
# path, not just the leaf. Without this, on a fresh volume where
# mail/ doesn't exist yet, creating mail/shared directly in one call
# would leave mail/ itself owned 5000:5000 instead of root:root.
#
# tmp/new/cur are created explicitly here because, unlike the virtual(8)
# agent, neither Postfix's pipe(8) transport nor safecat itself creates
# maildir subdirectories -- safecat's own docs say it stat()s both
# directories it's given and exits unless both already exist. cur/ is
# created for maildir-tool compatibility even though nothing ever writes
# to it: this mailbox is read-only, so a mail client can never move a
# message out of new/ once delivered.
install -d -o root -g root -m 0755 /bbs-data/mail
install -d -o 5000 -g 5000 -m 0755 /bbs-data/mail/shared
install -d -o 5000 -g 5000 -m 0755 /bbs-data/mail/shared/tmp
install -d -o 5000 -g 5000 -m 0755 /bbs-data/mail/shared/new
install -d -o 5000 -g 5000 -m 0755 /bbs-data/mail/shared/cur

# This minimal image runs no syslog daemon by default, so Postfix's
# (unchanged, default) syslog-based logging would otherwise go nowhere.
# busybox syslogd -C logs to a fixed-size in-memory ring buffer instead
# of a file -- confirmed working with Postfix's own logging -- so there's
# no on-disk log to grow or rotate; read it with `logread`.
syslogd -C64

postfix check
exec postfix start-fg
