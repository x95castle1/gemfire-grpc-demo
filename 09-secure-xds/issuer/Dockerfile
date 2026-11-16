# syntax=docker/dockerfile:1
FROM golang:1.27.2-alpine3.24 AS build
WORKDIR /src
COPY go.mod main.go ./
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/issuer .

FROM alpine:3.24.3
RUN apk add --no-cache curl
COPY --from=build /out/issuer /app/issuer
EXPOSE 8080
ENTRYPOINT ["/app/issuer"]
