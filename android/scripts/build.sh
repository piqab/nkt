#!/bin/sh
# Builds the Android app from the command line — no Android Studio needed.
#
# Prerequisite (once per machine):
#   android/scripts/setup-toolchain.sh
#
# Usage:
#   android/scripts/build.sh              # debug APK
#   android/scripts/build.sh assembleRelease
#   android/scripts/build.sh installDebug # build + push to a connected device
#
# Any arguments are passed straight through to Gradle, so anything you would
# type after `./gradlew` works here too (--info, --stacktrace, a task name).
#
# Plain POSIX sh on purpose: `sh android/scripts/build.sh` is a natural way to
# run it, and that shell has no BASH_SOURCE and no `set -o pipefail`.
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ANDROID_DIR="$(dirname "$SCRIPT_DIR")"

if [ ! -f "$SCRIPT_DIR/env.sh" ]; then
  echo "error: $SCRIPT_DIR/env.sh is missing — run setup-toolchain.sh first" >&2
  exit 1
fi

# shellcheck source=/dev/null
. "$SCRIPT_DIR/env.sh"

cd "$ANDROID_DIR"

WRAPPER_JAR="gradle/wrapper/gradle-wrapper.jar"
if [ ! -f "$WRAPPER_JAR" ] || ! unzip -l "$WRAPPER_JAR" >/dev/null 2>&1; then
  # Without a readable jar the JVM only says "Could not find or load main class
  # org.gradle.wrapper.GradleWrapperMain", which points nowhere near the cause.
  echo "error: $ANDROID_DIR/$WRAPPER_JAR is missing or corrupt." >&2
  echo "It is committed to the repository, so restore it with:" >&2
  echo "  git -C \"$ANDROID_DIR\" checkout -- $WRAPPER_JAR" >&2
  echo "If that reports nothing to restore, the checkout is incomplete —" >&2
  echo "re-clone the repository rather than copying the folder." >&2
  exit 1
fi

# Клон может приехать без прав на исполнение — через zip/scp, с
# core.fileMode=false или из Windows-чекаута. Тогда ./gradlew падает с
# невнятным сообщением; проще выставить бит, чем объяснять его отсутствие.
[ -x ./gradlew ] || chmod +x ./gradlew

./gradlew "${@:-assembleDebug}"

APK="$ANDROID_DIR/app/build/outputs/apk/debug/app-debug.apk"
if [ -f "$APK" ]; then
  echo
  echo "APK: $APK"
  echo "Install on a connected device/emulator with:"
  echo "  \$ANDROID_HOME/platform-tools/adb install -r \"$APK\""
fi
