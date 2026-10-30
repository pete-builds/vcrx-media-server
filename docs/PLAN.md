# VCRX Media Server: architecture and milestone plan

Written 2026-10-30. `docs/REFERENCE.md` holds the verified facts and governs wherever this plan is brief. `server/api/openapi.yaml` is the API contract between the server and every client.

## Decisions taken

| Question | Answer |
|---|---|
| Name | VCRX, written in capitals. Renamed from Flex on 2026-10-30: "Flex Media Server" was already in use by an unrelated Plex middleware project, and Flex sat one letter from Plex. Not yet cleared as a trademark. |
| Repo | `pete-builds/vcrx-media-server`, public. CLI `vcrx`, settings `VCRX_*`, TV app id `io.github.pete_builds.vcrx.tv` |
| Brand | VCRX Orange `#FF5F00` on Black, Rajdhani and Inter (`brand/`). Android assets are generated from the kit by `brand/tools/make-assets.py`. |
| Licence | AGPL-3.0-or-later |
| Port | 3750 on nix1, `VCRX_PORT` to change it |
| Test TV | Pete's TCL Google TV (model to be read off the device over ADB) |
| Networking | `network_mode: host`. nix1's Docker address pool ran out on 2026-10-30 and has only five free slots after a cleanup, so VCRX does not take one. Host mode also gives the server real client IPs for activity and the LAN-only admin check. |
| APK build | A pinned Android build image (`tv/Dockerfile`), run on any Docker host. From this NixOS box: `DOCKER_HOST=ssh://nix1`. Avoids the NixOS `aapt2` and SDK patching problems entirely. |
| FFmpeg audio extension | Google publishes no binary of `media3-decoder-ffmpeg`, so the build image compiles it from the androidx/media source at the same tag as the Media3 libraries, with vanilla FFmpeg. No third party builds. |
| Token storage on the TV | Android Keystore AES-GCM key encrypting the token in private preferences. `androidx.security:security-crypto` is deprecated, so it is not used. |

## Server shape

One Go binary, `vcrx`, one container. Subcommands: `serve` (default), `migrate` (apply migrations and exit, after an automatic backup), `user add|grant|list`, `library add|list|scan`. The CLI exists because the MVP has no web app: the admin account and the libraries are created with `docker compose exec vcrx vcrx ...`.

```
server/
  cmd/vcrx/            main, subcommands
  api/openapi.yaml     the contract, embedded and served at /api/v1/openapi.yaml
  internal/
    config/            env plus optional /config/vcrx.yaml
    db/                SQLite (modernc, WAL, OFD locks), embedded forward-only migrations
    library/           filename parser, walker, incremental diff, version grouping
    probe/             ffprobe wrapper
    metadata/          NFO reader (tmdb arrives in milestone 5)
    artwork/           local artwork discovery, resize cache, frame thumbnails
    auth/              argon2id passwords, opaque hashed tokens, login rate limit
    policy/            library grants and per-version visibility, used by every query
    playback/          decision ladder, direct play, session and event log
    api/               HTTP handlers, middleware, route list checked against the spec
```

Data model: `server/internal/db/migrations/0001_init.sql`. It already carries the tables for Activity (`playback_sessions`, `playback_events`), Collections (`collections`, `collection_items`, `labels`, `item_tags`, `item_ratings`) and access (`library_grants`, `user_policies`), so those features land later without a schema rewrite.

Safety rules built in from the first commit:
- Files are only ever reached through a version id. Every path is resolved with symlinks evaluated and must stay inside its library root, which must itself sit under `VCRX_MEDIA_ROOTS`.
- A scan of a root that is missing, unreadable or suddenly empty fails loudly and changes nothing. Files that vanish are marked missing and hidden, never deleted on the first miss.
- Every playback request writes a session row with the decision and its reason, refusals included.
- Admin routes answer only from loopback and private LAN ranges unless configured otherwise.

## Version grouping, an open question for Pete

The reference says to group files into one item only when they share a folder and a filename prefix. Pete's Movies folder is mostly one release folder per file (`Some Film 2009 2160p HDR .../`, `Some_Film_2009_Extended_.../`), so that rule alone will show duplicates. Each library therefore has a `grouping` setting: `folder` (the reference rule, default) or `title_year` (also group files whose parsed title and year match). Matching on TMDb ids in milestone 5 is the long-term answer. Manual merge and split follow in milestone 3.

## Milestones

1. **Server slice** (this session): compose up on nix1, config, migrations, a read-only library, incremental scan with ffprobe, filename and NFO metadata, local artwork, local accounts, `/api/v1` with the OpenAPI drift test, direct play with Range, the playback event log, watch state, health checks, CI.
2. **TV slice** (this session): Compose for TV app, connect by IP, login, home rows, library grid, details, season and episode picker, search, Media3 direct play with capabilities, seek, stop, resume, progress sync, error and disconnected states, one-command APK build, ADB steps. **Acceptance test passes here.**
3. **Playback breadth:** remux and HLS (fMP4) with an FFmpeg job manager, audio-only transcode, image subtitle burn-in, manual version merge and split.
4. **Activity:** live sessions, history, stats, playback policy before stream start (no 4K transcodes for non-admins), webhooks.
5. **Collections and overlays:** YAML in `/config/collections`, smart rules, auto collections, overlays, optional TMDb with the user's key.
6. **Access policies and VA-API** hardware transcoding.
7. **Ecosystem hooks:** Radarr and Sonarr import webhooks, watch state for media-reclaim.

## Acceptance test

On nix1: `docker compose up -d`, two read-only library mounts, a scan of the real Movies and TV folders. Build and sideload the APK, connect over the LAN, browse movies and shows, direct play a file, seek, stop, and resume from the saved position. Plex keeps running untouched. Steps: `README.md`, "Acceptance test".
