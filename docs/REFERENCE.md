# VCRX Media Server: build reference

The project was called Flex Media Server when this reference was written and was renamed VCRX on 2026-10-30. Names below were updated; the facts were not.

Written 2026-10-30 from a verified research run. This public copy omits details of the author's network exposure that the private original records. Every fact about Pete's environment here was read live that day; every external fact is cited in the research report, which this file summarizes for building:

the research report on open-source Plex alternatives (kept in Pete's private workspace, not this repo)

If this file and the report disagree, the report wins. If either disagrees with a live system, the live system wins: re-check before relying on anything marked "2026-10-30".

## 1. How to use this file

Read it in full before writing the plan. Sections 3 and 4 say where things run. Sections 5 to 7 are the server design. Sections 8 to 10 are the features that justify building VCRX instead of running Jellyfin; they are the product, not extras. Section 13 holds the workspace rules that bind this repo. Section 16 lists what only Pete can decide.

## 2. Thesis and non-negotiables

Pete's thesis: a modern, privacy-first media platform with no subscriptions, advertising, corporate accounts, telemetry, or mandatory cloud services. Users own their media, server, accounts, and data. It must run on a LAN with no internet access.

- No Plex, Jellyfin or Emby code, APIs or backends. Their docs are fine to read for pitfalls. Copy no code from them or from Kometa or Tautulli.
- Use vanilla FFmpeg and FFprobe, not the jellyfin-ffmpeg fork, to keep that line clean.
- Never write to source media. Never touch Plex's files, config, port or cron jobs.
- No telemetry of any kind, not even opt-out. Maintainerr's "anonymous weekly usage report" is the pattern to avoid.
- The server must start, scan and play with the network cable pulled.

**Why build at all** (report, "Existing Tooling"): Jellyfin already covers the basic thesis. VCRX earns its existence only by doing natively what Pete's Plex needs four add-ons and three scripts for: Kometa-style collections (Kometa has no Jellyfin connector today), Tautulli-style activity and transcode policy (Jellystat is paused), and access rules per media version. Design those in from the first commit.

## 3. Target environment (verified 2026-10-30)

**Server host: nix1**, on the home LAN (its address lives in the private workspace, not this public repo).

| Fact | Value |
|---|---|
| OS | Ubuntu 24.04.3 LTS, x86-64 |
| Docker Compose | 5.0.1 |
| CPU and GPU | Intel i7-8700, "CoffeeLake-S GT2 [UHD Graphics 630]" |
| Render node | `/dev/dri/renderD128`, group `render`, GID **993** (`card0` is group `video`, GID 44) |
| Media mount on host | `/mnt/media`, an autofs CIFS mount of the Synology's media share, SMB 3.1.1, mounted **read-write** on the host |
| Top folders under `/mnt/media` | `Movies`, `TV`, `Concerts`, `Music`, plus `AppData`, `Downloads`, `Pre-Roll`, `_backups` and Minecraft folders. Configure libraries per folder; never hardcode these. |
| Plex | container `plex` (`lscr.io/linuxserver/plex`), host network, port 32400. Do not touch. |
| Kometa | container `kometa`, runs 00:00, 06:00, 12:00, 18:00 ET against Plex. Do not touch. |
| nix1 cron to stay clear of | 04:30 plex-split-keeper, 04:45 plex-update (skips when anyone streams) |
| Ports already listening | 22 53 443 3000 3001 3100 3680 3690 3691 3694 to 3742 (most of that range), 4000 4566 5201 5433 to 5436 5678 8080 8082 to 8084 8443 8686 8765 8888 9090 9093 9100 10000 11437 18201 18202 20241 to 20245 32400 32401 32600 and some high ports |
| Proposed VCRX port | **3750** (free on 2026-10-30). Run `ss -tlnp` on nix1 before binding; make it configurable. |
| Container auto-update policy | read `Mine/Self-Hosted/directives/update-policy.md` before deciding VCRX's Watchtower label |
| Deploy conventions for nix1 apps | `.claude/commands/link.md` (Link owns webapps on nix1) |

In the VCRX compose file, mount media as `/mnt/media/<folder>:/media/<folder>:ro`, keep `/config` and `/cache` as separate writable volumes, and pass the GPU with `devices: [/dev/dri/renderD128]` and `group_add: ["993"]`. Run as a non-root user and drop capabilities.

**The *arr apps** run as native packages on the Synology: Radarr, Sonarr and SABnzbd. They write files on the NAS, so nix1 never sees those writes as local filesystem events.

**Library scale:** Movies 375, TV Shows 206, Music 22, Concerts 5 (Plex counts, 2026-10-30). Design for 10x that, not 1000x.

**Test TV:** not yet chosen; ask Pete which Google TV device. His cleanyourtv project already documents the wireless-ADB handshake: Developer options, then Wireless debugging, then "Pair device with pairing code", then `adb pair <ip>:<pairing port> <code>` and `adb connect <ip>:<port>` (Mine/Self-Hosted/cleanyourtv/examples/benq-gv32/README.md).

## 4. Development environment (this NixOS box)

- On 2026-10-30, `go`, `java`, `gradle`, `adb` and `docker` were all absent from PATH here. `nix-shell` at the repo root provides the ones the build uses from `shell.nix`; Java and Gradle stay out because the APK builds in a container. System-wide installs are Dozer's lane; ask rather than editing `~/nixos-config`.
- The cgo SQLite driver needs gcc, which on this box also means nix-shell. That is one more reason to use the pure-Go driver (section 5).
- The Android SDK is awkward on NixOS. Pick one path and document it in the README: nixpkgs `androidenv`, or a pinned Android build container run on nix1 (which has Docker). Either way the APK build must be reproducible from a clean checkout with one command.
- `adb` can run from either machine; the TV only needs to be on the LAN.

## 5. Architecture and data model

**Shape:** a Go modular monolith, one binary, one container. Suggested packages: `config`, `db` (embedded, forward-only migrations), `library` (walk, parse, incremental diff), `probe` (ffprobe), `metadata` (nfo, filename, tmdb), `artwork` (fetch, cache, overlays), `auth`, `policy`, `playback` (decision, direct, hls), `transcode` (ffmpeg job manager), `activity` (events, stats, notify), `collections` (yaml, rules, lists), `api` (HTTP, OpenAPI).

**SQLite** with `modernc.org/sqlite` (pure Go, static builds). Its docs say CPU-heavy queries run 1.3x to 2.0x slower than C SQLite, which is irrelevant at this scale. Required settings:
- WAL mode, a busy timeout, foreign keys on.
- `SetMaxOpenConns` bounded; the docs warn the default pool is unbounded.
- Turn on the package's opt-in, Linux-only OFD lock mode. Its docs say it "stops an unrelated os.File close anywhere in the process from silently stripping SQLite's transaction locks". A scanner opens and closes files constantly.
- Pin `modernc.org/libc` to exactly the version the sqlite module pins.

**Core tables** (names indicative):
- `libraries` (type movie or show, root path, scan settings)
- `items` (movie, show, season, episode; parent links; sort title; year; metadata JSON; ratings per source)
- `media_versions` (one row per file: path, size, mtime, container, duration, video codec, profile, bit depth, HDR type, resolution, bitrate; child rows for audio and subtitle streams, including sidecar subtitle files)
- `labels` and `item_labels` (hidden-capable)
- `users` (case-insensitive unique usernames from day one: Jellyfin 12.0's migration fails on usernames that differ only by case), `sessions`, `library_grants`, `user_policies`
- `watch_state` (per user, per item, plus the version last watched)
- `playback_sessions` and `playback_events` (section 8)
- `collections`, `collection_items` (a cache rebuilt from YAML, section 9)
- `artwork` (cache index), `scan_runs`, `settings`

**Versions are first-class.** One title with a 4K and a 1080p file is one item with two `media_versions`. Group automatically only when files share a folder and filename prefix (Jellyfin's documented rule), and allow manual merge and split. Policies apply to versions, so a merge can never leak a 4K file to a friend or hide a 1080p one. This retires `split_keeper.py`.

**API:** REST under `/api/v1`, OpenAPI 3.1 committed in the repo, and a CI test that fails when routes and spec drift. Version the API from the first route: Jellyfin 12.0 broke old clients by removing its legacy `/emby/` paths. Also `/healthz` (liveness) and `/readyz` (database and media mounts reachable).

**Config:** environment variables plus an optional YAML file in `/config`. Secrets (TMDb key, webhook URLs, session key) come from env or a file path, never the repo. Ship `.env.example`.

**Scanning:** incremental on path, size and mtime. Inotify on a CIFS mount does not see writes made by another machine (general practice), and the *arr apps write on the NAS, so use periodic scans plus webhook-triggered targeted scans (section 11). A missing mount must fail the scan loudly, never mark the library empty and delete rows.

## 6. Playback, HLS and hardware acceleration

**Decision ladder**, recorded with its reason on every playback (section 8):
1. **Direct play:** the client can decode container, video and audio. Serve the file over HTTP with Range, HEAD and If-Range support.
2. **Remux** ("direct stream"): the container is the only problem. Copy streams into fMP4 HLS. Jellyfin calls this "the least intensive process".
3. **Audio transcode only:** the video is fine and the audio is not.
4. **Full video transcode:** last resort.

**Google TV client facts (Media3 docs):**
- ExoPlayer plays MP4, Matroska, WebM and MPEG-TS progressively, so most of the library should direct play.
- Video and audio decoding uses the device's platform decoders, so the client must send its capabilities with each play request: codecs, profiles, levels, HDR types, maximum resolution, audio passthrough. Build the list from `MediaCodecList` on the device; never assume.
- Bundle Media3's FFmpeg decoder extension for audio. It decodes AC-3, E-AC-3, DTS, DTS-HD and TrueHD in software, which should remove most audio transcodes. (Medium confidence; verify on the real TV.)
- Sidecar WebVTT, TTML, SubRip and SSA/ASS subtitle files are supported. Send them as side-loaded tracks; no burn-in needed.
- HLS works with both MPEG-TS and fMP4/CMAF segments. Use fMP4 for compatibility with the later web client.

**HLS transcoding:**
- One session per playback, temp dir under `/cache/transcode/<session>`.
- A job manager that owns every FFmpeg process: concurrency cap from config, kill on stop, client timeout or disconnect, kill the whole process group, delete the temp dir, and sweep orphans on startup.
- Seeking past what is transcoded restarts the job at the new position.
- Burn in image subtitles (PGS) only when the user picks them; Jellyfin calls burn-in "the most intensive method of transcoding".

**Hardware acceleration on nix1 (later, optional):**
- Build the hardware path on **VA-API** through Intel's iHD driver, behind an interface, and add QSV as an optional faster backend. Jellyfin's Intel docs currently prefer QSV on mainstream GPUs like the UHD 630, but warn that Ice Lake "and older generations are losing support for QSV on Linux" and "may stop working in a few years". Coffee Lake is older than Ice Lake.
- Install `intel-media-va-driver-non-free` (Intel's Full Feature build) in the image, not `intel-media-va-driver` (Free Kernel build). Intel's table for the KBLx family, which includes Coffee Lake, lists HEVC 10-bit as decode plus hardware-and-shader encode in the Full Feature build and decode only in the Free Kernel build.
- So HEVC 8-bit and 10-bit decode, and encode with the Full Feature driver, are hardware-supported on nix1.
- AV1 has no hardware decoder before Gen 12, so AV1 sources on nix1 decode in software. Budget CPU accordingly.
- HDR to SDR tone mapping is hardware-capable on any Intel GPU with 10-bit HEVC decode. The OpenCL route may need a hand-installed runtime tied to LLVM 14; the VA-API route avoids that.
- Intel iGPUs have no concurrent-session cap, unlike NVENC, so the cap comes from the server's own config.
- Vanilla FFmpeg needs those Intel media driver packages installed in the image (the jellyfin-ffmpeg fork bundles them, which is off limits here). Probe for `/dev/dri/renderD128` at startup and fall back to software cleanly when it is absent.

## 7. Metadata and artwork

Order of precedence, all cached in the database and `/cache/artwork`, never beside the media:
1. **Kodi-style NFO files** next to the media. Kodi reads them before any scraper and has a "Local Info Only" mode. Read them; never write them.
2. **Filename and folder parsing**: title, year, edition, season and episode numbers, multi-episode files, and resolution tags. Unit-test the parser against a large fixture list of real names.
3. **TMDb, optional.** The user supplies their own key (TMDb issues them from account settings). Free for non-commercial use with attribution. Show this exact notice and the TMDb logo in the About screen: "This product uses the TMDB API but is not endorsed or certified by TMDB." Cache responses and respect their limits. With no key or no network, everything still works from steps 1 and 2.

**No TheTVDB.** It needs a negotiated contract or a $12 a year subscription per user, which fails "no subscriptions". TMDb covers TV.

Posters and backdrops: local artwork files (`poster.jpg` and similar) first, TMDb second, a generated placeholder last. Serve resized variants from cache.

## 8. Built-in Activity (Tautulli parity)

Pete runs Tautulli on the NAS today. VCRX replaces it natively.

**From the first milestone:** write `playback_sessions` and `playback_events` (start, pause, resume, seek, stop, error, progress heartbeat) with user, device name and model, client version, IP, item, version, decision and reason, bitrate, and positions. History cannot be backfilled, so this exists before any Activity UI does.

**Activity features, after the acceptance test:**
- Live sessions: who, what, which version, device, direct play, remux or transcode and why, bitrate, hardware acceleration on or off, progress.
- History with search and filters by user, item, device and date.
- Stats: most watched, most active users, platforms, peak concurrent streams, plays by day of week and hour, library breakdown by codec, resolution, HDR and size.
- **Playback policy evaluated before a stream starts.** Example rule: non-admins may not transcode 4K versions; refuse with a message shown on the TV. This replaces Tautulli plus JBOPS `kill_stream.py` plus Plex Pass (research-reports/tautulli-stop-4k-plex-transcodes.md). An admin "stop session with message" action covers the rest.
- Notifications by webhook: Discord, ntfy and generic JSON, on playback start and stop, transcode decision, recently added, scan finished, and server health. Pete already posts to Discord from `split_keeper.py`.
- Privacy: IP and device retention setting; nothing leaves the box.
- Later: a year-in-review page (what Wrapperr builds from Tautulli data).

Activity screens can live in the TV app's admin area first, since the MVP has no web app. Everything must also be exposed through the API.

## 9. Built-in Collections (Kometa parity)

Pete's Kometa config, read 2026-10-30, is the spec. Kometa keeps definitions outside the server so customizations are not lost "in the event of a media server database loss"; VCRX keeps that property by loading YAML from `/config/collections/` and treating database rows as a rebuildable cache.

**What Pete uses today:**
- Movies: collections from imdb, tmdb, director, studio, genre, decade, oscars, franchise, universe, streaming and seasonal defaults (about 160 collections, including empty "separator" headers VCRX does not need); an IMDb audience-rating overlay at top left; a 4K-only resolution badge at bottom right; IMDb audience ratings written over Plex's.
- TV: imdb, tmdb, network, streaming, genre, decade and emmy collections; a ribbon overlay.
- A default playlist file.
- Two custom smart-filter collections that stamp hidden labels, which control what friends see:

```yaml
# VCRX equivalent of Kometa's 4k-labels.yml and horror-labels.yml
collections:
  - name: 4K Resolution
    library: Movies
    smart:
      all:
        - resolution: 4K
    label: 4K
    visible: { library: false, home: false, shared: false }
  - name: R-Rated Horror
    library: Movies
    smart:
      all:
        - genre: Horror
        - content_rating: R
    label: horror
    visible: { library: false, home: false, shared: false }
```

**Split by data source, because offline is a hard rule:**
- **Offline, from local metadata:** smart collections (genre, decade, director, studio, resolution, content rating, label, added date, AND and OR rules), plus automatic genre, decade, director and studio collections. Recompute after every scan.
- **Offline once cached:** franchise and universe collections from TMDb collection IDs stored with each item.
- **Online, optional:** IMDb Top 250 and Popular, TMDb Popular, Top Rated and Trending, Oscars and Emmys, streaming-service and seasonal collections. Refresh on a schedule (Pete's Kometa runs 4 times a day), keep the last good result when offline, and never fail a scan because a list is unreachable. Kometa itself refuses to run without a TMDb key; VCRX must not inherit that.

**Overlays:** render them in VCRX's artwork pipeline when serving a poster (resolution badge, rating, ribbon), cache the result keyed on the inputs, and never modify original artwork. Overlay layouts are config, like collections.

**Ratings:** store IMDb and TMDb ratings separately per item; the display source is a setting. That replaces `mass_audience_rating_update`.

**Playlists:** definable in the same YAML.

## 10. Access control

Pete shares Plex with friends and uses hidden labels and content-rating ceilings to limit what they see. VCRX must cover that before friends could move over.

- Roles: admin and user. A new user sees nothing until granted a library (safe default).
- Per user: library grants, label allow and deny lists, content-rating ceiling, maximum version resolution (friends see the 1080p version, never the 4K one), and transcode policy (section 8).
- Policies filter every query and every stream URL server-side; the client is never trusted to hide things.
- Passwords hashed with a modern KDF (argon2id or bcrypt), sessions as random opaque tokens stored hashed, login rate limiting, and the TV client stores its token in encrypted storage.
- All file access goes through version IDs, never client-supplied paths. Resolve and confirm every path stays under a configured library root (guards against path traversal and symlink escapes).
- Admin endpoints are LAN-only by default. No router port forward, ever, by default. A media server exposed straight to the internet is a standing target for remote exploits.
- No dependency on any cloud for auth or sharing. The plex.tv friends API returned HTTP 410 on 2026-10-30, which is exactly the failure VCRX avoids.

## 11. Ecosystem integration

- **Radarr and Sonarr:** accept their "on import" webhook and run a targeted scan of that path. Map NAS paths to container paths in config. Polling remains the fallback.
- **Bazarr:** keep it external. It works through Sonarr and Radarr, not the media server. VCRX reads whatever sidecar subtitle files sit beside the media.
- **media-reclaim.py:** today it reads watch state from Plex. Expose per-user watch state and last-played through the API so it can move to VCRX later.
- **Requests:** Seerr supports Plex, Jellyfin or Emby, one at a time, and nothing else. Overseerr stays on Plex for now; requests are not in scope.
- **Maintainerr:** not needed; media-reclaim covers it.
- **A VCRX MCP server** for Claude sessions is a later idea and Forge's lane, not this build's.

## 12. Lessons from Jellyfin and Plex

- Version the API and the database together, and make migrations incremental and resumable. Jellyfin 10.11's migrations could take "several hours", and 12.0 added `--mode MigrateSystem` to run them deliberately. Give VCRX an equivalent migrate-only command early.
- Back up the database before every migration, automatically.
- Do not ship a plugin ABI before the core API is stable. Every Jellyfin 10.11 third-party plugin broke on 12.0.
- Plex merges versions in ways Pete has to undo nightly; Jellyfin had to clear its automatic groupings in 12.0. Keep grouping rules simple, explicit and reversible.
- Plex's 2025 roadmap promised "an open and documented API", and its July 2026 roadmap still lists NFO support as coming. VCRX ships both from the start.

## 13. Repo, licence, CI and workspace rules

- **Location:** its own repo at `~/vcrx`, outside the workspace, like Dewfront at `~/dewfront`. Not a submodule.
- **Layout:** `server/` (Go), `tv/` (Android, Kotlin), `deploy/` (compose, env examples), `docs/` (copy this file to `docs/REFERENCE.md` in the first commit).
- **Licence:** AGPL-3.0-or-later is the default here, so anyone running a modified VCRX as a service shares changes. Pete can override it; settle it before the first public push.
- **GitHub:** ask Pete public or private **before creating the remote**. Recommendation: public. GitHub's billing docs say "Standard GitHub-hosted runners in public repositories" are free, while private CI draws on a 2,000-minute monthly pool that demand of about 2,400 minutes has already exhausted twice (memory: actions-minutes-trim-2026-10). An Android build in private CI would make that worse.
- **CI from the first change** (CLAUDE.md: a repo holding code that runs somewhere real gets its standing gate with the first change): `go vet`, `golangci-lint`, `go test -race ./...`, `govulncheck`, a secret scan (gitleaks), the OpenAPI drift test, and for `tv/` a Gradle lint, unit tests and `assembleDebug`. Use path filters so server changes skip the Android job, cache Go modules and Gradle, and set `concurrency` with cancel-in-progress.
- **Tests that matter most:** the filename parser fixtures, path-traversal and symlink-escape attempts, policy filtering (a friend cannot list or stream a 4K version), Range request edge cases, FFmpeg job cleanup on cancel, and migrations on a copy of a real database.
- **Workspace rules that apply:** no em dashes and no spaced hyphens in docs and commit messages; commits co-authored with Claude; verify staged files in a separate command before committing; `git fetch` and check divergence before work; the `verify` skill before calling code done; prove "it works" with raw command output and test steps on the real TV, never by assertion.

## 14. Milestones

1. **Server slice:** compose up on nix1, config, migrations, a read-only library, scan with ffprobe, filename metadata, local accounts, `/api/v1` with OpenAPI, a direct-play endpoint with Range, the playback event log, health checks.
2. **TV slice:** Compose for TV app, connect by IP, login, home with Continue Watching and Recently Added, movie and show browsing, details, season and episode picker, search, Media3 direct play, seek, stop, resume, progress sync, error and disconnected states, APK build and ADB sideload steps. **The acceptance test passes here.**
3. **Playback breadth:** capability-based decisions, remux and HLS fallback, audio and subtitle selection, sidecar subtitles, burn-in for image subtitles.
4. **Activity** (section 8).
5. **Collections and overlays** (section 9), with optional TMDb enrichment.
6. **Access policies** (section 10) and VA-API hardware transcoding (section 6).
7. **Ecosystem hooks** (section 11).
8. **Later:** PWA web client, Android phone, Apple TV (native: WKWebView does not list tvOS), Roku (BrightScript and SceneGraph; Roku decodes HEVC only on 4K models), remote access through a reverse proxy, intro and credits detection from local audio fingerprints, a Concerts library type with local-only metadata.

**Acceptance test (Pete's):** on Ubuntu, `docker compose up`, configure a read-only NAS mount, scan a real media folder, build and sideload the APK on Google TV, connect over LAN, browse movies and shows, play a supported file, seek, stop, and resume from the saved position. Plex keeps running untouched the whole time.

## 15. Out of scope for the MVP

Web app, iOS, app-store releases, music libraries (Lidarr stays on Plex), Live TV and DVR, requests, plugins, remote access, multi-server, any cloud service, any telemetry.

## 16. Open decisions for Pete

1. Which Google TV device is the acceptance-test target.
2. Public or private GitHub repo (recommendation: public, section 13).
3. Licence (default AGPL-3.0-or-later).
4. The name: a 2026-10-30 web search found no product called exactly "VCRX Media Server", but "VCRX" is one letter from "Plex" in the same category. Worth a trademark check before any public launch.
5. Port 3750, or another.
