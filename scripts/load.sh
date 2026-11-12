#!/usr/bin/env bash
# Runs a k6 load test (load-testing/scripts/**.js) against an example's client services and
# prints a native-vs-gRPC comparison table.
#
# Everything is optional; anything left out is picked with fzf:
#   EX=01                 example (starts it if it isn't running)
#   CLIENT=all            all | grpc-java | native-java | ...
#   SCRIPT=crud/steady    path under load-testing/scripts, without .js
#   RUN=parallel          parallel (all clients at once) | sequential (one after another);
#                         defaults to the script's "// Run:" header
#   K6_ARGS="-e RATE=3000 -e DURATION=1m"   extra k6 arguments
#
# k6 runs as a throwaway container on the example's Docker network and pushes its metrics to
# that example's Prometheus, so they show up in its Grafana dashboard.
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

K6_IMAGE=${K6_IMAGE:-grafana/k6:2.3.0}
EX=${EX:-}; CLIENT=${CLIENT:-}; SCRIPT=${SCRIPT:-}; RUN=${RUN:-}; K6_ARGS=${K6_ARGS:-}

bold=$'\e[1m'; dim=$'\e[2m'; red=$'\e[31m'; green=$'\e[32m'; reset=$'\e[0m'
die() { echo "${red}✘${reset} $*" >&2; exit 1; }
info() { echo "${green}✓${reset} $*"; }

missing=0
for tool in docker jq curl; do
  command -v "$tool" >/dev/null || { echo "✘ $tool is required (brew install $tool)" >&2; missing=1; }
done
[ $missing -eq 0 ] || exit 1
# Interactive pickers need a terminal. Without one (CI, scripts), fail fast instead of hanging.
needFzf() {
  [ -t 0 ] && [ -t 1 ] || die "no terminal to choose $1 interactively: pass it on the command line (EX=, SCRIPT=, CLIENT=)"
  command -v fzf >/dev/null || die "fzf is required to choose $1 interactively (brew install fzf), or pass it on the command line"
}

# macOS ships bash 3.2: no associative arrays, so per-client values live in variables
# named <name>__<client> (dashes become underscores).
kvSet() { eval "$1__${2//-/_}=\$3"; }
kvGet() { eval "printf '%s' \"\${$1__${2//-/_}:-}\""; }

isRunning() { [ -n "$(cd "$1" && docker compose ps -q 2>/dev/null)" ]; }
envValue() { grep -E "^$2=" "$1/.env" 2>/dev/null | cut -d= -f2; }

# --- 1. Example --------------------------------------------------------------------------------
if [ -n "$EX" ]; then
  dir=$(ls -d "$EX"-*/ 2>/dev/null | head -1); dir=${dir%/}
  [ -n "$dir" ] && [ -f "$dir/docker-compose.yml" ] || die "no runnable example matches $EX-*"
else
  running=(); all=()
  for d in [0-9][0-9]-*/; do
    d=${d%/}; [ -f "$d/docker-compose.yml" ] || continue
    all+=("$d"); isRunning "$d" && running+=("$d")
  done
  if [ ${#running[@]} -eq 1 ]; then
    dir=${running[0]}
  else
    needFzf "the example"
    if [ ${#running[@]} -eq 0 ]; then choices=("${all[@]}"); else choices=("${running[@]}"); fi
    dir=$(printf '%s\n' "${choices[@]}" | fzf --height=40% --reverse \
      --header="$([ ${#running[@]} -eq 0 ] && echo 'Nothing is running. Pick an example to start and load test:' || echo 'Example to load test:')") || exit 0
  fi
fi
num=${dir%%-*}

if ! isRunning "$dir"; then
  info "Starting ${dir} first..."
  ./scripts/start.sh "$num" || exit 1
fi

# --- 2. Wait for the client services ----------------------------------------------------------
clientServices() { (cd "$dir" && docker compose config --services) | grep -E -- '-client-' ; }
echo -n "Waiting for ${dir}'s clients to be healthy"
for _ in $(seq 1 90); do
  unhealthy=0
  for svc in $(clientServices); do
    h=$(cd "$dir" && docker compose ps --format '{{.Health}}' "$svc" 2>/dev/null)
    [ "$h" = healthy ] || unhealthy=1
  done
  [ $unhealthy -eq 0 ] && break
  echo -n "."; sleep 2
done
echo
[ $unhealthy -eq 0 ] || die "clients didn't become healthy. Check: make logs EX=${num}"

svcToClient() { echo "$1" | sed 's/-client-/-/'; }   # grpc-client-java -> grpc-java
available=(); for svc in $(clientServices); do available+=("$(svcToClient "$svc")"); done

# --- 3. Script ---------------------------------------------------------------------------------
header() { sed -n "s|^// $2: *||p" "$1" | head -1; }
scriptWorks() {   # file -> works with this example and at least one running client?
  local f=$1 ex clients c
  ex=$(header "$f" Examples)
  [ "$ex" = all ] || [[ ",${ex// /}," == *",${num},"* ]] || return 1
  clients=$(header "$f" Clients)
  for c in "${available[@]}"; do [[ ",${clients// /}," == *",${c},"* ]] && return 0; done
  return 1
}

if [ -n "$SCRIPT" ]; then
  file="load-testing/scripts/${SCRIPT%.js}.js"
  [ -f "$file" ] || die "script not found: $file"
  scriptWorks "$file" || die "$file doesn't support example ${num} with clients: ${available[*]}"
else
  needFzf "the script"
  file=$(find load-testing/scripts -name '*.js' | sort | while read -r f; do
           scriptWorks "$f" && printf '%s\t%s\n' "${f#load-testing/scripts/}" "$(header "$f" Description)"
         done | column -t -s $'\t' |
         fzf --height=60% --reverse --header="Load test for ${dir}:" \
           --preview 'sed -n "1,/^import/p" load-testing/scripts/{1} | grep "^//" | sed "s|^// ||"; echo; sed -n "/export const options/,/^});/p" load-testing/scripts/{1}' \
           --preview-window=right,55%,wrap | awk '{print $1}') || exit 0
  [ -n "$file" ] || exit 0
  file="load-testing/scripts/$file"
fi
SCRIPT=${file#load-testing/scripts/}; SCRIPT=${SCRIPT%.js}
supported=$(header "$file" Clients | tr -d ' ')

# --- 4. Client(s) ------------------------------------------------------------------------------
targets=()
for c in "${available[@]}"; do [[ ",${supported}," == *",${c},"* ]] && targets+=("$c"); done
if [ -z "$CLIENT" ]; then
  if [ ${#targets[@]} -gt 1 ] && [ -t 0 ] && [ -t 1 ]; then
    needFzf "the client"
    CLIENT=$(printf '%s\n' all "${targets[@]}" | fzf --height=30% --reverse \
      --header="Which client? (all = compare them)") || exit 0
  else
    CLIENT=all
  fi
fi
if [ "$CLIENT" != all ]; then
  [[ " ${targets[*]} " == *" ${CLIENT} "* ]] || die "CLIENT must be all or one of: ${targets[*]}"
  targets=("$CLIENT")
fi
[ -n "$RUN" ] || RUN=$(header "$file" Run)
[ "$RUN" = sequential ] || RUN=parallel
[ ${#targets[@]} -eq 1 ] && RUN=sequential

# --- 5. Run ------------------------------------------------------------------------------------
project=$(cd "$dir" && docker compose config --format json | jq -r .name)
network="${project}_default"
grafanaPort=$(envValue "$dir" GRAFANA_PORT)
promPort=$(envValue "$dir" PROMETHEUS_PORT)
runId="$(date +%Y%m%d-%H%M%S)-${num}-${SCRIPT//\//-}"
results="load-testing/results/${runId}"
mkdir -p "$results"
extraArgs=(); [ -n "$K6_ARGS" ] && read -r -a extraArgs <<< "$K6_ARGS"

echo
echo "${bold}━━ ${dir} · ${SCRIPT} · ${targets[*]} · ${RUN} ━━${reset}"
echo "  $(header "$file" Description)"
[ -n "$K6_ARGS" ] && echo "  k6 args: $K6_ARGS"
echo "  results: ${results}/   grafana: http://localhost:${grafanaPort}"
echo

jq -n --arg ex "$dir" --arg script "$SCRIPT" --arg run "$RUN" --arg args "$K6_ARGS" \
  --arg image "$K6_IMAGE" --argjson clients "$(printf '%s\n' "${targets[@]}" | jq -R . | jq -s .)" \
  '{example:$ex, script:$script, clients:$clients, run:$run, k6_args:$args, k6_image:$image}' > "$results/meta.json"
cp "$file" "$results/"

containers=()
cleanup() { [ ${#containers[@]} -gt 0 ] && docker rm -f "${containers[@]}" >/dev/null 2>&1; }
trap 'echo; echo "Interrupted, stopping k6..."; cleanup; exit 130' INT TERM

k6Run() {   # client -> runs k6 against it; output to stdout
  local client=$1 svc
  svc=$(echo "$client" | sed 's/-/-client-/')
  docker run --rm --name "k6-${runId}-${client}" --network "$network" \
    -v "$ROOT/load-testing:/load:ro" -v "$ROOT/$results:/results" \
    -e BASE_URL="http://${svc}:8080" \
    -e K6_PROMETHEUS_RW_SERVER_URL=http://prometheus:9090/api/v1/write \
    -e K6_PROMETHEUS_RW_TREND_AS_NATIVE_HISTOGRAM=true \
    -e K6_PROMETHEUS_RW_PUSH_INTERVAL=5s \
    -e K6_NO_USAGE_REPORT=true \
    "$K6_IMAGE" run --quiet -o experimental-prometheus-rw \
      --tag client="$client" --tag run="$runId" --tag example="$dir" --tag script="$SCRIPT" \
      --summary-export "/results/${client}.json" \
      ${extraArgs[@]+"${extraArgs[@]}"} "/load/scripts/${SCRIPT}.js"
}

annotate() {   # start_ms end_ms text
  curl -s -o /dev/null -X POST "http://localhost:${grafanaPort}/api/annotations" \
    -H 'Content-Type: application/json' \
    -d "$(jq -n --argjson t "$1" --argjson te "$2" --arg text "$3" --arg s "$SCRIPT" \
          '{time:$t, timeEnd:$te, tags:["load-test",$s], text:$text}')" || true
}
nowMs() { python3 -c 'import time; print(int(time.time()*1000))'; }

if [ "$RUN" = parallel ]; then
  pids=()
  for c in "${targets[@]}"; do
    kvSet started "$c" "$(nowMs)"
    k6Run "$c" > "$results/${c}.log" 2>&1 &
    pids+=($!); containers+=("k6-${runId}-${c}")
  done
  echo "Running ${targets[*]} at the same time. Live output: tail -f ${results}/<client>.log"
  for i in "${!targets[@]}"; do
    c=${targets[$i]}
    wait "${pids[$i]}"; kvSet codes "$c" $?; kvSet ended "$c" "$(nowMs)"
    echo "  ${c} finished (k6 exit $(kvGet codes "$c"))"
  done
  annotate "$(kvGet started "${targets[0]}")" "$(nowMs)" "${SCRIPT} · ${targets[*]} · parallel"
else
  for c in "${targets[@]}"; do
    echo "${bold}▶ ${c}${reset}"
    containers+=("k6-${runId}-${c}")
    kvSet started "$c" "$(nowMs)"
    k6Run "$c" 2>&1 | tee "$results/${c}.log"
    kvSet codes "$c" "${PIPESTATUS[0]}"; kvSet ended "$c" "$(nowMs)"
    annotate "$(kvGet started "$c")" "$(kvGet ended "$c")" "${SCRIPT} · ${c}"
    echo
  done
fi
trap - INT TERM

# --- 6. Comparison table -----------------------------------------------------------------------
cpuPerReq() {   # client -> client container CPU µs per request over its run window (cAdvisor)
  local c=$1 svc secs reqs cpu end
  svc=$(echo "$c" | sed 's/-/-client-/')
  end=$(kvGet ended "$c")
  secs=$(( (end - $(kvGet started "$c")) / 1000 )); [ "$secs" -lt 15 ] && secs=15
  reqs=$(jq -r '.metrics.http_reqs.count // 0' "$results/${c}.json" 2>/dev/null)
  sleep 1
  cpu=$(curl -s "http://localhost:${promPort}/api/v1/query" \
          --data-urlencode "query=sum(increase(container_cpu_usage_seconds_total{service=\"${svc}\"}[${secs}s]))" \
          --data-urlencode "time=$(( end / 1000 ))" | jq -r '.data.result[0].value[1] // empty' 2>/dev/null)
  if [ -n "$cpu" ] && [ "${reqs:-0}" != 0 ]; then
    awk -v cpu="$cpu" -v r="$reqs" 'BEGIN { printf "%.0f", cpu / r * 1e6 }'
  else
    echo "-"
  fi
}

table() {
  printf '%-12s %10s %8s %7s │ %8s %8s %8s │ %8s %8s │ %8s  %s\n' \
    CLIENT REQUESTS 'REQ/S' FAILED 'E2E p50' 'p95' 'p99' 'GF p50' 'p99' 'CPU/REQ' RESULT
  printf '%-12s %10s %8s %7s │ %8s %8s %8s │ %8s %8s │ %8s\n' '' '' '' '' '(ms)' '(ms)' '(ms)' '(ms)' '(ms)' '(µs)'
  for c in "${targets[@]}"; do
    f="$results/${c}.json"
    cpu=$(cpuPerReq "$c")
    if [ -f "$f" ]; then
      read -r reqs rate failed p50 p95 p99 g50 g99 < <(jq -r '.metrics as $m | [
          $m.http_reqs.count, ($m.http_reqs.rate*10|round/10),
          (($m.http_req_failed.value // 0)*1000|round/10),
          $m.http_req_duration.med, $m.http_req_duration["p(95)"], $m.http_req_duration["p(99)"],
          ($m.gemfire_call_duration.med // 0), ($m.gemfire_call_duration["p(99)"] // 0)
        ] | map(tostring) | join(" ")' "$f")
      kvSet row "$c" "$p50 $p95 $p99 $g50 $g99 $cpu"
      printf '%-12s %10s %8s %6s%% │ %8.3f %8.3f %8.3f │ %8.3f %8.3f │ %8s  ' \
        "$c" "$reqs" "$rate" "$failed" "$p50" "$p95" "$p99" "$g50" "$g99" "$cpu"
    else
      printf '%-12s %10s %8s %7s │ %8s %8s %8s │ %8s %8s │ %8s  ' "$c" - - - - - - - - -
    fi
    code=$(kvGet codes "$c")
    case $code in
      0) echo PASS ;;
      99) echo "FAIL (thresholds crossed)" ;;
      *) echo "FAIL (k6 exit ${code}, see ${c}.log)" ;;
    esac
  done
  if [ -n "$(kvGet row grpc-java)" ] && [ -n "$(kvGet row native-java)" ]; then
    read -r -a g <<< "$(kvGet row grpc-java)"; read -r -a n <<< "$(kvGet row native-java)"
    ratio() { awk -v a="$1" -v b="$2" 'BEGIN { if (b+0 > 0 && a != "-") printf "%.1fx", a/b; else printf "-" }'; }
    printf '%-12s %10s %8s %7s │ %8s %8s %8s │ %8s %8s │ %8s\n' 'gRPC/native' '' '' '' \
      "$(ratio "${g[0]}" "${n[0]}")" "$(ratio "${g[1]}" "${n[1]}")" "$(ratio "${g[2]}" "${n[2]}")" \
      "$(ratio "${g[3]}" "${n[3]}")" "$(ratio "${g[4]}" "${n[4]}")" "$(ratio "${g[5]}" "${n[5]}")"
  fi
}

echo
{
  echo "━━ ${dir} · ${SCRIPT} · ${RUN} · ${runId} ━━"
  [ -n "$K6_ARGS" ] && echo "k6 args: $K6_ARGS"
  echo "E2E = HTTP request as k6 saw it. GF = time inside the GemFire call (client-reported)."
  echo
  table
} | tee "$results/summary.txt"
echo
echo "${dim}Results: ${results}/  ·  Grafana: http://localhost:${grafanaPort} (runs are annotated)  ·  make results${reset}"

for c in "${targets[@]}"; do [ "$(kvGet codes "$c")" = 0 ] || exit 1; done
