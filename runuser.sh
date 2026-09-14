#!/bin/bash

: "${BBS_DISABLE_ECHO:=1}"
: "${BBS_DISABLE_NETWORK:=1}"

while getopts c:l ch; do
  case $ch in
  c)
    SRC_CALLSIGN=$OPTARG
    ;;
  l)
    BBS_DISABLE_ECHO=0
    BBS_DISABLE_NETWORK=0
    ;;

  \?) exit 2 ;;
  esac
done
shift $((OPTIND - 1))

docker_args=()
if ((BBS_DISABLE_NETWORK)); then
  docker_args+=(--network none)
fi

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
  -e SRC_CALLSIGN="$SRC_CALLSIGN" \
  -e BBS_DISABLE_ECHO="${BBS_DISABLE_ECHO}" \
  "${docker_args[@]}" \
  "ghcr.io/larsks/unixbbs-user:${TAG:-latest}"
