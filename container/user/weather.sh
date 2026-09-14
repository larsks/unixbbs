#!/bin/sh
# Display the local forecast returned by api-service.
set -e

READ_SOCK=/bbs-sock/api-read.sock

case "$1" in
"")
  FILTER='[.properties.periods[] | select(.name == "Today" or .name == "Tonight") | "\(.name)\n\(.detailedForecast)"] | join("\n\n")'
  ;;
--long)
  FILTER='[.properties.periods[] | select(.name == "Today" or .name == "Tonight" or .isDaytime == true) | "\(.name)\n\(.detailedForecast)"] | join("\n\n")'
  ;;
*)
  echo "usage: weather [--long]" >&2
  exit 1
  ;;
esac

echo
curl -sf --unix-socket "$READ_SOCK" http://localhost/weather |
  jq -r "$FILTER"
echo
