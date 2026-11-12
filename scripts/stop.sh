#!/usr/bin/env bash
# Stops examples and removes their volumes.
#
#   scripts/stop.sh          # every example that has containers
#   scripts/stop.sh 01       # just example 01
set -uo pipefail
cd "$(dirname "$0")/.."

targets=()
if [ $# -gt 0 ] && [ -n "$1" ]; then
  for d in "$1"-*/; do [ -d "$d" ] && targets+=("${d%/}"); done
  [ ${#targets[@]} -gt 0 ] || { echo "no example folder matches $1-*" >&2; exit 1; }
else
  for d in [0-9][0-9]-*/; do
    d=${d%/}
    [ -f "$d/docker-compose.yml" ] || continue
    [ -n "$(cd "$d" && docker compose ps -a -q 2>/dev/null)" ] && targets+=("$d")
  done
  [ ${#targets[@]} -gt 0 ] || { echo "Nothing running."; exit 0; }
fi

for d in "${targets[@]}"; do
  echo "Stopping ${d} ..."
  (cd "$d" && docker compose down -v --remove-orphans 2>&1 | grep -v -E '^\s*$' | tail -1)
done
