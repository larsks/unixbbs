#!/bin/bash

: "${BBS_DISABLE_ECHO:=1}"

if [[ -z "$SRC_CALLSIGN" ]]; then
  echo "ERROR: unable to determine callsign" >&2
  exit 1
fi

container_name="bbs-user-${SRC_CALLSIGN,,}"

if docker container inspect "$container_name" >&/dev/null; then
  docker rm -f "$container_name" >&/dev/null
fi

docker run --rm -it \
  --name "$container_name" \
  --network none \
  --read-only \
  --tmpfs /etc \
  --tmpfs /tmp \
  --pids-limit=64 \
  --memory=64m \
  --hostname bbs.local \
  --add-host bbs.local:127.0.1.1 \
  --dns-option timeout:1 --dns-option attempts:1 \
  -v unixbbs-data:/bbs-data \
  -v unixbbs-sock:/bbs-sock \
  -v unixbbs-mailsock:/bbs-sock/mail \
  -v unixbbs-chatsock:/bbs-sock/chat \
  -e SRC_CALLSIGN \
  -e BBS_DISABLE_ECHO="${BBS_DISABLE_ECHO}" \
  "ghcr.io/larsks/unixbbs-user:${TAG:-latest}"
