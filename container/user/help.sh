#!/bin/bash

: "${HELPDIR:=/helpfiles}"

show_help_for() {
  item="$HELPDIR/$1.txt"
  if [[ -f "$item" ]]; then
    base=${item##*/}
    name=${base%.txt}
    printf "\n=== %s ===\n\n" "$name"
    less "$item"
  else
    echo "No help for \"$1\"."
    exit 1
  fi
}

list_help_topics() {
  printf "\nAvailable help topics:\n\n"
  for item in "$HELPDIR"/*.txt; do
    base=${item##*/}
    name=${base%.txt}
    echo "$name"
  done
}

while getopts l ch; do
  case $ch in
  l)
    list_help_topics
    exit
    ;;

  \?)
    exit 2
    ;;
  esac
done
shift $((OPTIND - 1))

if [[ -z "$1" ]]; then
  show_help_for general
else
  show_help_for "$1"
fi
