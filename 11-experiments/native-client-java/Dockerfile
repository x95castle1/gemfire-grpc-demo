# syntax=docker/dockerfile:1
ARG GEMFIRE_IMAGE

# The GemFire image already contains the client jars; copy them instead of pulling from
# Broadcom's Maven repo.
FROM ${GEMFIRE_IMAGE} AS gemfire

FROM gradle:8.14.3-jdk21 AS build
COPY --from=gemfire /gemfire/lib /gemfire/lib
WORKDIR /src
COPY settings.gradle.kts build.gradle.kts ./
COPY src ./src
RUN --mount=type=cache,target=/home/gradle/.gradle \
    gradle --no-daemon --quiet bootJar -PgemfireLib=/gemfire/lib

FROM eclipse-temurin:21-jre
COPY --from=build /src/build/libs/app.jar /app/app.jar
EXPOSE 8080
ENTRYPOINT ["java", "-XX:+UseZGC", "-Xms256m", "-Xmx512m", "-jar", "/app/app.jar"]
