# syntax=docker/dockerfile:1
# Build context: this folder. The `vendor` additional context (docker-compose.yml) supplies gemfire.proto.
FROM gradle:8.14.3-jdk21 AS build
WORKDIR /src
COPY settings.gradle.kts build.gradle.kts ./
COPY src ./src
COPY --from=vendor gemfire.proto src/main/proto/gemfire/v1/gemfire.proto
RUN --mount=type=cache,target=/home/gradle/.gradle \
    gradle --no-daemon --quiet installDist

FROM eclipse-temurin:21-jre
COPY --from=build /src/build/install/grpc-client-java /app
EXPOSE 8080
ENTRYPOINT ["/app/bin/grpc-client-java"]
