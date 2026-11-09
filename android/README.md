# VCRX for Android

The phone and tablet client for VCRX Media Server: connect by address, sign in (password, profile and PIN, or an invite link), browse Home and every library, search, open details, and play video full screen with Media3 ExoPlayer. Music, audiobooks and podcasts play in the background with notification and lock screen controls, Android Auto lists them as a media app, and movies, episodes, albums, books and podcast episodes download for offline playback.

No telemetry, analytics, crash reporting, Google Play services, ads or accounts. It talks only to the server you point it at. Distributed as a sideloaded APK; there is no Play Store listing. Package `io.github.pete_builds.vcrx.mobile` (the debug build adds `.debug`), Android 8.0 (API 26) and up.

## What it does

| Area | What works |
|---|---|
| Sign in | Server address (http on a private network, or https; plain http to a public host asks first, as the TV does), username and password, password-free sign-in on a trusted network, "Who's watching?" with PINs, and Open an invite link. The token is encrypted with an Android Keystore AES-GCM key, as on the TV (`data/TokenStore.kt`). |
| Browse | Home rows from `/api/v1/home` (Continue Watching and the rest, in the user's layout), libraries, details for movies, shows, seasons and episodes with cast, and search. A phone gets a bottom bar, a tablet a navigation rail. |
| Video | Full screen, landscape, picture in picture. The phone sends the decoders it really has (`caps/CapabilityProbe.kt`), so the server plays the file directly when the phone can decode it and converts it otherwise; a converted stream seeks through the server's seek endpoint (`player/ConvertedStream.kt`, from the TV). Subtitles are the server's WebVTT tracks, drawn in file time, so they work for both. Resume, playback events and heartbeats (`player/PlaybackReporter.kt`), Skip Intro and Skip Credits. |
| Music | Recently added, albums, artists, album and artist pages, queue, shuffle. A Media3 `MediaLibraryService` (`auto/PlaybackService.kt`) plays it, so it carries on with the screen off and shows notification controls. Each track opens a listen in Activity. On an invited device, which is stream only, every track and every audiobook file is fetched inside its own listen (`session=` on the URL, `music/ListenSessions.kt`, as the TV does), so a server that requires that still plays it; the household is unchanged. |
| IPTV | The server's channels with what is on now and next; tap one to watch. Shown when `/api/v1/me` says `live_tv`. |
| Audiobooks | Shown when `/api/v1/libraries` has a library of kind `audiobook`. Covers with Continue listening on top, four sorts, and a book page: author, narrator, series, chapters (tap one to play from it), Resume, From the start, Mark finished, Download. A book is its files as one playlist in the same `PlaybackService` (`music/BookTimeline.kt`): a place is seconds across the whole book, so skips and chapter jumps cross files; a converted file is reloaded at the place with `start_ms`, as the TV does. Back 15 and forward 30 seconds (in the notification too), previous and next chapter, speed 0.8x to 2.5x kept between books, a sleep timer (minutes or the end of the chapter), and the place saved with PUT `/progress` every 15 seconds, on pause and at the end. Each file opens a listen in Activity. |
| Podcasts | Shown when `/api/v1/me` says `podcasts`; never on an invited device, which the server answers 403. New episodes, your shows, a show page (Refresh, Unsubscribe), Add a podcast by feed URL, and search by name when `/me` says `podcast_search`. Episodes play through the server's stream route with the same skips, speed (kept apart from books') and sleep timer, save their place every 15 seconds and on pause, and can be marked played or unplayed. Import OPML (the system file picker; the file goes up as it is to POST `/podcasts/opml`, and the screen lists what was added, already there or failed) and Export OPML (saves GET `/podcasts/opml` where you choose) are in the Podcasts menu (`music/Opml.kt`). |
| Downloads | Download a movie, an episode, a whole album, a whole audiobook (every file, from `/audiobooks/files/{id}/file`) or a podcast episode (a full GET of `/podcasts/episodes/{id}/stream`). Media3 `DownloadManager` and `DownloadService` with a progress notification, Wi-Fi only by default (a switch on the Downloads screen), storage used and free, delete. A book is one row however many files it has. |
| Offline | When the server cannot be reached the app opens on Downloads, and everything there plays with no network (airplane mode included). Progress made offline is kept on the phone and sent when the server answers again: watched to the end becomes Watched, anything else a resume point; a book or episode sends its place, finished or played at the end (`offline/ListenPlaces.kt`). A downloaded book keeps its files and chapters on the phone, so chapter skips work offline too. |
| Android Auto | Continue listening (books in progress, episodes left part way, albums), Music (Recently added, Artists, Albums), Audiobooks (books in progress first; open one for its chapters), Podcasts (New episodes, then your shows) and Downloads. Auto shows at most four tabs, so when music, audiobooks and podcasts are all there Downloads moves to the end of Continue listening. Grids for albums and books, lists for tracks and episodes, artwork through a content provider. Search and voice requests ("play ... on VCRX") find albums and artists as before, and books (title, author, series), shows and episodes when the session has them; one of those plays first only when it matches better than the best music answer, and a show plays its newest unplayed episode (`auto/VoiceSearch.kt`). Audio only. |

Invited devices stream but never download, by design: the Download buttons are hidden when `/api/v1/me` answers `kind: token` (or the device joined by invite), and the server refuses those devices the whole-file routes anyway (`streamOnly` in `server/internal/api/invites.go`). They get no podcasts either; audiobooks they stream.

A phone's bottom bar holds six destinations. When the server has more (Home, Search, Library, Music, Audiobooks, Podcasts, IPTV, Downloads), the bar keeps the first four and Downloads, and the rest go under More (`ui/NavTabs.kt`); a tablet's rail shows them all. Audiobook libraries and Podcasts are also on the Library screen.

## Build the APK

One command, on any machine with Docker and buildx:

```sh
./android/build-apk.sh
```

The APKs land in `android/build/out/`:

| File | What it is |
|---|---|
| `vcrx-mobile-release.apk` | Install this one. Shrunk with R8, signed with your key. |
| `vcrx-mobile-debug.apk` | The same app as `io.github.pete_builds.vcrx.mobile.debug`, installs beside the release. |

The build runs the unit tests first and stops if any fail, and its log ends with `aapt2 dump badging` for the release APK. The image is `linux/amd64` because Google's `aapt2` is x86_64 only; on an Apple silicon Mac it runs under emulation (OrbStack or Docker Desktop with Rosetta), which works and is slower.

### Signing key: back it up

The first run creates `android/signing/vcrx-mobile.jks` and a random password in `android/signing/vcrx-mobile.pass`, separate from the TV's key. Both are gitignored and only reach the build as BuildKit secrets. **Copy `android/signing/` somewhere safe.** Android only upgrades an installed app with an APK signed by the same key; lose it and every phone has to uninstall VCRX, losing its downloads, before a new build installs.

### What is pinned

The same toolchain and libraries as the TV (`tv/README.md`): the `sdk` stage of `android/Dockerfile` is byte for byte the TV's, so one Docker host shares that layer, and `android/gradle/libs.versions.toml` uses the TV's versions, plus Material 3 from the same Compose BOM and `media3-session`, `media3-database` and `media3-datasource` at Media3 1.11.1.

There is no FFmpeg stage. Phones decode AAC, MP3, FLAC, Opus and Vorbis in hardware or the platform, and the capabilities the app sends leave out what a phone cannot decode (AC-3, DTS and TrueHD on most), so the server converts that audio. Building the TV's FFmpeg extension under amd64 emulation would add a long native compile for little gain; it can be added later the way `tv/ffmpeg/` does it.

### CI

`.github/workflows/android.yml` runs `./gradlew :app:lintDebug :app:testDebugUnitTest :app:assembleDebug` inside the `check` stage of `android/Dockerfile` on pushes and pull requests that touch `android/` or `server/api/openapi.yaml`, plus `android/build-apk_test.sh`. Run the same check locally from the repo root:

```sh
docker buildx build --platform linux/amd64 --target check \
  --build-context openapi=server/api -o type=cacheonly android
```

The unit tests are plain JVM tests of the logic that decides things: the token slots, download eligibility (an invited device gets none), the offline progress queues and their sync (video, and the place in books and episodes), the play source and start position, WebVTT cues, invite links, which destinations show (audiobooks from the library kinds, podcasts from `/me`, never on an invited device), a book's position across its files and chapters, the Android Auto browse tree, every request body against `server/api/openapi.yaml`, and every audiobook and podcast answer field against it too.

## Sideload it

1. Copy `vcrx-mobile-release.apk` to the phone (USB, a cloud drive, or a link from your own machine).
2. Open it. Android asks you to allow installs from the app you opened it with (Files, Chrome): allow it once, then Install.
3. Open VCRX, enter your server (for example `192.168.1.20`, which becomes `http://192.168.1.20:3750`), and sign in, or tap **Open an invite link** and paste the link from the server's Settings > Devices.

To update, install a newer APK signed with the same key over the old one; sign-in and downloads stay.

## Android Auto

Android Auto only lists media apps from the Play Store unless you allow others:

1. On the phone, open Android Auto's settings (Settings > Connected devices > Connection preferences > Android Auto, or the Android Auto app).
2. Scroll to the bottom and tap **Version** about ten times until it offers developer settings; accept.
3. In the menu at the top right, open **Developer settings** and turn on **Unknown sources**.
4. Back in Android Auto's settings, under **Customize launcher**, make sure VCRX is ticked.

VCRX then shows in the car's app launcher. Browse Continue listening, Music, Audiobooks, Podcasts or Downloads, or ask the Assistant to play an album, an artist, a book or a podcast on VCRX. A book or an episode resumes where you left it, on the phone or anywhere else. Downloads play in the car with no signal. Video never plays in the car.

## Layout

```
android/app/src/main/kotlin/io/github/pete_builds/vcrx/mobile/
  net/       the TV's API layer, copied: OkHttp, kotlinx.serialization, ServerApi
  caps/      decoder probe and the capabilities sent with every play request
  data/      token store (Keystore AES-GCM), settings, playback event sender
  player/    VideoPlayerActivity, converted stream timeline, reporter, skip markers, WebVTT
  offline/   downloads, download service, offline progress queue, invite links
  music/     listen sessions, a book's timeline (files and chapters), podcast skips and speeds
  auto/      MediaLibraryService for music, books, episodes and Android Auto, browse tree, artwork provider
  ui/        Compose screens
```

`net/`, `caps/` and parts of `data/` and `player/` are copies of the TV's files with the package renamed. A shared module would be tidier; it waits until the TV's layout settles.
