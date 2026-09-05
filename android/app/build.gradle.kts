plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.serialization")
}

android {
    namespace = "com.netknownsthat.app"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.netknownsthat.app"
        // API 26 (Oreo) — matches the plan's baseline for a modern client;
        // nothing here needs anything older, and DataStore/security-crypto
        // both assume 21+ anyway.
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }

    buildFeatures {
        compose = true
    }
    composeOptions {
        // Matches the Kotlin plugin version in the root build.gradle.kts —
        // bump both together if Android Studio suggests a newer Kotlin.
        kotlinCompilerExtensionVersion = "1.5.14"
    }

    packaging {
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
    }
}

dependencies {
    implementation(platform("androidx.compose:compose-bom:2024.09.00"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-graphics")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    // Icons.Default.* (Refresh/Info/etc.) live in material-icons-core, not
    // pulled in transitively by material3 — needed explicitly.
    implementation("androidx.compose.material:material-icons-core")
    debugImplementation("androidx.compose.ui:ui-tooling")

    implementation("androidx.activity:activity-compose:1.9.2")
    implementation("androidx.navigation:navigation-compose:2.8.1")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.6")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.6")
    implementation("androidx.core:core-ktx:1.13.1")

    // Persisted app state — the hub's own base URL, and the session cookie
    // jar (see net/CookieStore.kt) so a relaunch doesn't force a fresh login
    // every time, matching a browser tab's own behavior.
    implementation("androidx.datastore:datastore-preferences:1.1.1")

    // OkHttp: REST + WebSocket (terminal/install-log streams, phase 6) share
    // one client and its CookieJar — see net/HubClient.kt.
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    // JSON models mirror internal/model/model.go's structs 1:1 — see
    // net/model/*.kt. kotlinx.serialization over Moshi/Gson: no reflection,
    // and JsonObject is a clean escape hatch for the handful of endpoints
    // that return a hand-written map shape (e.g. /overview) instead of a
    // fixed struct.
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.3")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.9.0")

    testImplementation("junit:junit:4.13.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.6.1")
}
