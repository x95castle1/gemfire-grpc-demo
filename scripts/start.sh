#!/usr/bin/env bash
# Builds and starts an example.
#
#   scripts/start.sh 01      # example 01
#   scripts/start.sh         # pick one with fzf
set -uo pipefail
cd "$(dirname "$0")/.."

if [ $# -gt 0 ] && [ -n "$1" ]; then
  dir=$(ls -d "$1"-*/ 2>/dev/null | head -1)
else
  command -v fzf >/dev/null || { echo "usage: make start EX=01  (or install fzf to pick interactively)" >&2; exit 1; }
  dir=$(ls -d [0-9][0-9]-*/ | while read -r d; do [ -f "$d/docker-compose.yml" ] && echo "${d%/}"; done \
        | fzf --height=40% --reverse --prompt='start example> ' --preview 'head -40 {}/README.md') || exit 0
fi
dir=${dir%/}
[ -n "$dir" ] && [ -f "$dir/docker-compose.yml" ] || { echo "no runnable example matches ${1:-selection}" >&2; exit 1; }

(cd "$dir" && docker compose up -d --build) || exit 1
echo
echo "Grafana:    http://localhost:$(grep '^GRAFANA_PORT=' "$dir/.env" | cut -d= -f2)"
echo "Prometheus: http://localhost:$(grep '^PROMETHEUS_PORT=' "$dir/.env" | cut -d= -f2)"
