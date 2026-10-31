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
| Sign-in at home | Pete, 2026-10-30: "on lan there should be no auth". `auto_sign_in_user` signs clients on `trusted_networks` in as that user, judged by the TCP peer, never a forwarded header from an untrusted proxy. Off by default, and no default networks (#12): on nix1 the Docker bridges use 172.16.0.0/12 and 192.168.16.0/20 to 192.168.224.0/20, and an Incus bridge in 10.0.0.0/8. The server answers only to IP addresses, localhost and `allowed_hosts` (DNS rebinding). |
| Web client | Served by the server itself on 3750; vanilla ES modules, no framework, no build step, nothing from the internet. Browser sessions are an HttpOnly SameSite=Strict cookie because media elements cannot send a bearer header; changes need the `X-VCRX-Client` header. |
| Browser playback | Browsers decode H.264, HEVC (Chrome on a Mac) and AV1 but not AC-3, E-AC-3, DTS or TrueHD, which are most first audio tracks on nix1. So the server copies video and converts only audio (#8) rather than transcoding video. |
| Overlays | Baked into the served poster (like Kometa into Plex), so every client gets them unchanged. VCRX lettering, not third-party logos. Kometa's ribbon lists (IMDb Top 250, RT) have no source here; the ribbon is an IMDb rating and votes rule. |

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
    metadata/          NFO reader
    tmdb/              TMDb client and the title and year match rule
    enrich/            fills items from TMDb without overwriting NFO, local or locked data
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

## Version grouping

The reference says to group files into one item only when they share a folder and a filename prefix. A library laid out as one release folder per file (`Some Film 2009 2160p HDR .../`, `Some_Film_2009_Extended_.../`) shows duplicates under that rule. Each library therefore has a `grouping` setting: `folder` (the reference rule, and the default for new installs) or `title_year` (also group files whose parsed title and year match). Pete switched his Movies library to `title_year` on 2026-10-30: 392 cards became 384 with no duplicate title and year left, and 4K and 1080p copies now sit as versions of one movie. Matching on TMDb ids in milestone 5 is the long-term answer; manual merge and split follow in milestone 3.

Both shipped 2026-10-31 (`server/internal/library/regroup.go`). Every movie file is filed under a key: its natural key from its names, unless a merge or split says otherwise. An item owns its `group_key` plus the keys merged into it (`items.extra.merged_keys`), and a split item's `group_key` is `p:<path>`, which pins that one file. Scans route a file by its pin, then by a merged key, then by its natural key, so they keep what an admin did. After every enrichment pass and every scan, movies in one library with the same TMDb id merge into the one an admin edited, else the oldest; split items never take part. Copies whose content ratings or labels (set by hand or by an NFO) differ are never merged, automatically or by hand, because access rules judge a movie by them; the automatic merge logs why it left them apart. A merge combines watch state per user (played if either was, counts added, the newest position), moves playback history and collections, holds the shared labels as set by hand (so a rewrite of the survivor's NFO cannot drop one the other copy's file still declares), and fills only missing ratings, tags and artwork; the survivor keeps its title and match. Each merge and split is one transaction and refuses while the library is being scanned. A renamed file (an upgrade with a new name) is a new file and follows its natural key again; switching a library's grouping mode undoes manual merges but not splits.

## Milestones

1. **Server slice** (this session): compose up on nix1, config, migrations, a read-only library, incremental scan with ffprobe, filename and NFO metadata, local artwork, local accounts, `/api/v1` with the OpenAPI drift test, direct play with Range, the playback event log, watch state, health checks, CI.
2. **TV slice** (this session): Compose for TV app, connect by IP, login, home rows, library grid, details, season and episode picker, search, Media3 direct play with capabilities, seek, stop, resume, progress sync, error and disconnected states, one-command APK build, ADB steps. **Acceptance test passes here.**
3. **Playback breadth:** converted streams shipped 2026-10-30 for clients that ask (`fmp4_stream`): video copied, audio copied or converted to AAC, fragmented MP4, seek by keyframe (#8). Text subtitles as WebVTT, sidecar and embedded (#4). Manual version merge and split shipped 2026-10-31. Still to come: video transcoding (HLS), image subtitle burn-in.
4. **Activity:** shipped 2026-10-30 (#5): now playing, history, stats, per-user totals, stop a stream with a message, webhooks (Discord-shaped for Discord URLs). Playback policy before stream start already existed. Still to come: IP to location, a newsletter.
5. **Collections and overlays:** poster overlays shipped 2026-10-30 (#7): rating, resolution with HDR and DV, ribbon, drawn into the served poster like Kometa; IMDb ratings from IMDb's public file (opt-in). TMDb enrichment shipped earlier. Franchise collections shipped 2026-10-30 (#13): built from `tmdb_collection_id`, names from TMDb, rebuilt every 30 minutes; genre and decade are library filters rather than collections. Fix Match (pick the TMDb title by hand) shipped 2026-10-31; the TV app's collection screen is #15. Matching versions by TMDb id shipped 2026-10-31. Still to come: YAML collection rules, director and studio collections.
6. **Access policies and VA-API** hardware transcoding.
7. **Ecosystem hooks:** Radarr and Sonarr import webhooks shipped 2026-10-31 (targeted scans). Watch state for media-reclaim built 2026-10-30 (not yet deployed): `GET /api/v1/admin/watch/export` and `vcrx watch export` list every movie and show with its file paths, size, added and last played times across all users, play count, who finished it, and episode counts for shows, filtered by library and by days unplayed. Per-title history (Tautulli's item page) came with it: `GET /api/v1/admin/items/{id}/history` and a History section on details pages for admins. Still to come: pointing the reclaim tool at the export instead of Plex.
8. **Web app** (pulled forward from "Comes later", 2026-10-30, #6, #10, #11): browse, details, search, browser playback with subtitles and Up Next, Activity, Settings for libraries and users, sign-in without a password on trusted networks (#3) by cookie with a CSRF header rule. Plain ES modules embedded in the binary, no build step. Gates: `server/web/check-js.sh` and `scripts/web-smoke.mjs` (headless Chrome) in CI.

### Merge order for the 2026-10-30 overnight stack

Server: #3, #4, #5, #6, #7, #8, #10, #11, #12, #13, each based on the one before; do not deploy #3 without #12. TV: #9, based on #3 only. After the stack is merged and deployed, add to nix1's `deploy/data/config/vcrx.yaml`: `auto_sign_in_user: pete`, `trusted_networks` naming the home LAN and TV subnets plus the Tailscale IPs of Pete's own devices (the exact list lives in the workspace, not this public repo), `imdb_ratings: true`, and the overlays from #7's description. The APK has to be signed on the NixOS box, which holds `tv/signing/`.

## Acceptance test

On nix1: `docker compose up -d`, two read-only library mounts, a scan of the real Movies and TV folders. Build and sideload the APK, connect over the LAN, browse movies and shows, direct play a file, seek, stop, and resume from the saved position. Plex keeps running untouched. Steps: `README.md`, "Acceptance test".
