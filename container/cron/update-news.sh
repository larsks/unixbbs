#!/bin/bash

: "${DATADIR:=/bbs-data}"
: "${NEWSDIR:="$DATADIR/news"}"
: "${ITEMDIR:="$DATADIR/news.items"}"

set -e

if ! [[ -d "$ITEMDIR/.git" ]]; then
  rm -rf "$ITEMDIR"
  git clone https://github.com/larsks/unixbbs-news "$ITEMDIR"
else
  git -C "$ITEMDIR" fetch origin --prune
  git -C "$ITEMDIR" reset --hard origin/main
  git -C "$ITEMDIR" clean -f -d -x
fi

newsdir=$(mktemp -d /tmp/newsXXXXXX)
trap 'rm -rf "$newsdir"' EXIT
mkdir -p "$newsdir"/{tmp,cur,new}

for item in "$ITEMDIR"/*; do
  safecat "$newsdir"/{tmp,cur} <"$item"
done

rm -rf "${NEWSDIR:?}"/*
cp -a "$newsdir"/* "$NEWSDIR"/
chmod 755 "$NEWSDIR"
