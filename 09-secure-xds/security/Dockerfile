# syntax=docker/dockerfile:1
# The GemFire image every member (and gfsh-init) runs: stock GemFire plus the SecurityManager jar.
ARG GEMFIRE_IMAGE

# Compile against the GemFire jars in the image itself (it's a JDK image); no Maven, no Gradle.
FROM ${GEMFIRE_IMAGE} AS build
COPY src /src
RUN mkdir -p /out/classes \
 && javac --release 17 -d /out/classes -cp "/gemfire/lib/*" /src/demo/security/*.java \
 && jar cf /out/token-security.jar -C /out/classes .

FROM ${GEMFIRE_IMAGE}
COPY --from=build /out/token-security.jar /security/token-security.jar
