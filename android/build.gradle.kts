// Root build file — plugin versions declared here, applied per-module with
// `apply false` at the root and applied for real in app/build.gradle.kts.
// Kept minimal: this project has exactly one module (:app) for the phases
// currently implemented (see /home/alex/.claude/plans — the Android client
// plan, phases 1-2).
plugins {
    id("com.android.application") version "8.6.0" apply false
    id("org.jetbrains.kotlin.android") version "2.0.20" apply false
    id("org.jetbrains.kotlin.plugin.serialization") version "2.0.20" apply false
}
