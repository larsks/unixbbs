#!/bin/bash

if ! [[ -d /bbs-data/news.items ]]; then
  echo "No news items."
  exit
fi

rm -rf /bbs-data/news
mkdir -p /bbs-data/news/{tmp,cur,new}
for item in /bbs-data/news.items/*; do
  ts=$(stat -c '%Z' "$item")
  date=$(date -R -d "@$ts")
  (
    echo "From: Operator"
    echo "To: All"
    echo "Date: $date"
    cat "$item"
  ) | safecat /bbs-data/news/{tmp,cur}
done
