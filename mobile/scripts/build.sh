#!/bin/sh
# Builds the Kotlin Multiplatform mobile app from the command line.
#
# Prerequisite (once per machine): mobile/scripts/setup-toolchain.sh — JDK 17
# and the Android SDK in ~/.local, no root. iOS targets need macOS with Xcode
# and are skipped elsewhere (CI builds them on a macOS runner).
#
# Usage:
#   mobile/scripts/build.sh                      # debug APK
#   mobile/scripts/build.sh allTests             # every test
#   mobile/scripts/build.sh :androidApp:assembleRelease
set -eu
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MOBILE_DIR="$(dirname "$SCRIPT_DIR")"
ENV="$SCRIPT_DIR/env.sh"
if [ -f "$ENV" ]; then
  # shellcheck source=/dev/null
  . "$ENV"
fi
cd "$MOBILE_DIR"
[ -x ./gradlew ] || chmod +x ./gradlew
./gradlew "${@:-:androidApp:assembleDebug}"
APK="$MOBILE_DIR/androidApp/build/outputs/apk/debug/androidApp-debug.apk"
[ -f "$APK" ] && echo "APK: $APK" || true
