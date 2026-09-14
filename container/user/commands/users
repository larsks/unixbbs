#!/bin/sh
# List known or currently-online BBS users. See DESIGN.md §4.1/§4.2.
#
# Only ever talks to api-read.sock (world-connectable, 0666) -- this
# runs as the logged-in, unprivileged account and must not be able to
# reach api-write.sock.
set -e

READ_SOCK=/bbs-sock/api-read.sock

case "$1" in
-o | --online)
  curl -s --unix-socket "$READ_SOCK" http://localhost/users/online |
    jq -r '.[].callsign' | sort -u
  ;;
"")
  curl -s --unix-socket "$READ_SOCK" http://localhost/users/list |
    jq -r '.[].callsign' | sort -u
  ;;
*)
  echo "usage: users [-o|--online]" >&2
  exit 1
  ;;
esac
