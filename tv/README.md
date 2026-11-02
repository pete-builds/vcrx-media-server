# VCRX for TV

The Google TV and Android TV client for VCRX Media Server: connect by LAN address, sign in, browse Home and each library, search, open details, and play with Media3 ExoPlayer. It sends the device's real decoding capabilities with every play request, reports playback events for the activity log, and keeps the resume position even when you press Home mid-film.

No telemetry, analytics, crash reporting, Google Play services, ads or accounts. It talks only to the server you point it at and works on a LAN with no internet. Distributed as a sideloaded APK; there is no Play Store listing.

## Build the APK

One command, on any machine with Docker and buildx:

```sh
./tv/build-apk.sh
```

From Pete's NixOS box, which has no Docker daemon, use nix1's Docker over SSH:

```sh
nix-shell -p docker-client docker-buildx --run 'DOCKER_HOST=ssh://nix1 ./tv/build-apk.sh'
```

The APKs land in `tv/build/out/`:

| File | What it is |
|---|---|
| `vcrx-tv-release.apk` | Install this one. Shrunk with R8, signed with your key. |
| `vcrx-tv-debug.apk` | Same app as `io.github.pete_builds.vcrx.tv.debug`, installs beside the release. For logs and debugging. |

The build runs the unit tests first and stops if any fail, and its log ends with `aapt2 dump badging` for the release APK (package, label, launcher activities, ABIs). A rebuild after a code change takes about two and a half minutes on nix1. The first build also downloads the SDK and NDK, compiles FFmpeg (under a minute on nix1) and fetches the Gradle dependencies, a few minutes more; those layers are then cached.

Nothing is built natively on NixOS: Google's `aapt2` binary does not run there, so every step happens in the pinned image described by `tv/Dockerfile`. nix1's Docker cannot create new bridge networks (its address pool is exhausted); the build needs none.

### Signing key: back it up

The first run creates `tv/signing/vcrx-tv.jks` and a random password in `tv/signing/vcrx-tv.pass`. Both are gitignored and never enter a Docker image or cache: the build mounts them as BuildKit secrets, and if your machine has no `keytool`, the key is made in a throwaway container with no network that hands it back over stdout.

**Copy `tv/signing/` somewhere safe** (a password manager or an encrypted backup). Android only upgrades an installed app with an APK signed by the same key. Lose the key and every TV has to uninstall VCRX, which also clears its sign in, before a new build will install.

Debug builds are signed with the same key, so they upgrade cleanly too.

### What is pinned

| Piece | Version | How it is verified |
|---|---|---|
| JDK image | `eclipse-temurin:21.0.13_9-jdk-noble` | image digest |
| Android cmdline-tools | build 15859902 | SHA-256 from developer.android.com |
| Platform, build-tools, NDK, CMake | android-37.0, 36.0.0, r28c (28.2.13676358), 3.31.6 | sdkmanager checks Google's repository checksums |
| Gradle | 9.8.2 | `distributionSha256Sum` in the wrapper; wrapper jar matches Gradle's published SHA-256 |
| Android Gradle plugin | 9.5.0 (built-in Kotlin) | |
| Kotlin, Compose compiler | 2.4.21 | |
| Compose BOM, tv-material | 2026.09.00, 1.1.0 | |
| Media3 | 1.11.1 | |
| OkHttp, Coil, kotlinx-serialization | 5.5.1, 3.6.3, 1.12.0 | |
| androidx/media source for the FFmpeg extension | tag 1.11.1, commit `8c6678b6` | the build checks the commit hash |
| FFmpeg | 6.0.1 | SHA-256 `9b16b873...9623`, taken after checking the release's GPG signature |

`minSdk` is 26 (Android 8.0), `targetSdk` and `compileSdk` 37. ABIs: `armeabi-v7a` (many TCL TVs run a 32-bit userspace), `arm64-v8a`, and `x86_64` for the emulator.

### The FFmpeg audio extension

Google publishes no binary of Media3's FFmpeg decoder, so the `ffmpeg` stage builds it from source: the `androidx/media` checkout at the same tag as the Media3 libraries, vanilla FFmpeg 6.0.1 (the version that tag's README recommends), and the upstream `build_ffmpeg.sh` run unmodified with the NDK. Then `stage-extension.sh` builds `libffmpegJNI.so` for each ABI with CMake. The FFmpeg configuration is LGPL only; the build fails if `config.h` says anything but "LGPL version 2.1 or later".

Decoders: aac ac3 eac3 dca mlp truehd flac alac opus vorbis mp3 pcm_mulaw pcm_alaw.

The player runs it in `EXTENSION_RENDERER_MODE_ON`: platform decoders and passthrough to a receiver win, and FFmpeg decodes what they cannot (TrueHD, DTS-HD and E-AC-3 on TVs without those decoders). The upstream `ExperimentalFfmpegVideoRenderer` is left out on purpose: `DefaultRenderersFactory` loads it by reflection when present, and upstream marks it not yet functional.

The stage only reruns when its pins or `stage-extension.sh` change. To use the extension in Android Studio, export it once:

```sh
docker buildx build --target ffmpeg-ext --output type=local,dest=tv/ffmpeg/ext tv
```

### CI

`.github/workflows/tv.yml` runs `./gradlew :app:lintDebug :app:testDebugUnitTest :app:assembleDebug` inside the `check` stage of the same Dockerfile, on pushes and pull requests that touch `tv/`. Docker layers (SDK, FFmpeg extension, Gradle dependencies) are cached in the Actions cache, so a warm run only redoes the Gradle tasks. Run the same check locally:

```sh
docker buildx build --target check --output type=cacheonly tv
```

## Install on the TV with ADB

ADB runs from any machine on the same LAN. On this NixOS box, `nix-shell -p android-tools` provides it.

1. On the TV: Settings, System, About, then select **Android TV OS build** seven times to unlock Developer options.
2. Settings, System, Developer options: turn on **USB debugging** and **Wireless debugging**.
3. In Wireless debugging, choose **Pair device with pairing code**. Note the IP, pairing port and code.
4. Pair, then connect to the port shown on the Wireless debugging screen (not the pairing port):

   ```sh
   adb pair 192.168.1.50:37123 123456
   adb connect 192.168.1.50:41235
   ```

5. Install, or upgrade in place:

   ```sh
   adb install -r tv/build/out/vcrx-tv-release.apk
   ```

6. Note the model for the issue tracker:

   ```sh
   adb shell getprop ro.product.model
   adb shell getprop ro.build.version.release
   adb shell getprop ro.product.cpu.abilist
   ```

VCRX appears in the TV's app row with its banner. Some Google TV launchers hide sideloaded apps from the For You tab: look under Apps, or open it from Settings, Apps.

## Connect to the server

On first launch, enter the server's LAN address with its port, for example `192.168.1.20:3750` or `http://192.168.1.20:3750`. A bare address gets `http://` and port 3750. The app checks `GET /api/v1/server` and refuses anything that is not `vcrx-media-server` speaking API version 1, then asks for a username and password.

Plain HTTP is allowed because LAN addresses cannot be listed ahead of time (`res/xml/network_security_config.xml`). HTTPS comes later through a reverse proxy; enter the `https://` address then and it works with publicly trusted certificates.

The bearer token is encrypted with an AES-GCM key held in the Android Keystore; only ciphertext reaches the app's private storage, and backup and device transfer are off. The same OkHttp client, and so the same token, carries API calls, artwork, the video stream and subtitles. Requests to any other host never get the token.

## Settings

Settings is the last entry in the left rail. Each page lists its rows with the current value at the right; the focused row adds a one-line description. Everything is stored per server and user, so two people sharing a TV keep their own.

| Page | What it changes |
|---|---|
| Subtitles | On or Off for every video (Off means none, forced lines included); when On: always, forced only, or only when the audio is not in your language; first and second language (default: the TV's); style (the TV's caption settings, or custom size, colour, background, edge and position with a live preview) |
| Playback | Play the next episode and the countdown; Resume (ask, always resume, always start over); skip back and forward sizes; maximum streaming quality, sent as `max_width`, `max_height` and `max_bitrate` |
| Audio | Prefer the original language, or a preferred language; passthrough to a receiver (off removes the passthrough formats from what the TV reports and decodes everything itself) |
| Video | HDR10, HDR10+ and HLG; Dolby Vision. Off removes them from what the TV reports |
| Home screen | Show, hide and reorder the Home rows |
| Account and server | Who is signed in; switch user or sign out (shows the sign-in form even on a trusted network); change server |
| About and diagnostics | App and server versions, what this TV plays in plain words, a speed test to the server (`GET /api/v1/speedtest`), clear the image cache, licences and the raw capability list |

Subtitles during playback: the captions or menu key, or the Subtitles button in the controls, turns them on or off in one press; Subtitle track picks one. A choice made while watching sticks to that title, and to the rest of a show's episodes (a title's own choice outranks its show's). Changing the switch, mode or languages in Settings forgets those choices, so the setting always means what it says; Subtitles, Forget choices made while watching does it by hand.

The settings for trailers and skip intro are stored already (`Preferences.trailers`, `Preferences.skipIntro`); their rows appear once `FeatureHooks.TRAILERS` and `FeatureHooks.SKIP_INTRO` are on.

## Remote control

| Where | Key | Does |
|---|---|---|
| Everywhere | D-pad, center | Move focus (a green border on cards, a green fill on buttons), select |
| Browse | Back | Content to the left rail, rail to Home, Home's rail out of the app |
| Login | Back | Back to Connect |
| Player, controls hidden | Center, Play/Pause | Play or pause |
| | Left, Right | Seek by the skip size (10 s unless changed in Settings); hold to go faster (30 s, then 60 s steps), applied on release |
| | Captions or Menu | Subtitles on or off |
| | Up, Down | Show the controls: play or pause, back and forward, subtitles on or off, subtitle track, audio |
| | Back | Stop and return to Details |
| Player, controls shown | Back | Hide the controls |

Pressing Home during playback ends the session and saves the position. The final stop event is written to disk before it is sent and retried on the next launch if the network was down.

## Files the TV cannot play directly

Every play request says the app takes a converted stream (`fmp4_stream`). When the server's decision is anything but direct play, it answers with protocol `fmp4`: a fragmented MP4 piped from ffmpeg, with the video copied (or re-encoded once the server can) and the audio copied or converted to AAC. The app plays it as a progressive stream. It cannot be seeked by byte range, so every seek (left, right, the 10 s buttons) asks `GET /api/v1/playback/sessions/{id}/seek` where a stream from that point starts, a keyframe at or before it, and opens a new stream there. The position on screen and in every playback event is that start plus the player's own time, so resume points and Activity read the same as direct play.

The bar shows which kind it is: Audio converted, Repackaged, or Video converted. A converted stream carries no subtitles, and side-loaded files would drift after the first seek, since each stream restarts at zero. So the app fetches the session's WebVTT copy of a text track (`webvtt_subtitles`; an embedded track answers 202 while the server reads it out, and the app waits), parses it once with Media3's WebVTT parser, and draws the cues active at the file position (start plus player time) itself, in the same caption style. They stay in sync through any number of seeks. Picture subtitles (PGS, VobSub) cannot be drawn that way: they show in direct play only, and the subtitle picker says so on a converted stream. A converted stream that ends well before the file does is treated as cut off, not finished, so the item is not marked played.

## Brand

Follows `brand/README.md` and `brand/reference/VCRX-green-brand-board-updated.png`. Black `#000000` is the ground everywhere; VCRX Green `#008F11` marks the logo, focus, accents, primary actions and headings; body text is white and secondary text light gray. Display text, screen titles, row titles and labels are Rajdhani SemiBold and Bold, uppercase with wide tracking (0.15 em); interface and body text is Inter Regular and Medium. A focused card gets a 3dp green border and a slight scale, a focused button fills green, and nothing glows or casts a shadow. Corners are 6 to 8dp, matching the rounded icon. The Connect screen shows the wordmark and the tagline. In the player the chrome is black and the progress bar and scrubber are green; subtitles follow the TV's own caption settings unless Settings, Subtitles, Style is Custom.

Brand assets, copied unmodified from `brand/android/res/` (generated there by `brand/tools/make-assets.py`): the adaptive launcher icon with a monochrome layer (`mipmap-anydpi-v26/ic_launcher.xml`, `ic_launcher_round.xml`, and `ic_launcher_foreground.png` and `ic_launcher_monochrome.png` per density), legacy `ic_launcher.png` and `ic_launcher_round.png` for mdpi to xxxhdpi, `drawable-xhdpi/banner.png` and `drawable-xxxhdpi/banner.png` (the TV banner), `drawable-nodpi/brand_wordmark.png` and `brand_mark.png`, and `values/brand_colors.xml`. The wordmark is raster artwork shown at its own aspect ratio; the name is never re-typed in a font as the logo.

Renaming again is mechanical: every product name the app shows lives in `res/values/strings.xml`, and the logo is reached only through the neutral names `brand_wordmark` and `brand_mark`.

Fonts, all SIL Open Font License 1.1, from their official sources. Licence texts and this record ship in the APK under `assets/licenses/`.

| Font | Source | SHA-256 |
|---|---|---|
| Rajdhani SemiBold | github.com/google/fonts, `ofl/rajdhani` at commit `8772f86137e17678cbd3584ca9a3c9486c3e7e50` | `94bbd25a18ca665999feb05a537de9fd2b860dcfb78bbe9ca00270825bf235da` |
| Rajdhani Bold | same | `691470dd3286a14e9677940d0bf75796179841ba5215cbda1a2c8910a3226afd` |
| Inter Regular | github.com/rsms/inter release 4.1, `Inter-4.1.zip` (`9883fdd4a49d4fb66bd8177ba6625ef9a64aa45899767dde3d36aa425756b11e`) | `40d692fce188e4471e2b3cba937be967878f631ad3ebbbdcd587687c7ebe0c82` |
| Inter Medium | same archive | `97ad806f526e41546d46365bb3a393145f75b7b1568913db74549ad8b8dba872` |

## Emulator smoke test

`tv/scripts/emulator-smoke.sh` boots a headless Android TV emulator (API 36, x86_64, KVM) in a throwaway container, installs the release APK, and walks it against a small mock server (`tv/scripts/emulator/mock_server.py`): Connect, Login, Home, Details, a refused play request, a library, Settings and About. Screenshots, logcat, the capabilities the app sent and the mock server's request log land in `tv/scripts/out/`.

```sh
nix-shell -p docker-client docker-buildx --run 'DOCKER_HOST=ssh://nix1 ./tv/scripts/emulator-smoke.sh'
```

It needs `/dev/kvm` on the Docker host, publishes no ports and uses no network. The container is always removed; remove the image with `docker rmi vcrx-tv-emulator:smoke`.

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| "Server unreachable" | Wrong address or port, the server is down, or the TV is on another VLAN. Retry, or Change server. `curl http://<address>:3750/api/v1/server` from a laptop on the same network should answer. |
| "answered, but it is not a VCRX Media Server" | Something else listens on that port. Check the port the server is configured to use. |
| Sent back to Sign in | The server answered 401: the token expired or was revoked. Sign in again. |
| "Cannot play this here" | The server refused (403 policy) or cannot adapt that file for this device yet (422). The message is the server's. |
| "Server busy" | The server is converting as many streams as it allows (503 `busy`). Retry in a minute, or stop a converted stream on another screen. |
| No sound on some files | Settings, About and diagnostics shows whether the FFmpeg extension loaded and lists every decoder and passthrough format the TV reported. If a soundbar or receiver is silent, try Settings, Audio, Passthrough off. |
| Playback stutters | Settings, About and diagnostics runs a speed test to the server. Settings, Playback, Maximum streaming quality caps what the server sends (once it can convert video). |
| `INSTALL_FAILED_UPDATE_INCOMPATIBLE` | The installed copy was signed with a different key. Restore `tv/signing/` from backup, or `adb uninstall io.github.pete_builds.vcrx.tv` and reinstall (this clears the sign in). |
| `INSTALL_FAILED_NO_MATCHING_ABIS` | The device is 32-bit x86. Add `x86` to `abiFilters` in `app/build.gradle.kts` and to the ABI list in the Dockerfile's ffmpeg stage. |
| Logs | `adb logcat --pid=$(adb shell pidof io.github.pete_builds.vcrx.tv)` |

## Layout

```
tv/
  build-apk.sh            the one command
  Dockerfile              pinned toolchain; stages sdk, ffmpeg, deps, check, release, apk
  ffmpeg/                 Gradle module compiling the staged extension; stage-extension.sh
  app/src/main/kotlin/io/github/pete_builds/vcrx/tv/
    net/                  contract models, API client, auth interceptor, server address rules
    data/                 settings, Keystore token store, reliable playback event sender
    caps/                 capability probe and the MIME and profile to FFprobe name mapping
    ui/                   Compose for TV screens, brand components, theme
    player/               player activity, controller, event reporter, chrome
  app/src/test/           JVM unit tests (mapping, merge rules, contract decoding, events)
  scripts/                emulator smoke test
```
