#!/usr/bin/env bash
# The worker-threads experiment: does the gRPC server's thread pool limit throughput?
#
#   ./worker-threads.sh                       # VALUES=4,8,24,96, plus the default and native as references
#   VALUES=2,48 ./worker-threads.sh           # other settings
#
# gemfire.grpc.worker-threads is a system property, read when a server starts. So for each value this
# restarts server1 and server2 with GRPC_WORKER_THREADS set (start-member.sh passes it on), runs the
# experiments/worker-threads load test (64 VUs, closed loop, 1 minute) with the grpc-go client, and
# reads the result from the run's summary. Then it restarts the servers with the default and runs
# native-java once, as the reference. Prints one table at the end (also saved next to the runs).
#
# Needs the example running (make start EX=11). Takes about 2 minutes per value.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
VALUES=${VALUES:-4,8,24,96}
prom="http://localhost:$(grep -E '^PROMETHEUS_PORT=' "$here/.env" | cut -d= -f2)"

restart() {   # value ("" = the extension's default)
  echo "── restarting server1 and server2 with worker-threads=${1:-default}"
  # --no-deps: recreate only the servers. Recreating the locator too would lose the cluster
  # configuration, and with it the regions the restarted servers get back.
  (cd "$here" && GRPC_WORKER_THREADS="$1" docker compose up -d --force-recreate --no-deps --wait server1 server2 >/dev/null 2>&1)
}

# The server1 CPU (cores) over the last minute, from cAdvisor via Prometheus.
server1Cpu() {
  curl -s "${prom}/api/v1/query" --data-urlencode \
    'query=sum(rate(container_cpu_usage_seconds_total{service="server1"}[1m]))' |
    jq -r '.data.result[0].value[1] // "0"' | awk '{printf "%.2f", $1}'
}

# grpc-worker threads server1 has created (idle ones stay for 30 minutes, so this is the peak).
workers() {
  (cd "$here" && docker compose exec -T server1 bash -c 'jcmd 1 Thread.print 2>/dev/null | grep -c "^\"grpc-worker-"' || echo "?")
}

run() {   # client label
  local client=$1 label=$2 dir row
  (cd "$root" && make select-load-test EX=11 SCRIPT=experiments/worker-threads CLIENT="$client" >/dev/null 2>&1) || true
  dir=$(ls -d "$root"/load-testing/results/*-11-experiments-worker-threads | sort | tail -1)
  # CLIENT  REQUESTS  REQ/S  FAILED │  E2E p50 p95 p99 │  GF p50 p99 │ CPU/REQ RESULT
  row=$(sed 's/\x1b\[[0-9;]*m//g' "$dir/summary.txt" | awk -v c="$client" '$1 == c && $NF == "PASS" {print $3, $4, $10, $11}')
  if [ -z "$row" ]; then
    echo "The ${client} run with worker-threads=${label} didn't pass. From ${dir#"$root"/}/${client}.log:" >&2
    grep -E 'level=error|preload|✗' "$dir/${client}.log" | tail -5 >&2
    exit 1
  fi
  local w="–"   # native doesn't use the gRPC worker pool (the idle threads left from earlier runs don't count)
  [ "$client" = grpc-go ] && w=$(workers)
  printf '%-22s %-12s %10s %7s %9s %9s %8s %8s\n' "$label" "$client" $row "$(server1Cpu)" "$w" | tee -a "$table"
}

table=$(mktemp)
header=$(printf '%-22s %-12s %10s %7s %9s %9s %8s %8s' "worker-threads" CLIENT 'REQ/S' FAILED 'GF p50' 'GF p99' 'server1' 'grpc-')
sub=$(printf '%-22s %-12s %10s %7s %9s %9s %8s %8s' '' '' '' '' '(ms)' '(ms)' '(cores)' 'workers')

IFS=',' read -r -a values <<< "$VALUES"
for v in "${values[@]}"; do
  restart "$v"
  run grpc-go "$v"
done
restart ""
run grpc-go "default (2 × $(cd "$here" && docker compose exec -T server1 nproc | tr -d '\r') CPUs)"
run native-java "(not used by native)"

out="$root/load-testing/results/$(date +%Y%m%d-%H%M%S)-11-worker-threads.txt"
{
  echo "━━ 11-experiments · gemfire.grpc.worker-threads (experiments/worker-threads: 64 VUs, 1 minute each) ━━"
  echo "$header"; echo "$sub"; cat "$table"
} | tee "$out"
rm -f "$table"
echo
echo "Saved: ${out#"$root"/}. The servers are back on the default setting."
