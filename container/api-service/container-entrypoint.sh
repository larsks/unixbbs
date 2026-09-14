#!/bin/bash

: "${BBS_DATA_DIR:=/bbs-data}"

declare -A question_data=(
  ["technician"]=https://raw.githubusercontent.com/russolsen/ham_radio_question_pool/refs/heads/main/technician-2026-2030/technician-2026-2030.json
  ["general"]=https://raw.githubusercontent.com/russolsen/ham_radio_question_pool/refs/heads/main/general-2023-2027/general-2023-2027.json
  ["extra"]=https://raw.githubusercontent.com/russolsen/ham_radio_question_pool/refs/heads/main/extra-2024-2028/extra-2024-2028.json
)

for pool in "${!question_data[@]}"; do
  poolDir="$BBS_DATA_DIR/pools/$pool"
  mkdir -p "$poolDir"

  if ! [[ -f "$poolDir/questions.json" ]]; then
    echo "Fetching questions for $pool pool"
    curl -sfL -o "$poolDir/questions.json" "${question_data[$pool]}"
  fi
done

exec /usr/local/bin/api-service "$@"
