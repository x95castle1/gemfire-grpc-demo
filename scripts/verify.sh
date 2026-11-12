#!/usr/bin/env bash
# Smoke-tests examples: starts each one in MODE=demo, waits for its clients to finish, checks
# that every client exited 0 (all demo assertions passed), then tears it down.
#
#   scripts/verify.sh            # every NN-* example
#   scripts/verify.sh 01 03      # selected examples (by number prefix)
set -uo pipefail
cd "$(dirname "$0")/.."

if [ ! -f vendor/gemfire.proto ] || ! ls vendor/*.gfm >/dev/null 2>&1; then
  echo "vendor/ is missing gemfire.proto or the .gfm extension. See vendor/README.md." >&2
  exit 1
fi

examples=()
if [ $# -eq 0 ]; then
  for d in [0-9][0-9]-*/; do [ -f "$d/docker-compose.yml" ] && examples+=("${d%/}"); done
else
  for n in "$@"; do for d in "$n"-*/; do [ -d "$d" ] && examples+=("${d%/}"); done; done
fi

mkdir -p build/verify
declare -a results
overall=0

for ex in "${examples[@]}"; do
  echo "━━ ${ex} ━━"
  log="build/verify/${ex}.log"
  (
    cd "$ex" || exit 1
    MODE=demo docker compose up -d --build --quiet-pull >"../${log}" 2>&1 || { echo "compose up failed"; exit 2; }
    clients=$(docker compose config --services | grep -E 'client' || true)
    # shellcheck disable=SC2086
    docker compose wait $clients >/dev/null 2>&1
    status=0
    for svc in $clients; do
      code=$(docker inspect -f '{{.State.ExitCode}}' "$(docker compose ps -a -q "$svc")")
      summary=$(docker compose logs --no-log-prefix "$svc" 2>/dev/null | grep -E 'demo complete' | tail -1)
      printf '   %-22s exit=%s  %s\n' "$svc" "$code" "${summary//══/}"
      [ "$code" = "0" ] || status=1
    done
    docker compose logs --no-color >>"../${log}" 2>&1
    docker compose down -v >/dev/null 2>&1
    exit $status
  )
  rc=$?
  if [ $rc -eq 0 ]; then results+=("PASS  ${ex}"); else results+=("FAIL  ${ex}   (log: ${log})"); overall=1; fi
done

echo
echo "━━ summary ━━"
printf '%s\n' "${results[@]}"
exit $overall
