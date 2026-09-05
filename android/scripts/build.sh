#!/usr/bin/env bash
# Builds the Android app from the command line — no Android Studio needed.
#
# Prerequisite (once per machine):
#   android/scripts/setup-linux-toolchain.sh
#
# Usage:
#   android/scripts/build.sh              # debug APK
#   android/scripts/build.sh assembleRelease
#   android/scripts/build.sh installDebug # build + push to a connected device
#
# Any arguments are passed straight through to Gradle, so anything you would
# type after `./gradlew` works here too (--info, --stacktrace, a task name).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ANDROID_DIR="$(dirname "$SCRIPT_DIR")"

if [ ! -f "$SCRIPT_DIR/env.sh" ]; then
  echo "error: $SCRIPT_DIR/env.sh is missing — run setup-linux-toolchain.sh first" >&2
  exit 1
fi

# shellcheck source=/dev/null
source "$SCRIPT_DIR/env.sh"

cd "$ANDROID_DIR"
./gradlew "${@:-assembleDebug}"

APK="$ANDROID_DIR/app/build/outputs/apk/debug/app-debug.apk"
if [ -f "$APK" ]; then
  echo
  echo "APK: $APK"
  echo "Install on a connected device/emulator with:"
  echo "  \$ANDROID_HOME/platform-tools/adb install -r \"$APK\""
fi
