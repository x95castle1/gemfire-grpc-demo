# syntax=docker/dockerfile:1
# The gfsh-init image: GemFire (for gfsh) plus the descriptor JAR it deploys.
# Build context: this folder. The `schema` additional context (docker-compose.yml) is ../proto.
ARG GEMFIRE_IMAGE

# A descriptor JAR is just a zip with a FileDescriptorSet at META-INF/gemfire/protobuf/<name>.desc.
# No classes, no manifest entries. --include_imports puts every imported .proto in the set too.
FROM alpine:3.24.3 AS descriptors
RUN apk add --no-cache protobuf protobuf-dev zip
COPY --from=schema person/v1/person.proto /src/person/v1/person.proto
RUN mkdir -p /stage/META-INF/gemfire/protobuf \
 && protoc -I /src --include_imports --descriptor_set_out=/stage/META-INF/gemfire/protobuf/person.desc person/v1/person.proto \
 && cd /stage && zip -q -r /person-types.jar META-INF

FROM ${GEMFIRE_IMAGE}
COPY --from=descriptors /person-types.jar /descriptors/person-types.jar
