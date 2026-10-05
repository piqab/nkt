---
title: Android app
---

# Android app

A native phone client for the [hub](/en/guide/hub): the same API as the web
UI, with screens built for a finger rather than a mouse. The hub itself does
not change — the app is just another way to talk to it, like a browser.
Android 8 or newer is required.

::: warning Beta
The app is new. Everything it does is checked by decoding real server
responses in tests, but it has seen few devices so far — please report
oddities in [issues](https://github.com/piqab/nkt/issues).
:::

## Installation

Every [nkt release](https://github.com/piqab/nkt/releases) carries a signed
`nkt-android-<version>.apk` (checksum in `SHA256SUMS`). Download it on the
phone and open it — Android asks to allow installs from that source.
Updating is the same: the new APK installs over the old one, sign-in and
settings are kept.

The app version is the nkt version: app `1.11.150` is written for hub
`1.11.150`. Both are shown in the app's About screen; with a newer hub the
app works, but may lack the hub's newest sections.

## Language

The interface is in Russian and English. By default it follows the phone
language (Russian; any other — English). Switch it on the sign-in screen or
in About, instantly, without a restart. Server messages — errors, job logs,
alert and problem texts — come in the same language.

## Sign-in

The hub address is the one used in the browser (`http://192.168.1.10:8077`
or `https://hub.example.com`); the login and password are a hub account.
The session survives app restarts.

If the hub serves a self-signed certificate (`NKT_TLS_ENABLED`), the app
remembers it on first sign-in and checks it afterwards: a swapped
certificate is an error, not a silent connection. The fingerprint is shown
in About; after reinstalling the hub, reset the remembered certificate
there.

## What's inside

**Host list** — by group: state, number of problems, Kubernetes role, sudo
(passwordless in red), an nkt version behind the hub. The bell on top is
unread alerts, the menu button on the left opens the hub sections, the list
icon manages groups (create, rename, delete).

**Host management** (admins): “+” adds a host — address, SSH user and
sign-in with a password, your own key or a hub key (the app then shows the
line for the host's `authorized_keys`), and optionally installs nkt right
away. A row's “⋮” menu has install, update to the hub's version or reinstall
of nkt (a hub job with a live log), the last install log, moving to a group,
and deleting: from the hub only, or together with nkt on the host itself
(service, data, the hub's access; password sign-in is restored). If the host
already runs a foreign nkt, the app asks whether to install over it.

**A host** opens with its own section menu: overview, problems (each with a
jump to where it is fixed), terminal and btop, logs, services, containers
(Docker has “Inspect” with environment variables), vulnerabilities, ClamAV,
availability, usage, configs, firewall, certificates, interfaces, the
resource map, users, jobs, fail2ban, audit log. The “⋮” menu has host
reboot: the app first shows what is running and what will not come back by
itself.

**Hub sections:**

| Section | What |
|---|---|
| Alerts | the hub journal with a kind filter; “To host” opens the host right at the relevant section |
| Hub jobs | deployments, fleet bans and other hub jobs with a live log and cancel |
| Monitoring | what needs attention (disks, leaks, availability drops), host CPU, memory and disks, cluster nodes |
| Fail2ban | banned addresses across all hosts; ban and unban everywhere as a hub job |
| Deployments | pipelines: deploy, dry run, log, history with rollback, old stacks left on a previous host |

Creating and editing pipelines, profiles, scripts, clusters, hub export and
import, and settings stays in the web UI: those are editors with diffs, not phone work.

## Notifications

Alerts has a “Phone notifications” switch. When on, every 15 minutes (Android
does not allow background checks more often) it asks the hub for new events
and shows the kinds the hub also sends to Slack/Telegram — the choice comes
from the hub's alert settings; the phone keeps no list of its own. Tapping a
notification opens the host at the event's section. The first check only
notes where the journal is — old events do not pop up.

The app uses no push service (Firebase): the hub needs no Google account or
server key, and the phone reaches the hub itself, like a browser.

## Building from source

```sh
android/scripts/setup-toolchain.sh   # JDK 17 and Android SDK in ~/.local, no root
android/scripts/build.sh test        # tests: server responses decoded by the app's models
android/scripts/build.sh             # debug APK
```

The APK is `android/app/build/outputs/apk/debug/app-debug.apk`. A debug build
is signed with a developer key and will not install over a release build
(or the other way round) — uninstall the app first.

### Signed APK in a release

The release builds it (`.github/workflows/release.yml`, job `android-apk`)
when the repository secrets hold a signing key. The key is created once:

```sh
keytool -genkeypair -keystore nkt-release.jks -alias nkt \
  -keyalg RSA -keysize 4096 -validity 36500
base64 -w0 nkt-release.jks   # → secret ANDROID_KEYSTORE_BASE64
```

Secrets: `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`,
`ANDROID_KEY_ALIAS`, `ANDROID_KEY_PASSWORD`. Without them the release ships
without an APK. Keep the key separately and safely: a lost key means
installed apps can no longer be updated.

### Beta release of the app only

To publish the app separately from nkt: **Actions → android-beta-release →
Run workflow**. It runs the tests, signs the APK with the same key and
creates a pre-release `android-v<version>-beta` with
`nkt-android-<version>-beta.apk` and `SHA256SUMS`; the app's About shows the
version as `<version>-beta`. Hubs do not see such a release and do not offer
it as an update. Running it again for the same version replaces the APK in
that release.

A locally signed build uses the same environment variables
(`NKT_ANDROID_KEYSTORE` is the .jks path; the rest are passwords and alias):

```sh
NKT_ANDROID_KEYSTORE=$HOME/nkt-release.jks NKT_ANDROID_KEY_ALIAS=nkt \
NKT_ANDROID_KEYSTORE_PASSWORD=… NKT_ANDROID_KEY_PASSWORD=… \
  android/scripts/build.sh assembleRelease
```
