---
owner: @esengine
backup: @SivanCola
status: active
reviewed: 2026-10-20
---

# Reasonix Mobile — the Android shell

Runs Reasonix as an Android app: the kernel ships inside the APK and starts on the
device, the interface is the same `desktop/frontend-next` build the desktop Studio
serves, and the agent reaches servers over SSH.

This file is for people building it. What a person installing it needs — what it
does, how to pair with a computer, what it cannot do — belongs in the release
notes.

## One command

```bash
./scripts/build.sh release     # or debug
```

Three steps, in this order:

1. `build-kernel.sh` → `app/src/main/jniLibs/arm64-v8a/libreasonix.so`
2. `sync-page.sh` → `app/src/main/assets/page/`
3. `./gradlew assembleRelease`

Output: `app/build/outputs/apk/release/app-release.apk`

The kernel and the page are **inputs to packaging, not Gradle build steps**.
Running `gradle assembleRelease` on its own fails on purpose and names the script
that produces whichever input is missing (`requireBuildInputs` in
`app/build.gradle`): an APK with no kernel in it should not be buildable by
accident.

## Requirements

| Component | Version |
| --- | --- |
| JDK | 21 (17 also works) |
| Android SDK | `platforms;android-35`, `build-tools;35.0.0`, `platform-tools` |
| Gradle | the committed wrapper, 8.14.3 (for AGP 8.11.1) |
| Go | 1.26+ |
| Node / pnpm | Node 24+ / pnpm 10 |

```bash
export ANDROID_HOME="$HOME/Android/Sdk"
export ANDROID_SDK_ROOT="$ANDROID_HOME"
export JAVA_HOME="/path/to/jdk-21"
export PATH="$PATH:$(go env GOPATH)/bin"
```

`ANDROID_HOME` is the one thing nothing sets for you.

## Scripts

| Script | What it does |
| --- | --- |
| `build.sh [debug\|release]` | The whole chain |
| `build-kernel.sh` | Cross-compiles the kernel only (`GOOS=android GOARCH=arm64 CGO_ENABLED=0`) |
| `sync-page.sh` | Builds the frontend, copies `dist/` into assets, copies `LICENSE` |
| `verify-page.sh` | **No phone needed**: starts the real kernel against the packaged page |
| `run.sh [debug\|release]` | `adb install`, launch, follow the log |

`verify-page.sh` is the one to run after a change: it starts the kernel with the
same arguments `Kernel.kt` uses (`--auth token`, `--token-file`, `--port-file`,
`-page`) and checks that it binds a port and answers 200. Without a device, that
is as deep as the chain can be verified.

## Where the code is

| File | Owns |
| --- | --- |
| `MainActivity.kt` | The WebView, the back gesture, downloads, external links, renderer restart |
| `Kernel.kt` | Unpacking the page, starting and stopping the kernel, waiting for the port, health probes, logs, the workspace directory |
| `KernelService.kt` | The foreground service that keeps a long turn alive |
| `NativeBridge.kt` | `@JavascriptInterface`: what the page may ask the phone for |
| `BrowserShellServer.kt` | Loopback HTTP server the kernel's browser tools drive the overlay WebView through |
| `Downloader.kt` | Writing a kernel-produced file through the system document picker |

The page reaches the shell through `window.reasonixHost` and `ReasonixNative`,
defined in `desktop/frontend-next/src/port/host.ts` (`AndroidHost`).

**Phone and desktop share one page build.** What differs only on a phone is
decided at runtime, not by a second set of components. `phoneShell()` in
`src/port/host.ts` and the `[data-shell="android"]` selector in `mobile.css` both
read `<html data-shell="android">`, which `index.html` sets before the bundle
loads.

One attribute feeding both is what keeps style and components from disagreeing
about what a phone is.

## What the page may ask the phone for

`NativeBridge` is reachable from whatever document the WebView holds, so every
method that touches the device asks `trusted()` first.

The activity answers by comparing the **main frame's** origin against the
kernel's and against the one paired computer the person scanned a code for. A
page that navigated elsewhere is not trusted, even though it may still hold the
object.

Five methods answer without the check — `version`, `systemTheme`, `safeArea`,
`setLayerOpen`, `setRunning` — because they report back what the page already
knows about its own window and grant nothing.

`openInApp(url)` is the pairing decision, not a navigation one: it loads a URL in
this window only for the origin the scanner just returned, and only once.
Without that, any page holding the kernel origin could hand itself the bridge.

External links leave through the browser. A non-web scheme is routed to the
system only for `mailto:` and `tel:`; everything else is dropped, because an
`intent://` URL can start an arbitrary exported activity with caller-supplied
extras.

## Permissions, and what they cost

`MANAGE_EXTERNAL_STORAGE` — opening a folder on the phone as a workspace. The
kernel is a Go binary: it opens real paths with `open(2)`, and SAF hands back a
`content://` URI it cannot open.

Requested from the folder-picker action, never at launch. Android shows no dialog
for it and opens system settings instead.

**There is no OS-level sandbox around this kernel.** The PR sets the sandbox to
off on Android, so confining the agent to the chosen folder is Reasonix's own
write-root check and nothing else. A shell tool with all-files access can reach
all shared storage.

`REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` — Doze freezes the app group, which stops
a turn that is still running. Asked once when a turn actually runs, never at
launch, and the person can refuse. Refusing costs battery life during long turns
and nothing else.

**Google Play restricts this permission**: fine for a sideloaded build, a
store-policy risk if this is ever listed.

`FOREGROUND_SERVICE` / `FOREGROUND_SERVICE_DATA_SYNC` — keeping the kernel alive
while the screen is off. `dataSync` is capped per 24 hours from Android 15. It is
the closest honest description of "an agent is working".

`CAMERA` — reading the pairing code a computer shows. Requested when the person
asks to scan, never at launch. `ScanActivity` is not exported.

`READ_EXTERNAL_STORAGE` / `WRITE_EXTERNAL_STORAGE` — the same folder walk below
API 33. `maxSdkVersion`-gated; above 33 they are superseded by the permission
above.

`allowBackup=false` with explicit extraction rules, so a `keystore` or a token in
app storage never leaves the device in a cloud backup.

## Cleartext HTTP

The kernel serves its page on `127.0.0.1:<port>`, and a paired computer serves
the same page over its LAN address. Both are plain HTTP.

Neither address can be written into a `network-security-config`: the port is
chosen at start, the LAN address at scan time, and the format matches literals
and domains, not subnets.

So the config permits cleartext and the boundary is in the code instead.
`shouldOverrideUrlLoading` sends every other http(s) origin to the browser, and
the bridge answers only the kernel and the just-paired computer. A plaintext page
on the LAN cannot become the document holding the bridge.

## What goes into the APK

| Content | Size | Why it is shaped this way |
| --- | --- | --- |
| `lib/arm64-v8a/libreasonix.so` | 45 MB | Go static binary, stripped. It is a `.so` rather than a file in `assets/` because only `nativeLibraryDir` is guaranteed executable — a binary copied to app storage fails on a device whose data partition is mounted `noexec`. |
| `assets/page/` | 14 MB | The frontend build, including the Inter and Noto Sans SC fonts, `noCompress` |
| `assets/LICENSE` | 1 KB | The repository's MIT licence, verbatim. MIT requires the notice and the licence text to accompany a binary distribution. |

`assets/LICENSE` is generated by `sync-page.sh` from the repository's root
`LICENSE`, not committed a second time: a checked-in copy drifts when the root
file is updated.

It sits in `assets/` rather than `assets/page/` because that directory is removed
and rebuilt on every build.

A release APK is about 31 MB.

## Building

**The Gradle wrapper is committed**, jar and all. `build.sh` refuses to run
without `gradlew` rather than generating one from whatever `gradle` is on `PATH`:
the dependency versions are part of the build.

`gradle-wrapper.properties` pins `distributionSha256Sum`, so a mirror that serves
a different distribution zip fails verification instead of being unpacked and
run. Dependency repositories are the official ones only — a mirror belongs in a
user-level init script.

**Signing.** A release with no key configured comes out **unsigned**
(`app-release-unsigned.apk`) with a warning. It used to fall back to the debug
key, which produced an artifact that looked shippable and was signed by a
per-machine key nobody chose: it could not install over the previous build.

Point the build at a real key by writing `keystore/keystore.properties`
(`storeFile`, `storePassword`, `keyAlias`, and optionally `keyPassword`) or by
setting the matching `REASONIX_KEYSTORE*` environment variables. Key material
stays out of the repository and out of build logs.

Debug builds always use the debug key. Signing a debug build with the release key
would let anyone who can read the keystore produce a build that installs over the
shipped one under the same signature.

**Non-ASCII checkout paths.** AGP refuses to build under a path with non-ASCII
characters on Windows unless `android.overridePathCheck=true` is set. That is a
property of one machine's disk layout, not of this project, so it is not in the
repository's `gradle.properties`.

Set it in a user-level `~/.gradle/gradle.properties` or pass
`-Pandroid.overridePathCheck=true`.
