#!/bin/sh
# Show the last 10 recorded logins, replacing the standard unix `last`
# command (which has nothing to read here -- no wtmp). See DESIGN.md
# §4.1/§4.2.
#
# Only ever talks to user-read.sock (world-connectable, 0666) -- this
# runs as the logged-in, unprivileged account and must not be able to
# reach user-write.sock.
set -e

READ_SOCK=/bbs-sock/user-read.sock

curl -s --unix-socket "$READ_SOCK" http://localhost/users/logins |
  jq -r '.[] | "\(.callsign)\t\(.logged_in_at)"'
