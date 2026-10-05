pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "nkt-mobile"

// Clean Architecture layers — dependencies point inward only:
// ui → domain ← data; core and terminal are leaf utilities.
include(":shared:core")
include(":shared:domain")
include(":shared:data")
include(":shared:terminal")
include(":shared:ui")
// app — the composition root: Koin graph wiring data implementations into
// domain interfaces, the root composable and the iOS framework.
include(":shared:app")
include(":androidApp")
