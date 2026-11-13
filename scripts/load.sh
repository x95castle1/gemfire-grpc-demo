#!/usr/bin/env bash
# Runs a k6 load test (load-testing/scripts/**.js) against an example's client services and
# prints a comparison table, with each gRPC client as a ratio of the native one.
#
# Everything is optional; anything left out is picked with fzf:
#   EX=01                 example (starts it if it isn't running)
#   CLIENT=all            all | native-java | grpc-java | grpc-go
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
found=(); for svc in $(clientServices); do found+=("$(svcToClient "$svc")"); done
# Fixed order (the baseline first), whatever the order in docker-compose.yml; then any others.
available=()
for c in native-java grpc-java grpc-go "${found[@]}"; do
  [[ " ${found[*]} " == *" $c "* ]] && [[ " ${available[*]-} " != *" $c "* ]] && available+=("$c")
done

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
trap 'printf "\e[?25h"; echo; echo "Interrupted, stopping k6..."; cleanup; exit 130' INT TERM

# In a terminal, show k6 at work. Sequential runs get k6's own interactive screen (banner, live
# progress bar, end-of-test summary). Parallel runs get liveScreen below. Without a terminal
# (CI, scripts), k6 runs quietly and only the comparison table is printed.
interactive=0; [ -t 1 ] && [ "${TERM:-dumb}" != dumb ] && interactive=1

k6Run() {   # client mode -> runs k6 against it; output to stdout
  # mode: tty   = k6's interactive screen (docker run -t)
  #       plain = no TTY: k6 prints a progress line per scenario every second (parsed by liveScreen)
  #       quiet = no progress output at all
  local client=$1 mode=$2 svc
  local -a tty=() quiet=()
  [ "$mode" = tty ] && tty=(-t)
  [ "$mode" = quiet ] && quiet=(--quiet)
  svc=$(echo "$client" | sed 's/-/-client-/')
  docker run --rm ${tty[@]+"${tty[@]}"} --name "k6-${runId}-${client}" --network "$network" \
    -v "$ROOT/load-testing:/load:ro" -v "$ROOT/$results:/results" \
    -e BASE_URL="http://${svc}:8080" \
    -e K6_PROMETHEUS_RW_SERVER_URL=http://prometheus:9090/api/v1/write \
    -e K6_FEATURES=native-histograms \
    -e K6_PROMETHEUS_RW_PUSH_INTERVAL=5s \
    -e K6_NO_USAGE_REPORT=true \
    "$K6_IMAGE" run ${quiet[@]+"${quiet[@]}"} -o experimental-prometheus-rw \
      --tag client="$client" --tag run="$runId" --tag example="$dir" --tag script="$SCRIPT" \
      --summary-export "/results/${client}.json" \
      ${extraArgs[@]+"${extraArgs[@]}"} "/load/scripts/${SCRIPT}.js"
}

# --- Live screen for parallel runs -------------------------------------------------------------
# One k6-style progress bar per client (parsed from its log), plus live numbers from this run's
# k6 metrics in Prometheus (k6 pushes every 5s, so they appear a few seconds in).
clientColor() {   # the dashboard colors, in 256-color ANSI
  case $1 in
    native-java) printf '\e[38;5;33m' ;;
    grpc-java) printf '\e[38;5;208m' ;;
    grpc-go) printf '\e[38;5;36m' ;;
    *) printf '\e[1m' ;;
  esac
}

liveFetch() {   # refreshes the live_* values from Prometheus, in one query
  local sel="run=\"${runId}\"" q out m c v
  hq() { echo "label_replace(histogram_quantile($2, sum by (client) (rate($3{${sel}}[30s]))), \"m\", \"$1\", \"\", \"\")"; }
  q="label_replace(sum by (client) (rate(k6_http_reqs_total{${sel}}[15s])), \"m\", \"rps\", \"\", \"\")"
  q="$q or $(hq e2e 0.5 k6_http_req_duration_seconds) or $(hq gf50 0.5 k6_gemfire_call_duration_seconds)"
  q="$q or $(hq gf99 0.99 k6_gemfire_call_duration_seconds)"
  q="$q or label_replace(sum by (client) (k6_http_reqs_total{${sel}, expected_response=\"false\"}), \"m\", \"failed\", \"\", \"\")"
  out=$(curl -s --max-time 2 "http://localhost:${promPort}/api/v1/query" --data-urlencode "query=$q" |
        jq -r '.data.result[]? | "\(.metric.m) \(.metric.client) \(.value[1])"' 2>/dev/null)
  while read -r m c v; do [ -n "$c" ] && kvSet "live_$m" "$c" "$v"; done <<< "$out"
}

ms() { awk -v s="$1" 'BEGIN { if (s == "" || s == "NaN") printf "-"; else printf "%.2f ms", s * 1000 }'; }

liveLines() {   # prints the screen's lines for the current state
  local c log line pct rest name bar width=30 fill status color rps failed
  for c in "${targets[@]}"; do
    log="$results/${c}.log"; color=$(clientColor "$c")
    line=$(tail -n 30 "$log" 2>/dev/null | grep -E '\[ *[0-9]+% \]' | tail -1)
    if [ -n "$line" ]; then
      name=${line%%[*}; name=${name%% *}
      pct=$(echo "$line" | sed -E 's/.*\[ *([0-9]+)% \].*/\1/')
      rest=$(echo "$line" | sed -E 's/.*% \] *//; s/  +/  /g')
      fill=$(( pct * width / 100 ))
      bar=$(printf '%*s' "$fill" '' | tr ' ' '=')
      [ "$fill" -lt "$width" ] && bar="${bar}>$(printf '%*s' $(( width - fill - 1 )) '' | tr ' ' '-')"
      line=$(printf '%-9s [%s] %3s%%  %s' "$name" "$bar" "$pct" "$rest")
    elif grep -q 'preloaded' "$log" 2>/dev/null; then
      line="starting scenario..."
    else
      line="${dim}setup: preloading keys...${reset}"
    fi
    status=""
    case $(kvGet codes "$c") in
      "") ;;
      0) status="  ${green}✓ done${reset}" ;;
      99) status="  ${red}✘ thresholds crossed${reset}" ;;
      *) status="  ${red}✘ k6 exit $(kvGet codes "$c"), see ${c}.log${reset}" ;;
    esac
    printf '  %s%s%-12s%s %s%s\n' "$color" "$bold" "$c" "$reset" "$line" "$status"
    rps=$(kvGet live_rps "$c"); failed=$(kvGet live_failed "$c")
    printf '  %-12s %s%s req/s · E2E p50 %s · GF p50 %s · GF p99 %s · %s failed%s\n' '' "$dim" \
      "$(awk -v r="$rps" 'BEGIN { if (r == "") printf "-"; else printf "%.1f", r }')" \
      "$(ms "$(kvGet live_e2e "$c")")" "$(ms "$(kvGet live_gf50 "$c")")" \
      "$(ms "$(kvGet live_gf99 "$c")")" "${failed:-0}" "$reset"
  done
  echo
  printf '  %selapsed %s · Grafana http://localhost:%s · full k6 output: %s/<client>.log · Ctrl-C stops all%s\n' \
    "$dim" "$(elapsed)" "$grafanaPort" "$results" "$reset"
}

elapsed() { local s=$(( ($(nowMs) - runStart) / 1000 )); printf '%dm%02ds' $((s / 60)) $((s % 60)); }

liveScreen() {   # pid... (one per target) -> redraws until every k6 process exits
  local -a pids=("$@")
  local i c drawn=0 tick=0 running first="$results/${targets[0]}.log"
  # k6's banner and scenario summary, once, from the first client's log.
  for _ in $(seq 1 40); do grep -qE '^ +\* ' "$first" 2>/dev/null && break; sleep 0.25; done
  sed -n '/^ *\/\\ /,/^ *\* /p' "$first" 2>/dev/null
  printf '%15s %s\n\n' "clients:" "${targets[*]} (all at once, one k6 process each)"
  printf '\e[?25l'   # hide the cursor while redrawing
  while :; do
    running=0
    for i in "${!targets[@]}"; do
      c=${targets[$i]}
      [ -n "$(kvGet codes "$c")" ] && continue
      if kill -0 "${pids[$i]}" 2>/dev/null; then
        running=1
      else
        wait "${pids[$i]}"; kvSet codes "$c" $?; kvSet ended "$c" "$(nowMs)"
      fi
    done
    [ $((tick % 2)) -eq 0 ] || [ $running -eq 0 ] && liveFetch
    [ $drawn -gt 0 ] && printf '\e[%dA' "$drawn"
    screen=$(liveLines)
    printf '%s\n' "$screen" | sed $'s/^/\e[2K/'
    drawn=$(printf '%s\n' "$screen" | wc -l | tr -d ' ')
    [ $running -eq 0 ] && break
    tick=$((tick + 1)); sleep 1
  done
  printf '\e[?25h'
}

annotate() {   # start_ms end_ms text
  curl -s -o /dev/null -X POST "http://localhost:${grafanaPort}/api/annotations" \
    -H 'Content-Type: application/json' \
    -d "$(jq -n --argjson t "$1" --argjson te "$2" --arg text "$3" --arg s "$SCRIPT" \
          '{time:$t, timeEnd:$te, tags:["load-test",$s], text:$text}')" || true
}
nowMs() { python3 -c 'import time; print(int(time.time()*1000))'; }

runStart=$(nowMs)
if [ "$RUN" = parallel ]; then
  pids=()
  mode=quiet; [ $interactive -eq 1 ] && mode=plain
  for c in "${targets[@]}"; do
    kvSet started "$c" "$(nowMs)"
    k6Run "$c" "$mode" > "$results/${c}.log" 2>&1 &
    pids+=($!); containers+=("k6-${runId}-${c}")
  done
  if [ $interactive -eq 1 ]; then
    liveScreen "${pids[@]}"
  else
    echo "Running ${targets[*]} at the same time. Live output: tail -f ${results}/<client>.log"
    for i in "${!targets[@]}"; do
      c=${targets[$i]}
      wait "${pids[$i]}"; kvSet codes "$c" $?; kvSet ended "$c" "$(nowMs)"
      echo "  ${c} finished (k6 exit $(kvGet codes "$c"))"
    done
  fi
  annotate "$(kvGet started "${targets[0]}")" "$(nowMs)" "${SCRIPT} · ${targets[*]} · parallel"
else
  mode=quiet; [ $interactive -eq 1 ] && mode=tty
  for i in "${!targets[@]}"; do
    c=${targets[$i]}
    echo "$(clientColor "$c")${bold}▶ ${c}${reset} ($((i + 1)) of ${#targets[@]})"
    containers+=("k6-${runId}-${c}")
    kvSet started "$c" "$(nowMs)"
    k6Run "$c" "$mode" 2>&1 | tee "$results/${c}.log"
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
  printf '%-21s %10s %8s %7s │ %11s %11s %11s │ %11s %11s │ %9s  %s\n' \
    CLIENT REQUESTS 'REQ/S' FAILED 'E2E p50' 'p95' 'p99' 'GF p50' 'p99' 'CPU/REQ' RESULT
  printf '%-21s %10s %8s %7s │ %11s %11s %11s │ %11s %11s │ %9s\n' '' '' '' '' '(ms)' '(ms)' '(ms)' '(ms)' '(ms)' '(µs)'
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
      printf '%-21s %10s %8s %6s%% │ %11.3f %11.3f %11.3f │ %11.3f %11.3f │ %9s  ' \
        "$c" "$reqs" "$rate" "$failed" "$p50" "$p95" "$p99" "$g50" "$g99" "$cpu"
    else
      printf '%-21s %10s %8s %7s │ %11s %11s %11s │ %11s %11s │ %9s  ' "$c" - - - - - - - - -
    fi
    code=$(kvGet codes "$c")
    case $code in
      0) echo PASS ;;
      99) echo "FAIL (thresholds crossed)" ;;
      *) echo "FAIL (k6 exit ${code}, see ${c}.log)" ;;
    esac
  done
  # Ratio rows: each gRPC client against native-java, then the two gRPC stacks against each other.
  ratioRow grpc-java native-java
  ratioRow grpc-go native-java
  ratioRow grpc-go grpc-java
}

# How A's value compares with B's, in words: "22% faster" / "60% slower" for latency, "43% less" /
# "62% more" for CPU. Green = better, red = worse (colors only in a terminal).
ratio() {   # a b width kind(time|cpu)
  awk -v a="$1" -v b="$2" -v w="$3" -v kind="$4" -v color="$interactive" 'BEGIN {
    if (!(b + 0 > 0) || a == "-") { printf "%*s", w, "-"; exit }
    p = (a / b - 1) * 100; n = sprintf("%.0f", p < 0 ? -p : p)
    if (n == "0") { printf "%*s", w, "same"; exit }
    word = kind == "cpu" ? (p < 0 ? "less" : "more") : (p < 0 ? "faster" : "slower")
    cell = sprintf("%*s", w, n "% " word)
    if (color == 1) cell = (p < 0 ? "\033[32m" : "\033[31m") cell "\033[0m"
    printf "%s", cell
  }'
}
ratioRow() {   # a b -> "a vs b" row: how a's latency and CPU compare with b's, if both clients ran
  [ -n "$(kvGet row "$1")" ] && [ -n "$(kvGet row "$2")" ] || return 0
  local a b
  read -r -a a <<< "$(kvGet row "$1")"; read -r -a b <<< "$(kvGet row "$2")"
  printf '%-21s %10s %8s %7s │ %s %s %s │ %s %s │ %s\n' "${1} vs ${2/native-java/native}" '' '' '' \
    "$(ratio "${a[0]}" "${b[0]}" 11 time)" "$(ratio "${a[1]}" "${b[1]}" 11 time)" "$(ratio "${a[2]}" "${b[2]}" 11 time)" \
    "$(ratio "${a[3]}" "${b[3]}" 11 time)" "$(ratio "${a[4]}" "${b[4]}" 11 time)" "$(ratio "${a[5]}" "${b[5]}" 9 cpu)"
}

echo
{
  echo "━━ ${dir} · ${SCRIPT} · ${RUN} · ${runId} ━━"
  [ -n "$K6_ARGS" ] && echo "k6 args: $K6_ARGS"
  echo "E2E = HTTP request as k6 saw it. GF = time inside the GemFire call (client-reported)."
  [ ${#targets[@]} -gt 1 ] && echo "\"A vs B\" rows: how A did compared with B (e.g. \"grpc-go vs grpc-java: 22% faster\" = grpc-go was faster)."
  echo
  table
} | tee "$results/summary.raw"
sed $'s/\e\\[[0-9;]*m//g' "$results/summary.raw" > "$results/summary.txt" && rm -f "$results/summary.raw"   # no colors in the file
echo
echo "${dim}Results: ${results}/  ·  Grafana: http://localhost:${grafanaPort} (runs are annotated)  ·  make results${reset}"

for c in "${targets[@]}"; do [ "$(kvGet codes "$c")" = 0 ] || exit 1; done
