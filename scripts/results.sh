#!/usr/bin/env bash
# Browses past load-test runs (load-testing/results/<run>/), newest first. The preview shows each
# run's comparison table; ENTER prints it with the run's path.
#
#   scripts/results.sh                 # pick with fzf
#   scripts/results.sh latest          # print the newest run
#   scripts/results.sh 20261112-1504   # print the first run matching a prefix
set -uo pipefail
cd "$(dirname "$0")/.."

dir=load-testing/results
runs=$(ls -1d "$dir"/*/ 2>/dev/null | sed "s|^$dir/||; s|/$||" | sort -r)
[ -n "$runs" ] || { echo "No load-test results yet. Run one with: make select-load-test"; exit 0; }

pick=${1:-}
if [ "$pick" = latest ]; then
  run=$(head -1 <<<"$runs")
elif [ -n "$pick" ]; then
  run=$(grep -m1 "^$pick" <<<"$runs") || { echo "no run matches $pick" >&2; exit 1; }
else
  command -v fzf >/dev/null || { echo "fzf is required (brew install fzf), or pass a run: make results RUN=latest" >&2; exit 1; }
  run=$(fzf --reverse --header='Load-test runs (newest first) · ENTER to print' \
          --preview "cat $dir/{}/summary.txt 2>/dev/null || echo '(no summary: run was interrupted)'; echo; ls $dir/{}" \
          --preview-window=down,65%,wrap <<<"$runs") || exit 0
fi

cat "$dir/$run/summary.txt" 2>/dev/null || echo "(no summary: run was interrupted)"
echo
echo "Files: $dir/$run/"
ls -1 "$dir/$run" | sed 's/^/  /'
