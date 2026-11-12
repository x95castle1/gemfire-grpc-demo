#!/usr/bin/env bash
# Interactive log viewer: pick an example (if several are running), then fuzzy-pick one or more
# of its containers. The preview pane shows each container's recent logs. ENTER follows the
# selected containers' logs.
#
#   scripts/logs.sh          # choose among the running examples
#   scripts/logs.sh 01       # go straight to example 01's containers
set -uo pipefail
cd "$(dirname "$0")/.."

command -v fzf >/dev/null || { echo "fzf is required (brew install fzf)" >&2; exit 1; }

# --- Pick the example ------------------------------------------------------------------------
if [ $# -gt 0 ] && [ -n "$1" ]; then
  dir=$(ls -d "$1"-*/ 2>/dev/null | head -1)
  dir=${dir%/}
  [ -n "$dir" ] || { echo "no example folder matches $1-*" >&2; exit 1; }
else
  running=()
  for d in [0-9][0-9]-*/; do
    d=${d%/}
    [ -f "$d/docker-compose.yml" ] || continue
    [ -n "$(cd "$d" && docker compose ps -a -q 2>/dev/null)" ] && running+=("$d")
  done
  case ${#running[@]} in
    0) echo "No examples running. Start one with: make start EX=01" >&2; exit 1 ;;
    1) dir=${running[0]} ;;
    *) dir=$(printf '%s\n' "${running[@]}" | fzf --height=40% --reverse --prompt='example> ' \
               --preview 'cd {} && docker compose ps -a --format "table {{.Service}}\t{{.State}}\t{{.Status}}"') \
         || exit 0 ;;
  esac
fi

cd "$dir" || exit 1

# --- Pick the containers ---------------------------------------------------------------------
# Clients first (the interesting logs), then everything else.
services=$(docker compose ps -a --format '{{.Service}}\t{{.State}}' | sort -k1,1 \
  | awk '/client/ { print; next } { rest = rest $0 "\n" } END { printf "%s", rest }')
[ -n "$services" ] || { echo "$dir has no containers" >&2; exit 1; }

selected=$(printf '%s\n' "$services" | fzf --multi --reverse --delimiter='\t' --with-nth=1,2 \
  --prompt="$dir logs> " \
  --header='TAB select several · ENTER follow · CTRL-A select all · ESC quit' \
  --bind 'ctrl-a:select-all' \
  --preview 'docker compose logs --no-color --no-log-prefix --tail 200 {1}' \
  --preview-window 'right,65%,follow,wrap' \
  | cut -f1) || exit 0
[ -n "$selected" ] || exit 0

# shellcheck disable=SC2086
exec docker compose logs -f --tail 200 $selected
