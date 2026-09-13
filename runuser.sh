#!/bin/bash

if [[ -z "$SRC_CALLSIGN" ]]; then
  echo "ERROR: unable to determine callsign" >&2
  exit 1
fi

docker run --rm -it \
  --name "bbs-user-${SRC_CALLSIGN,,}" \
  --network none \
  --read-only \
  --tmpfs /etc \
  --tmpfs /tmp \
  --pids-limit=64 \
  --memory=64m \
  --hostname bbs.local \
  -v unixbbs-data:/bbs-data \
  -v unixbbs-sock:/bbs-sock \
  -v unixbbs-mailsock:/bbs-sock/mail \
  -e SRC_CALLSIGN \
  unixbbs-user
