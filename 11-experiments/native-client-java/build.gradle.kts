import org.springframework.boot.gradle.plugin.SpringBootPlugin

plugins {
    java
    id("org.springframework.boot") version "4.1.2"
}

val prometheusVersion = "1.8.0"

// GemFire client jars are copied out of the GemFire image by the Dockerfile (no Maven
// credentials needed). For a local build: ./gradlew build -PgemfireLib=/path/to/vmware-gemfire-10.3.2/lib
val gemfireLib: String = providers.gradleProperty("gemfireLib").getOrElse("/gemfire/lib")

repositories {
    mavenCentral()
}

java {
    sourceCompatibility = JavaVersion.VERSION_21
    targetCompatibility = JavaVersion.VERSION_21
}

dependencies {
    implementation(platform(SpringBootPlugin.BOM_COORDINATES))
    // Plain Spring Boot (Spring MVC on Tomcat), no Spring Data for GemFire.
    implementation("org.springframework.boot:spring-boot-starter-webmvc")

    // GemFire's lib folder also serves the server and gfsh, so it carries its own Spring, Jetty,
    // logging, Jackson and Micrometer jars. Spring Boot supplies those, so leave GemFire's copies
    // out (GemFire then logs through log4j-api → Logback).
    implementation(fileTree(gemfireLib) {
        include("*.jar")
        exclude(
            "spring-*", "jetty-*", "jakarta.servlet-api-*", "gemfire-http-service-*", "gemfire-gfsh-*", "gfsh-dependencies*",
            "log4j-*", "gemfire-log4j-*", "slf4j-api-*", "commons-logging-*",
            "jackson-core-*", "jackson-databind-*", "jackson-annotations-*", "jspecify-*", "jakarta.annotation-api-*",
            "micrometer-*", "HdrHistogram-*",
        )
    })
    // GemFire 10.3.2 was built against these; Spring Boot's managed versions are a bit older.
    implementation(platform("tools.jackson:jackson-bom:3.2.2"))
    implementation(platform("org.apache.logging.log4j:log4j-bom:2.26.1"))
    implementation("io.micrometer:micrometer-core")
    implementation("org.apache.logging.log4j:log4j-api")

    implementation("io.prometheus:prometheus-metrics-core:$prometheusVersion")
    implementation("io.prometheus:prometheus-metrics-exposition-formats:$prometheusVersion")
    implementation("io.prometheus:prometheus-metrics-instrumentation-jvm:$prometheusVersion")
}

tasks.bootJar {
    archiveFileName = "app.jar"
}
