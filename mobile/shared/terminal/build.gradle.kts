plugins {
    alias(libs.plugins.kotlin.multiplatform)
    alias(libs.plugins.android.library)

}

kotlin {
    androidTarget {
        compilerOptions { jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17) }
    }
    iosArm64()
    iosSimulatorArm64()

    sourceSets {
        commonMain.dependencies {

        }
        androidMain.dependencies {

        }
        iosMain.dependencies {

        }
        // JVM tests (JUnit, resources, names with spaces) — run by
        // testDebugUnitTest on the build machine.
        androidUnitTest.dependencies {
            implementation(libs.junit)
        }
        commonTest.dependencies {
            implementation(kotlin("test"))

        }
    }
}

android {
    namespace = "com.netknownsthat.terminal"
    compileSdk = 36
    defaultConfig { minSdk = 26 }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}
