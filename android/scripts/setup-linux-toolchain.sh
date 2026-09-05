#!/usr/bin/env bash
# Installs everything needed to build the android/ project from the command
# line, with no Android Studio and no root/sudo — JDK, Gradle and the
# Android SDK command-line tools, all under $HOME/.local (same convention
# as the project's own go/node toolchains, see project's memory:
# project_native_toolchains).
#
# Idempotent: safe to re-run, skips anything already installed at the
# expected version. Takes a few minutes and ~2-3 GB of disk the first time
# (JDK + Gradle + SDK platform/build-tools).
#
# Usage:
#   android/scripts/setup-linux-toolchain.sh
#   source android/scripts/env.sh   # after it finishes, in every new shell
set -euo pipefail

LOCAL="$HOME/.local"
JDK_VERSION="17.0.20.1_1"
JDK_DIR="$LOCAL/jdk-17"
GRADLE_VERSION="8.9"
GRADLE_DIR="$LOCAL/gradle-$GRADLE_VERSION"
SDK_DIR="$LOCAL/android-sdk"
CMDLINE_TOOLS_VERSION="11076708" # cmdline-tools "latest" as of writing
PLATFORM="android-35"
BUILD_TOOLS="35.0.0"

log() { echo "==> $*" >&2; }

# --- JDK 17 (Temurin) ---
if [ -x "$JDK_DIR/bin/java" ]; then
  log "JDK already at $JDK_DIR ($("$JDK_DIR/bin/java" -version 2>&1 | head -1))"
else
  log "Downloading Temurin JDK 17..."
  tmp=$(mktemp -d)
  curl -fL --max-time 300 -o "$tmp/jdk.tar.gz" \
    "https://api.adoptium.net/v3/binary/latest/17/ga/linux/x64/jdk/hotspot/normal/eclipse?project=jdk"
  mkdir -p "$JDK_DIR"
  tar -xzf "$tmp/jdk.tar.gz" -C "$JDK_DIR" --strip-components=1
  rm -rf "$tmp"
  log "JDK installed: $("$JDK_DIR/bin/java" -version 2>&1 | head -1)"
fi

# --- Gradle ---
if [ -x "$GRADLE_DIR/bin/gradle" ]; then
  log "Gradle already at $GRADLE_DIR"
else
  log "Downloading Gradle $GRADLE_VERSION..."
  tmp=$(mktemp -d)
  curl -fL --max-time 300 -o "$tmp/gradle.zip" \
    "https://services.gradle.org/distributions/gradle-${GRADLE_VERSION}-bin.zip"
  unzip -q "$tmp/gradle.zip" -d "$tmp"
  mkdir -p "$LOCAL"
  rm -rf "$GRADLE_DIR"
  mv "$tmp/gradle-${GRADLE_VERSION}" "$GRADLE_DIR"
  rm -rf "$tmp"
  log "Gradle installed at $GRADLE_DIR"
fi

export JAVA_HOME="$JDK_DIR"
export PATH="$JDK_DIR/bin:$GRADLE_DIR/bin:$PATH"

# --- Android SDK command-line tools ---
if [ -x "$SDK_DIR/cmdline-tools/latest/bin/sdkmanager" ]; then
  log "Android cmdline-tools already at $SDK_DIR"
else
  log "Downloading Android cmdline-tools..."
  tmp=$(mktemp -d)
  curl -fL --max-time 300 -o "$tmp/cmdline-tools.zip" \
    "https://dl.google.com/android/repository/commandlinetools-linux-${CMDLINE_TOOLS_VERSION}_latest.zip"
  mkdir -p "$SDK_DIR/cmdline-tools"
  unzip -q "$tmp/cmdline-tools.zip" -d "$tmp"
  rm -rf "$SDK_DIR/cmdline-tools/latest"
  # Google's zip extracts to "cmdline-tools/" — sdkmanager insists on living
  # under a versioned subdir like ".../cmdline-tools/latest/bin/...".
  mv "$tmp/cmdline-tools" "$SDK_DIR/cmdline-tools/latest"
  rm -rf "$tmp"
  log "cmdline-tools installed at $SDK_DIR"
fi

export ANDROID_HOME="$SDK_DIR"
export ANDROID_SDK_ROOT="$SDK_DIR"
SDKMANAGER="$SDK_DIR/cmdline-tools/latest/bin/sdkmanager"

log "Accepting SDK licenses..."
yes | "$SDKMANAGER" --sdk_root="$SDK_DIR" --licenses >/dev/null 2>&1 || true

log "Installing platform-tools, platforms;$PLATFORM, build-tools;$BUILD_TOOLS..."
"$SDKMANAGER" --sdk_root="$SDK_DIR" \
  "platform-tools" "platforms;$PLATFORM" "build-tools;$BUILD_TOOLS" >&2

# --- local.properties (machine-specific, gitignored) ---
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cat > "$REPO_ROOT/android/local.properties" <<EOF
sdk.dir=$SDK_DIR
EOF

# --- env.sh helper for future shells ---
cat > "$(dirname "${BASH_SOURCE[0]}")/env.sh" <<EOF
# Source this in any shell before building android/ from the command line:
#   source android/scripts/env.sh
export JAVA_HOME="$JDK_DIR"
export ANDROID_HOME="$SDK_DIR"
export ANDROID_SDK_ROOT="$SDK_DIR"
export PATH="$JDK_DIR/bin:$GRADLE_DIR/bin:\$PATH"
EOF

log "Done. In new shells, run: source android/scripts/env.sh"
log "Then build with: cd android && ./gradlew assembleDebug"
