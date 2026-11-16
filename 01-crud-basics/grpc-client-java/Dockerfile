# syntax=docker/dockerfile:1
# Build context: this folder. The `gemfire-extensions` additional context (docker-compose.yml) supplies gemfire.proto.
FROM gradle:8.14.3-jdk21 AS build
WORKDIR /src
COPY settings.gradle.kts build.gradle.kts ./
COPY src ./src
COPY --from=gemfire-extensions gemfire.proto src/main/proto/gemfire/v1/gemfire.proto
RUN --mount=type=cache,target=/home/gradle/.gradle \
    gradle --no-daemon --quiet bootJar

FROM eclipse-temurin:21-jre
COPY --from=build /src/build/libs/app.jar /app/app.jar
EXPOSE 8080
ENTRYPOINT ["java", "-XX:+UseZGC", "-Xms256m", "-Xmx512m", "-jar", "/app/app.jar"]
