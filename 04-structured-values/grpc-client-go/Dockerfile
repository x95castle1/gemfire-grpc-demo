# syntax=docker/dockerfile:1
# Build context: this folder. Additional contexts (docker-compose.yml): `gemfire-extensions` supplies
# gemfire.proto, `schema` is ../proto (person.v1 and note.v1).
FROM golang:1.27.2-alpine3.24 AS build
# protoc, plus google/protobuf/*.proto (well-known types) from protobuf-dev
RUN apk add --no-cache protobuf protobuf-dev curl unzip
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY generate.sh ./
COPY --from=gemfire-extensions gemfire.proto /tmp/gemfire.proto
COPY --from=schema . /tmp/schema
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    ./generate.sh /tmp/gemfire.proto /tmp/schema
COPY *.go ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/grpc-client-go .

# curl is for the compose healthcheck and `make verify`.
FROM alpine:3.24.3
RUN apk add --no-cache curl
COPY --from=build /out/grpc-client-go /app/grpc-client-go
EXPOSE 8080
ENTRYPOINT ["/app/grpc-client-go"]
