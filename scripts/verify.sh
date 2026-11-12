#!/usr/bin/env bash
# Smoke-tests examples. For each one: start it, wait for every client service to be healthy,
# run its narrated demo (POST /demo, 200 = every assertion passed), then tear it down.
#
#   scripts/verify.sh            # every NN-* example
#   scripts/verify.sh 01 03      # selected examples (by number prefix)
set -uo pipefail
cd "$(dirname "$0")/.."

if [ ! -f gemfire-extensions/gemfire.proto ] || ! ls gemfire-extensions/*.gfm >/dev/null 2>&1; then
  echo "gemfire-extensions/ is missing gemfire.proto or the .gfm extension. See gemfire-extensions/README.md." >&2
  exit 1
fi

examples=()
if [ $# -eq 0 ] || [ -z "${1:-}" ]; then
  for d in [0-9][0-9]-*/; do [ -f "$d/docker-compose.yml" ] && examples+=("${d%/}"); done
else
  for n in "$@"; do for d in "$n"-*/; do [ -d "$d" ] && examples+=("${d%/}"); done; done
fi

mkdir -p build/verify
results=()
overall=0

for ex in "${examples[@]}"; do
  echo "━━ ${ex} ━━"
  log="build/verify/${ex}.log"
  (
    cd "$ex" || exit 1
    # Skip the startup demo; verify runs it explicitly below.
    DEMO_ON_START=false docker compose up -d --build --quiet-pull >"../${log}" 2>&1 || { echo "   compose up failed (see ${log})"; exit 2; }
    clients=$(docker compose config --services | grep -E -- '-client-' || true)

    for _ in $(seq 1 120); do
      pending=0
      for svc in $clients; do
        [ "$(docker compose ps --format '{{.Health}}' "$svc" 2>/dev/null)" = healthy ] || pending=1
      done
      [ $pending -eq 0 ] && break
      sleep 2
    done

    status=0
    for svc in $clients; do
      if [ $pending -ne 0 ] && [ "$(docker compose ps --format '{{.Health}}' "$svc")" != healthy ]; then
        printf '   %-22s not healthy\n' "$svc"; status=1; continue
      fi
      out=$(docker compose exec -T "$svc" curl -sS -w '\n%{http_code}' -X POST http://localhost:8080/demo 2>&1)
      code=$(tail -1 <<<"$out")
      printf '%s\n' "$out" >>"../${log}"
      summary=$(grep -E 'demo complete' <<<"$out" | tail -1 | sed -E 's/.*demo complete: //; s/ ══//')
      printf '   %-22s HTTP %s  %s\n' "$svc" "$code" "$summary"
      [ "$code" = 200 ] || status=1
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
