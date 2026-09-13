#!/bin/sh
# mail-service ENTRYPOINT. See DESIGN.md §2.1 for why the two
# directories below must exist under $queue_directory before `master`
# starts: without them, `postfix start` reports success and the first
# connection or two can even succeed before `smtpd` starts throttling on
# a lock-file error visible only in the log.
set -e

install -d -o postfix -g postdrop -m 2710 /var/spool/postfix/public/bbssock
install -d -o root    -g root     -m 0755 /var/spool/postfix/pid/unix.bbssock

# This minimal image runs no syslog daemon by default, so Postfix's
# (unchanged, default) syslog-based logging would otherwise go nowhere.
# busybox syslogd -C logs to a fixed-size in-memory ring buffer instead
# of a file -- confirmed working with Postfix's own logging -- so there's
# no on-disk log to grow or rotate; read it with `logread`.
syslogd -C64

postfix check
exec postfix start-fg
