plugins {
    alias(libs.plugins.kotlin.multiplatform)
    alias(libs.plugins.android.library)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.compose.mp)
    alias(libs.plugins.kotlin.serialization)
}

kotlin {
    androidTarget {
        compilerOptions { jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17) }
    }
    iosArm64()
    iosSimulatorArm64()

    sourceSets {
        commonMain.dependencies {
            // Presentation sees the domain only — never data (Clean
            // Architecture); implementations arrive through Koin.
            api(project(":shared:domain"))
            api(project(":shared:terminal"))
            api(compose.runtime)
            api(compose.foundation)
            api(compose.ui)
            api(libs.cmp.material3)
            api(libs.cmp.icons)
            implementation(libs.cmp.adaptive)
            implementation(libs.cmp.navsuite)
            api(libs.lifecycle.viewmodel.compose)
            implementation(libs.lifecycle.runtime.compose)
            api(libs.navigation.compose)
            api(libs.koin.compose)
            api(libs.koin.compose.viewmodel)
            implementation(libs.serialization.json)
        }
        // JVM tests (JUnit, resources, names with spaces) — run by
        // testDebugUnitTest on the build machine.
        androidUnitTest.dependencies {
            implementation(libs.junit)
        }
        commonTest.dependencies {
            implementation(kotlin("test"))
            implementation(libs.coroutines.test)
        }
    }
}

android {
    namespace = "com.netknownsthat.ui"
    compileSdk = 36
    defaultConfig { minSdk = 26 }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}
