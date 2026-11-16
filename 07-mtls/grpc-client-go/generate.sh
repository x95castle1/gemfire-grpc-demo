#!/bin/sh
# Generates the Go gRPC stubs for gemfire.proto into ./gen/gemfirev1 (git-ignored).
# The Dockerfile runs this; to build outside Docker you need protoc on your PATH:
#
#   ./generate.sh ../../gemfire-extensions/gemfire.proto && go run .
#
# gemfire.proto imports google/api/field_behavior.proto, which doesn't ship with the GemFire
# download. It's taken from the same Maven Central jar the Java client builds with.
set -eu
cd "$(dirname "$0")"

PROTO=${1:-../../gemfire-extensions/gemfire.proto}
COMMON_PROTOS=2.78.0   # com.google.api.grpc:proto-google-common-protos, as in grpc-client-java
MODULE=grpc-client-go
PKG=$MODULE/gen/gemfirev1

[ -f "$PROTO" ] || { echo "gemfire.proto not found at $PROTO (see ../../gemfire-extensions/README.md)" >&2; exit 1; }

# The proto declares go_package "com.vmware.gemfire/grpc/v1"; the M option below maps it into
# this module instead, so no go.mod replace is needed.
mkdir -p build/proto/gemfire/v1 build/proto/google/api
cp "$PROTO" build/proto/gemfire/v1/gemfire.proto
if [ ! -f build/proto/google/api/field_behavior.proto ]; then
  jar=build/proto-google-common-protos-$COMMON_PROTOS.jar
  curl -fsSL -o "$jar" "https://repo1.maven.org/maven2/com/google/api/grpc/proto-google-common-protos/$COMMON_PROTOS/proto-google-common-protos-$COMMON_PROTOS.jar"
  unzip -q -o -d build/proto "$jar" google/api/field_behavior.proto
fi

# protoc-gen-go and protoc-gen-go-grpc are `tool` dependencies in go.mod, so their versions
# match the protobuf and grpc runtimes this module builds against.
GOBIN=$(pwd)/build/bin go install tool
rm -rf gen && mkdir -p gen
PATH="$(pwd)/build/bin:$PATH" protoc -I build/proto \
  --go_out=. --go_opt=module=$MODULE --go_opt=Mgemfire/v1/gemfire.proto=$PKG \
  --go-grpc_out=. --go-grpc_opt=module=$MODULE --go-grpc_opt=Mgemfire/v1/gemfire.proto=$PKG \
  gemfire/v1/gemfire.proto
echo "generated $(ls gen/gemfirev1 | tr '\n' ' ')"
