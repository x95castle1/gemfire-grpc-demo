#!/usr/bin/env bash
# Starts a GemFire locator or server in the foreground with the gRPC extension enabled.
#
#   start-member.sh locator|server [extra gfsh args...]
#
# The member name and every advertised address come from the container hostname, so the
# same script serves locator, server1 and server2. Hands off to the image's entry-point,
# which turns `gfsh start ...` into a foreground java process that logs to stdout.
set -euo pipefail

# (Docker creates an empty *directory* when a bind-mount source is missing, so check for a file.)
gfm_found=false
for f in /gemfire-extensions/*.gfm; do [ -f "$f" ] && gfm_found=true; done
if [ "$gfm_found" != true ]; then
  echo "ERROR: no gemfire-for-grpc .gfm found in /gemfire-extensions." >&2
  echo "       Put the extension in GEMFIRE_EXTENSIONS_DIR (see ../gemfire-extensions/README.md) and check GRPC_EXTENSION_FILE in .env." >&2
  exit 1
fi

ROLE="${1:?usage: start-member.sh locator|server [gfsh args...]}"
shift
NAME="$(hostname)"

COMMON=(
  --name="${NAME}"
  --hostname-for-clients="${NAME}"
  # HTTP service hosts the Prometheus endpoint at :7070/metrics
  --http-service-port=7070
  --J=-Dgemfire.prometheus.metrics.emission=All
  --J=-Dgemfire.prometheus.metrics.interval=15s
  # gRPC endpoint: bind on all interfaces, advertise the compose service name
  --J=-Dgemfire.grpc.bind-address=0.0.0.0:50051
  --J=-Dgemfire.grpc.advertised-address="${NAME}:50051"
  # Mutual TLS for gRPC: keystore, truststore, require-authentication (gemfire/gemfire.properties)
  --properties-file=/scripts/gemfire.properties
  # Security: the SecurityManager jar and its settings (gemfire/gfsecurity.properties)
  --classpath=/security/token-security.jar
  --security-properties-file=/scripts/gfsecurity.properties
)

case "${ROLE}" in
  locator)
    exec sh /application/entry-point.sh gfsh start locator \
      "${COMMON[@]}" \
      --port=10334 \
      --J=-Dgemfire.jmx-manager-hostname-for-clients="${NAME}" \
      --max-heap="${MAX_HEAP:-512m}" \
      "$@"
    ;;
  server)
    exec sh /application/entry-point.sh gfsh start server \
      "${COMMON[@]}" \
      --locators="${LOCATORS:-locator[10334]}" \
      --server-port=40404 \
      --start-rest-api \
      --initial-heap="${MAX_HEAP:-1g}" \
      --max-heap="${MAX_HEAP:-1g}" \
      "$@"
    ;;
  *)
    echo "unknown role: ${ROLE}" >&2
    exit 1
    ;;
esac
