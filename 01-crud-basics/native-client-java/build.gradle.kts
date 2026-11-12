plugins {
    application
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
    implementation(fileTree(gemfireLib) { include("*.jar") })

    implementation("io.prometheus:prometheus-metrics-core:$prometheusVersion")
    implementation("io.prometheus:prometheus-metrics-exporter-httpserver:$prometheusVersion")
    implementation("io.prometheus:prometheus-metrics-instrumentation-jvm:$prometheusVersion")
}

application {
    mainClass.set("demo.Main")
    applicationDefaultJvmArgs = listOf("-XX:+UseZGC", "-Xms256m", "-Xmx512m")
}
