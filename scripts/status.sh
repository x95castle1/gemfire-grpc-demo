#!/usr/bin/env bash
# Shows which examples are running and the state of each of their containers.
#
#   scripts/status.sh        # every example that has containers
#   scripts/status.sh 01     # one example (also shows a stopped one)
set -uo pipefail
cd "$(dirname "$0")/.."

bold=$'\e[1m'; dim=$'\e[2m'; green=$'\e[32m'; reset=$'\e[0m'

show() {
  local dir=$1
  local env="$dir/.env"
  local grafana prometheus
  grafana=$(grep -E '^GRAFANA_PORT=' "$env" 2>/dev/null | cut -d= -f2)
  prometheus=$(grep -E '^PROMETHEUS_PORT=' "$env" 2>/dev/null | cut -d= -f2)

  local rows
  rows=$(cd "$dir" && docker compose ps -a --format '{{.Service}}\t{{.State}}\t{{.Status}}' 2>/dev/null | sort)
  if [ -z "$rows" ]; then
    printf '%s%s%s  %snot running%s\n\n' "$bold" "$dir" "$reset" "$dim" "$reset"
    return
  fi
  local running total
  running=$(grep -c $'\trunning\t' <<<"$rows")
  total=$(wc -l <<<"$rows" | tr -d ' ')
  printf '%s%s%s  %s%s/%s running%s' "$bold" "$dir" "$reset" "$green" "$running" "$total" "$reset"
  [ -n "$grafana" ] && printf '   grafana http://localhost:%s   prometheus http://localhost:%s' "$grafana" "$prometheus"
  printf '\n'
  printf '%s\n' "$rows" | awk -F'\t' '{ printf "  %-22s %-9s %s\n", $1, $2, $3 }'
  printf '\n'
}

if [ $# -gt 0 ] && [ -n "$1" ]; then
  for d in "$1"-*/; do [ -d "$d" ] && show "${d%/}"; done
  exit 0
fi

found=false
for d in [0-9][0-9]-*/; do
  dir=${d%/}
  [ -f "$dir/docker-compose.yml" ] || continue
  if [ -n "$(cd "$dir" && docker compose ps -a -q 2>/dev/null)" ]; then
    show "$dir"
    found=true
  fi
done
$found || echo "No examples running. Start one with: make start EX=01"
