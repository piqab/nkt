plugins {
    alias(libs.plugins.kotlin.multiplatform)
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.serialization)
}

kotlin {
    androidTarget {
        compilerOptions { jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17) }
    }
    iosArm64()
    iosSimulatorArm64()
    compilerOptions { freeCompilerArgs.add("-Xexpect-actual-classes") }

    sourceSets {
        commonMain.dependencies {
            api(project(":shared:domain"))
            implementation(project(":shared:terminal"))
            implementation(libs.serialization.json)
            implementation(libs.ktor.core)
            implementation(libs.ktor.websockets)
            implementation(libs.datastore.preferences.core)
            implementation(libs.okio)
            implementation(libs.koin.core)
            implementation(libs.kermit)
        }
        androidMain.dependencies {
            implementation(libs.ktor.okhttp)
            implementation(libs.koin.android)
            implementation(libs.coroutines.android)
        }
        iosMain.dependencies {
            implementation(libs.ktor.darwin)
        }
        // JVM tests (JUnit, resources, names with spaces) — run by
        // testDebugUnitTest on the build machine.
        androidUnitTest.dependencies {
            implementation(libs.junit)
        }
        commonTest.dependencies {
            implementation(kotlin("test"))
            implementation(libs.ktor.mock)
            implementation(libs.coroutines.test)
        }
    }
}

android {
    namespace = "com.netknownsthat.data"
    compileSdk = 36
    defaultConfig { minSdk = 26 }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}
