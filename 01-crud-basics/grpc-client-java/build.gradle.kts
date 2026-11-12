plugins {
    application
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
    implementation("io.grpc:grpc-netty-shaded:$grpcVersion")
    implementation("io.grpc:grpc-protobuf:$grpcVersion")
    implementation("io.grpc:grpc-stub:$grpcVersion")
    implementation("com.google.protobuf:protobuf-java:$protobufVersion")
    // Supplies google/api/field_behavior.proto, which gemfire.proto imports but the
    // extension download does not ship (see release notes).
    implementation("com.google.api.grpc:proto-google-common-protos:$commonProtosVersion")
    compileOnly("org.apache.tomcat:annotations-api:6.0.53")

    implementation("io.prometheus:prometheus-metrics-core:$prometheusVersion")
    implementation("io.prometheus:prometheus-metrics-exporter-httpserver:$prometheusVersion")
    implementation("io.prometheus:prometheus-metrics-instrumentation-jvm:$prometheusVersion")
}

// gemfire.proto lives in src/main/proto/gemfire/v1/ (copied from gemfire-extensions/ by the Dockerfile).
protobuf {
    protoc { artifact = "com.google.protobuf:protoc:$protobufVersion" }
    plugins {
        create("grpc") { artifact = "io.grpc:protoc-gen-grpc-java:$grpcVersion" }
    }
    generateProtoTasks {
        all().forEach { it.plugins { create("grpc") } }
    }
}

application {
    mainClass.set("demo.Main")
    // sun.net.httpserver.nodelay: without TCP_NODELAY the JDK HTTP server's separate header and
    // body writes hit Nagle + delayed ACK, adding ~40 ms to back-to-back keep-alive requests.
    applicationDefaultJvmArgs = listOf("-XX:+UseZGC", "-Xms256m", "-Xmx512m", "-Dsun.net.httpserver.nodelay=true")
}
