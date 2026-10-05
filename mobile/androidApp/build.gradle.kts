plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
}

// The app is versioned with the server: versionName is the repository's
// VERSION file (the number a release tag carries) plus an optional suffix
// (-PnktVersionSuffix=-beta for beta builds, shown in About); versionCode is
// derived from it so every release installs over the previous one —
// 1.11.149 → 1_011_149.
val nktVersion: String = rootProject.file("../VERSION").readText().trim()
val nktVersionSuffix: String = (project.findProperty("nktVersionSuffix") as String?).orEmpty()
val nktVersionCode: Int = nktVersion.split(".").map { it.toInt() }
    .let { (major, minor, patch) -> major * 1_000_000 + minor * 1_000 + patch }

// Release signing comes from the environment only — a keystore and its
// passwords never live in the repository (see .github/workflows).
val releaseKeystore: String? = System.getenv("NKT_ANDROID_KEYSTORE")?.takeIf { it.isNotBlank() }

android {
    // Same application id as the former android/ app, so this build installs
    // over it and keeps sign-in and settings.
    namespace = "com.netknownsthat.app"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.netknownsthat.app"
        minSdk = 26
        targetSdk = 36
        versionCode = nktVersionCode
        versionName = nktVersion + nktVersionSuffix
    }

    signingConfigs {
        if (releaseKeystore != null) {
            create("release") {
                storeFile = file(releaseKeystore)
                storePassword = System.getenv("NKT_ANDROID_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("NKT_ANDROID_KEY_ALIAS")
                keyPassword = System.getenv("NKT_ANDROID_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            if (releaseKeystore != null) signingConfig = signingConfigs.getByName("release")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlin {
        compilerOptions { jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17) }
    }
    buildFeatures {
        compose = true
        buildConfig = true
    }
    packaging {
        resources { excludes += "/META-INF/{AL2.0,LGPL2.1}" }
    }
}

dependencies {
    implementation(project(":shared:app"))
    implementation(libs.koin.android)
    implementation(libs.activity.compose)
    implementation(libs.core.ktx)
    implementation(libs.work.runtime)
    implementation(libs.coroutines.android)
}
