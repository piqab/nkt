#!/usr/bin/env bash
# Prepares everything needed to build the android/ project from the command
# line, with no Android Studio and no root/sudo. Works on Linux and macOS
# (Intel and Apple Silicon).
#
# Nothing is downloaded that the machine already has: an existing JDK 17+
# (including the one bundled with Android Studio, or a Homebrew one), an
# existing Android SDK (including Android Studio's own), and already-installed
# SDK packages are all detected and reused. Only genuinely missing pieces are
# fetched, into $HOME/.local — the same convention as this repo's go/node
# toolchains.
#
# Gradle itself is never downloaded here: android/gradlew (the committed
# wrapper) fetches the exact version the project expects on first use.
#
# Idempotent — safe to re-run; a second run should download nothing.
#
# Usage:
#   android/scripts/setup-toolchain.sh
#   source android/scripts/env.sh   # afterwards, in every new shell

# This script needs bash (arrays); `sh setup-toolchain.sh` would fail on dash
# in confusing ways, so re-exec instead of letting it break halfway through.
if [ -z "${BASH_VERSION:-}" ]; then
  exec bash "$0" "$@"
fi

set -euo pipefail

LOCAL="$HOME/.local"
JDK_DIR="$LOCAL/jdk-17"
OWN_SDK_DIR="$LOCAL/android-sdk"
CMDLINE_TOOLS_VERSION="11076708" # cmdline-tools "latest" as of writing
PLATFORM="android-35"
BUILD_TOOLS="35.0.0"
MIN_JDK=17

log() { echo "==> $*" >&2; }
die() { echo "error: $*" >&2; exit 1; }

# --- platform ---------------------------------------------------------------

case "$(uname -s)" in
  Linux)  JDK_OS=linux; TOOLS_OS=linux ;;
  Darwin) JDK_OS=mac;   TOOLS_OS=mac   ;;
  *) die "unsupported OS: $(uname -s) (this script handles Linux and macOS)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64)  JDK_ARCH=x64 ;;
  arm64|aarch64) JDK_ARCH=aarch64 ;;
  *) die "unsupported CPU: $(uname -m)" ;;
esac

for tool in curl unzip tar; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is required but not installed"
done

# --- JDK --------------------------------------------------------------------

# Major version of the JDK at $1 (a JAVA_HOME-style dir), or nothing if it
# isn't a usable JDK. Handles both "17.0.2" and legacy "1.8.0_292" forms.
jdk_major() {
  local home="$1" raw
  [ -x "$home/bin/java" ] || return 0
  raw=$("$home/bin/java" -version 2>&1 | head -1 | sed -n 's/.*version "\([0-9][0-9.]*\).*/\1/p')
  [ -n "$raw" ] || return 0
  case "$raw" in
    1.*) echo "$raw" | cut -d. -f2 ;;
    *)   echo "$raw" | cut -d. -f1 ;;
  esac
}

# Prints the first candidate that is a JDK >= $MIN_JDK.
find_existing_jdk() {
  local candidates=() c major

  [ -n "${JAVA_HOME:-}" ] && candidates+=("$JAVA_HOME")
  candidates+=("$JDK_DIR")
  # macOS keeps its own registry of installed JDKs; ask it rather than guessing.
  if [ "$JDK_OS" = mac ] && [ -x /usr/libexec/java_home ]; then
    for v in 17 21 23; do
      c=$(/usr/libexec/java_home -v "$v" 2>/dev/null || true)
      [ -n "$c" ] && candidates+=("$c")
    done
    candidates+=("/Applications/Android Studio.app/Contents/jbr/Contents/Home")
    candidates+=("/opt/homebrew/opt/openjdk@17" "/usr/local/opt/openjdk@17")
  else
    candidates+=("/opt/android-studio/jbr" "$HOME/android-studio/jbr")
    # A java on PATH knows its own home better than any path guess does.
    if command -v java >/dev/null 2>&1; then
      c=$(java -XshowSettings:properties -version 2>&1 |
          sed -n 's/^ *java.home = *//p' | head -1 || true)
      [ -n "$c" ] && candidates+=("$c")
    fi
  fi
  : # keep the function's exit status independent of the tests above

  for c in "${candidates[@]}"; do
    [ -n "$c" ] && [ -d "$c" ] || continue
    major=$(jdk_major "$c")
    if [ -n "$major" ] && [ "$major" -ge "$MIN_JDK" ] 2>/dev/null; then
      # A JRE would pass the java check but fail to compile anything.
      [ -x "$c/bin/javac" ] || continue
      echo "$c"
      return 0
    fi
  done
  return 1
}

install_jdk() {
  log "No JDK $MIN_JDK+ found — downloading Temurin $MIN_JDK ($JDK_OS/$JDK_ARCH)..."
  local tmp
  tmp=$(mktemp -d)
  curl -fL --max-time 600 -o "$tmp/jdk.tar.gz" \
    "https://api.adoptium.net/v3/binary/latest/${MIN_JDK}/ga/${JDK_OS}/${JDK_ARCH}/jdk/hotspot/normal/eclipse?project=jdk"
  rm -rf "$JDK_DIR"
  mkdir -p "$JDK_DIR"
  tar -xzf "$tmp/jdk.tar.gz" -C "$JDK_DIR" --strip-components=1
  rm -rf "$tmp"
  # macOS tarballs are .jdk bundles: the real JAVA_HOME sits deeper.
  if [ -x "$JDK_DIR/Contents/Home/bin/java" ]; then
    echo "$JDK_DIR/Contents/Home"
  else
    echo "$JDK_DIR"
  fi
}

if JAVA_HOME_FOUND=$(find_existing_jdk); then
  log "Using existing JDK: $JAVA_HOME_FOUND ($("$JAVA_HOME_FOUND/bin/java" -version 2>&1 | head -1))"
else
  JAVA_HOME_FOUND=$(install_jdk)
  log "JDK installed: $("$JAVA_HOME_FOUND/bin/java" -version 2>&1 | head -1)"
fi

export JAVA_HOME="$JAVA_HOME_FOUND"
export PATH="$JAVA_HOME/bin:$PATH"

# --- Android SDK ------------------------------------------------------------

# An SDK directory is anything that looks like one; cmdline-tools can be added
# to it afterwards if missing.
looks_like_sdk() {
  [ -d "$1" ] && { [ -d "$1/platforms" ] || [ -d "$1/platform-tools" ] ||
                   [ -d "$1/cmdline-tools" ] || [ -d "$1/licenses" ]; }
}

find_existing_sdk() {
  local candidates=() c
  [ -n "${ANDROID_HOME:-}" ]     && candidates+=("$ANDROID_HOME")
  [ -n "${ANDROID_SDK_ROOT:-}" ] && candidates+=("$ANDROID_SDK_ROOT")
  # Android Studio's own default locations, per OS.
  candidates+=("$HOME/Library/Android/sdk" "$HOME/Android/Sdk" "$OWN_SDK_DIR")
  for c in "${candidates[@]}"; do
    if looks_like_sdk "$c"; then echo "$c"; return 0; fi
  done
  return 1
}

if SDK_DIR=$(find_existing_sdk); then
  log "Using existing Android SDK: $SDK_DIR"
else
  SDK_DIR="$OWN_SDK_DIR"
  log "No Android SDK found — will create one at $SDK_DIR"
  mkdir -p "$SDK_DIR"
fi

export ANDROID_HOME="$SDK_DIR"
export ANDROID_SDK_ROOT="$SDK_DIR"

SDKMANAGER="$SDK_DIR/cmdline-tools/latest/bin/sdkmanager"
if [ ! -x "$SDKMANAGER" ]; then
  log "Downloading Android cmdline-tools ($TOOLS_OS)..."
  tmp=$(mktemp -d)
  curl -fL --max-time 600 -o "$tmp/cmdline-tools.zip" \
    "https://dl.google.com/android/repository/commandlinetools-${TOOLS_OS}-${CMDLINE_TOOLS_VERSION}_latest.zip"
  unzip -q "$tmp/cmdline-tools.zip" -d "$tmp"
  mkdir -p "$SDK_DIR/cmdline-tools"
  rm -rf "$SDK_DIR/cmdline-tools/latest"
  # Google's zip extracts to a bare "cmdline-tools/"; sdkmanager insists on
  # living under a versioned subdir such as ".../cmdline-tools/latest/bin/".
  mv "$tmp/cmdline-tools" "$SDK_DIR/cmdline-tools/latest"
  rm -rf "$tmp"
  log "cmdline-tools installed into $SDK_DIR"
else
  log "cmdline-tools already present"
fi

# Only ask sdkmanager for packages that are actually missing — it is slow even
# when it has nothing to do, and it always reaches out to the network.
missing=()
[ -x "$SDK_DIR/platform-tools/adb" ]          || missing+=("platform-tools")
[ -d "$SDK_DIR/platforms/$PLATFORM" ]         || missing+=("platforms;$PLATFORM")
[ -d "$SDK_DIR/build-tools/$BUILD_TOOLS" ]    || missing+=("build-tools;$BUILD_TOOLS")

if [ ${#missing[@]} -eq 0 ]; then
  log "SDK packages already installed: platform-tools, $PLATFORM, build-tools $BUILD_TOOLS"
else
  log "Accepting SDK licenses..."
  yes | "$SDKMANAGER" --sdk_root="$SDK_DIR" --licenses >/dev/null 2>&1 || true
  log "Installing: ${missing[*]}"
  "$SDKMANAGER" --sdk_root="$SDK_DIR" "${missing[@]}" >&2
fi

# --- Gradle -----------------------------------------------------------------

# Not installed on purpose: android/gradlew downloads the exact version the
# build needs. An existing gradle is still put on PATH if there is one, since
# it costs nothing and some people prefer typing `gradle`.
GRADLE_BIN_DIR=""
if command -v gradle >/dev/null 2>&1; then
  GRADLE_BIN_DIR="$(cd "$(dirname "$(command -v gradle)")" && pwd)"
  log "Found gradle on PATH: $(gradle --version 2>/dev/null | sed -n 's/^Gradle //p' | head -1)"
else
  log "No system gradle — android/gradlew will fetch its own on first build"
fi

# --- generated, machine-specific files (both gitignored) --------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ANDROID_DIR="$(dirname "$SCRIPT_DIR")"

cat > "$ANDROID_DIR/local.properties" <<EOF
sdk.dir=$SDK_DIR
EOF

{
  echo "# Generated by setup-toolchain.sh — source before building:"
  echo "#   source android/scripts/env.sh"
  echo "export JAVA_HOME=\"$JAVA_HOME\""
  echo "export ANDROID_HOME=\"$SDK_DIR\""
  echo "export ANDROID_SDK_ROOT=\"$SDK_DIR\""
  if [ -n "$GRADLE_BIN_DIR" ]; then
    echo "export PATH=\"\$JAVA_HOME/bin:$GRADLE_BIN_DIR:\$ANDROID_HOME/platform-tools:\$PATH\""
  else
    echo "export PATH=\"\$JAVA_HOME/bin:\$ANDROID_HOME/platform-tools:\$PATH\""
  fi
} > "$SCRIPT_DIR/env.sh"

log "Done."
log "Build with: android/scripts/build.sh"
