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

# The fixed 'news' pseudo-recipient's Maildir, owned by the fixed uid/gid
# those maps resolve it to -- read directly from virtual-news-uid/-gid
# (the same texthash: files Postfix itself consults, see the
# Containerfile) rather than duplicating the number here, so the two
# can't drift out of sync. mail-service is the only container with
# bbs-data mounted read-write, so this is the natural place to create
# it -- same as bbssock above. /bbs-data/mail is created first,
# separately and root-owned: `install -d` (like Go's os.MkdirAll -- see
# src/api-service/internal/provision/provision.go's EnsureUserDirs) applies
# its mode/owner to every directory it has to create along the path, not
# just the leaf, so creating news-inbox/ directly in one call on a fresh
# volume would leave mail/ itself owned by the news uid/gid instead of
# root:root. Mode 0700 matches the per-user Maildir convention
# EnsureUserDirs uses.
news_uid=$(awk '$1 == "news" { print $2 }' /etc/postfix/virtual-news-uid)
news_gid=$(awk '$1 == "news" { print $2 }' /etc/postfix/virtual-news-gid)
install -d -o root -g root -m 0755 /bbs-data/mail
install -d -o "$news_uid" -g "$news_gid" -m 0700 /bbs-data/mail/news-inbox
install -d -o "$news_uid" -g "$news_gid" -m 0700 /bbs-data/mail/news-inbox/tmp
install -d -o "$news_uid" -g "$news_gid" -m 0700 /bbs-data/mail/news-inbox/new
install -d -o "$news_uid" -g "$news_gid" -m 0700 /bbs-data/mail/news-inbox/cur

# This minimal image runs no syslog daemon by default, so Postfix's
# (unchanged, default) syslog-based logging would otherwise go nowhere.
# busybox syslogd -C logs to a fixed-size in-memory ring buffer instead
# of a file -- confirmed working with Postfix's own logging -- so there's
# no on-disk log to grow or rotate; read it with `logread`.
syslogd -C64

postfix check
exec postfix start-fg
