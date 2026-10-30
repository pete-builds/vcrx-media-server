// The Media3 FFmpeg audio decoder extension. Google publishes no binary of it, so
// the ffmpeg stage of tv/Dockerfile builds it from the androidx/media sources and
// vanilla FFmpeg, then stages the result in ext/ (not committed). This module only
// compiles that staged Java and packages the native libraries.
plugins {
    alias(libs.plugins.android.library)
}

android {
    namespace = "androidx.media3.decoder.ffmpeg"
    compileSdk = 37

    defaultConfig {
        minSdk = 26
        consumerProguardFiles("ext/proguard-rules.txt")
    }

    sourceSets {
        getByName("main") {
            java.directories.add("ext/java")
            jniLibs.directories.add("ext/jniLibs")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    api(libs.media3.decoder)
    implementation(libs.media3.common)
    implementation(libs.media3.exoplayer)
    implementation(libs.androidx.annotation)
    compileOnly(libs.checker.qual)
}
