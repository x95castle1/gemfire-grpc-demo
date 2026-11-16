import org.springframework.boot.gradle.plugin.SpringBootPlugin

plugins {
    java
    id("org.springframework.boot") version "4.1.2"
    id("com.google.protobuf") version "0.9.5"
}

// Match the versions bundled in the gemfire-for-grpc 1.0.0 extension (.gfm).
val grpcVersion = "1.84.3"
val protobufVersion = "4.36.2"
val commonProtosVersion = "2.78.0"
val prometheusVersion = "1.8.0"

repositories {
    mavenCentral()
}

java {
    sourceCompatibility = JavaVersion.VERSION_21
    targetCompatibility = JavaVersion.VERSION_21
}

dependencies {
    implementation(platform(SpringBootPlugin.BOM_COORDINATES))
    // Plain Spring Boot (Spring MVC on Tomcat).
    implementation("org.springframework.boot:spring-boot-starter-webmvc")

    implementation("io.grpc:grpc-netty-shaded:$grpcVersion")
    implementation("io.grpc:grpc-protobuf:$grpcVersion")
    implementation("io.grpc:grpc-stub:$grpcVersion")
    implementation("com.google.protobuf:protobuf-java:$protobufVersion")
    // Supplies google/api/field_behavior.proto, which gemfire.proto imports but the
    // extension download does not ship (see release notes).
    implementation("com.google.api.grpc:proto-google-common-protos:$commonProtosVersion")
    compileOnly("org.apache.tomcat:annotations-api:6.0.53")

    implementation("io.prometheus:prometheus-metrics-core:$prometheusVersion")
    implementation("io.prometheus:prometheus-metrics-exposition-formats:$prometheusVersion")
    implementation("io.prometheus:prometheus-metrics-instrumentation-jvm:$prometheusVersion")
}

// gemfire.proto lives in src/main/proto/gemfire/v1/ (copied from gemfire-extensions/ by the Dockerfile).
// The Spring Boot plugin already adds the grpc code generator to every proto task; this pins the
// protoc and generator versions to the ones above.
protobuf {
    protoc { artifact = "com.google.protobuf:protoc:$protobufVersion" }
    plugins {
        maybeCreate("grpc").artifact = "io.grpc:protoc-gen-grpc-java:$grpcVersion"
    }
    generateProtoTasks {
        all().forEach { it.plugins { maybeCreate("grpc") } }
    }
}

tasks.bootJar {
    archiveFileName = "app.jar"
}
