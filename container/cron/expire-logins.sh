#!/bin/bash
# Expire login history rows older than 14 days. user-service is the
# only writer to bbs.db (see DESIGN.md §4/§4.1), so this goes through
# its write socket rather than touching the database file directly.
set -e

: "${WRITE_SOCK:=/bbs-sock/user-write.sock}"

curl -sf --unix-socket "$WRITE_SOCK" -X POST http://localhost/users/logins/expire
