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
| Browser playback | Browsers decode H.264, HEVC (Chrome on a Mac) and AV1 but not AC-3, E-AC-3, DTS or TrueHD, which are most first audio tracks on nix1. So the server copies video and converts only audio (#8) where it can, and transcodes the video to H.264 only when the browser cannot decode it at all. |
| Overlays | Baked into the served poster (like Kometa into Plex), so every client gets them unchanged. VCRX lettering, not third-party logos. Kometa's ribbon lists (IMDb Top 250, RT) have no source here; the ribbon is an IMDb rating and votes rule. |
| Trailers | Offline first (2026-10-31): local extras in Plex's layout (`-trailer` files, `Trailers` and the other extras folders, a show's `theme.mp3`) attach to their title and play through the same decision and access rules as a version, without touching watch state. YouTube trailers are opt-in (`trailers_youtube`, needs a TMDb key): TMDb's best official trailer, embedded from youtube-nocookie.com on the web (the only frame the CSP then allows) and opened in the YouTube app on the TV. |

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
3. **Playback breadth:** converted streams shipped 2026-10-30 for clients that ask (`fmp4_stream`): video copied, audio copied or converted to AAC, fragmented MP4, seek by keyframe (#8). Text subtitles as WebVTT, sidecar and embedded (#4). Subtitle search and download from OpenSubtitles (opt-in, with an automatic fetch for new files) and a per-user subtitle delay (2026-10-31). Video transcoding in the same converted streams added 2026-10-30 for `fmp4_stream` clients that decode H.264: libx264 veryfast CRF 21 with a bitrate cap, scaled down to the client's size and never up, a keyframe every 2 s, exact-position seeks, HDR tone mapped with zscale when the client reports no HDR; at most `VCRX_MAX_TRANSCODES` (default 2) at once; on only when ffmpeg has libx264. The TV app sends `fmp4_stream` since 2026-10-31 (#31) and plays these streams, seeking through the seek endpoint. Safari added 2026-10-31 (`hls_stream`): the same conversions written by ffmpeg's HLS muxer as an EVENT playlist of fMP4 segments per session, seek by starting a new stream as for fMP4, disk bounded by pausing ffmpeg and deleting played segments, files removed at session end, idle and startup. Manual version merge and split shipped 2026-10-31. Still to come: video transcoding for the TV app (HLS), image subtitle burn-in. Skip Intro and Skip Credits shipped 2026-10-31: intros and credits found from each season's audio (own fingerprint, no chromaprint) at night at low priority, chapter and Plex-style NFO markers, an admin editor, a per-user button, automatic or off setting, web and TV.
4. **Activity:** shipped 2026-10-30 (#5): now playing, history, stats, per-user totals, stop a stream with a message, webhooks (Discord-shaped for Discord URLs). Playback policy before stream start already existed. Added 2026-10-31: a weekly recently-added newsletter through the webhooks, Tautulli's stats views (direct play against converted, platforms, most at once, a table under every chart), and addresses labelled by network (LAN, Tailscale, this server, internet). Still to come: placing public addresses on a map, which needs an offline geolocation database.
5. **Collections and overlays:** poster overlays shipped 2026-10-30 (#7): rating, resolution with HDR and DV, ribbon, drawn into the served poster like Kometa; IMDb ratings from IMDb's public file (opt-in). TMDb enrichment shipped earlier. Franchise collections shipped 2026-10-30 (#13): built from `tmdb_collection_id`, names from TMDb, rebuilt every 30 minutes; genre and decade are library filters rather than collections. Fix Match (pick the TMDb title by hand) shipped 2026-10-31; the TV app's collection screen is #15. Matching versions by TMDb id shipped 2026-10-31. Smart collections from YAML rules, which can stamp hidden labels, shipped 2026-10-31 (#33). Still to come: director and studio collections.
6. **Access policies and VA-API** hardware transcoding. Access policies shipped 2026-10-31: content-rating ceilings, label allow and deny lists, a Settings > Users editor with a plain-English summary per user, login limits per address and per account with separate LAN and internet counts, and `server/internal/api/access_test.go`, which calls every route as a restricted friend and fails when a new route skips the policy. Still to come: VA-API.
7. **Ecosystem hooks:** Radarr and Sonarr import webhooks shipped 2026-10-31 (targeted scans). Watch state for media-reclaim built 2026-10-30 (not yet deployed): `GET /api/v1/admin/watch/export` and `vcrx watch export` list every movie and show with its file paths, size, added and last played times across all users, play count, who finished it, and episode counts for shows, filtered by library and by days unplayed. Per-title history (Tautulli's item page) came with it: `GET /api/v1/admin/items/{id}/history` and a History section on details pages for admins. Still to come: pointing the reclaim tool at the export instead of Plex.
8. **Web app** (pulled forward from "Comes later", 2026-10-30, #6, #10, #11): browse, details, search, browser playback with subtitles and Up Next, Activity, Settings for libraries and users, sign-in without a password on trusted networks (#3) by cookie with a CSRF header rule. Plain ES modules embedded in the binary, no build step. Gates: `server/web/check-js.sh` and `scripts/web-smoke.mjs` (headless Chrome) in CI.

9. **Friend network: your movies, no accounts** (Pete, 2026-10-31). What sets VCRX apart from Plex and Jellyfin: token auth with no login, and libraries shared between friends' servers, "think torrent". In priority order:
   1. **The invite token is the account.** The owner mints a link or QR code; a device redeems it with no username, password or email. The token carries its scope: libraries, maximum resolution, whether converting is allowed, and an expiry. Each device can be revoked on its own. A TV pairs by QR (the TV shows a code, a phone scans it), entirely on the local network, with no central service. **Built 2026-10-31** (`server/internal/invites`, migration 0011): each invited device is its own principal (a users row with `device_token = 1`, never admin, never in the household) whose scope lives in `library_grants` and `user_policies`, so the one policy path enforces it; the access walk runs as a user, a profile and a token. Invites are 160 random bits stored as SHA-256, shown once, single-use; expiry and revocation are checked on every request, and revoking (or expiry, within a minute) cuts open streams through the stream registry. TV pairing is the RFC 8628 device-code flow: the TV gets a ten-minute, single-use XXXX-XXXX code and a QR code of `#/pair/<code>`, an admin's phone on the LAN approves it with a scope, and the TV's poll collects its session once. Invited devices reach a file only inside one of their own playback sessions (`TestNoTokenRouteServesAWholeFile` walks every route for whole-file responses). Settings > Devices mints, lists and revokes; Activity tags an invited device's stream and revokes it.
   2. **Attenuable delegation** (macaroon-style caveats): a holder can mint a narrower token for another device, never a wider one, with no central server.
   3. **Federated libraries.** Each friend runs their own VCRX; servers link by exchanging tokens, and Home merges the libraries with a source badge ("on Mark's server, 4K"). Per-copy policy (1080p not 4K, a per-friend bandwidth cap) applies to a federated peer exactly as to a local user.
   4. **Multi-source streaming** (the torrent idea): when several peers hold the same title (TMDb id match), play from the fastest; later, fetch HLS segments from several peers in parallel.
   5. **Direct peer connectivity** with NAT traversal (WireGuard or Tailscale style, or WebRTC), with no vendor relay.

   Build order: 1, 3 and 5 first (that is the pitch), then 2, then 4. Every token and federation change gets a security review covering scope enforcement, replay, revocation, and the DNS-rebinding Host check on peer routes.

   **Design constraint** (recommended to Pete by the product session on 2026-10-31; he has not objected, and has not signed off on relaxing it): stream to invited peers, NEVER replicate or copy files between servers. Torrent is the transport model, not file sync. File copying between friends turns the public AGPL repo into a distribution tool (Popcorn Time). Offline download only to the requesting user's own devices.

   The first change that adds a peer-facing route also adds the check that enforces this: a test over the registered routes that fails if any peer-facing route serves a whole file for storage (a full-file response, a download disposition, or a range request covering the whole file outside a playback session), with a control that proves the test fails when such a route is added.

10. **The video store you and your friends own** (Pete, 2026-10-31: "take it to the next level, be bold, never quit"). Plex imitates Netflix; VCRX imitates the video store and the tape-trading scene, which streaming services cannot do because they have no friends. In priority order:
    1. **Lend a tape.** A VHS-style card with a QR code: an invite token (milestone 9 item 1) scoped to ONE title for 72 hours that expires on its own. One loan per copy: while it is out, nobody else can stream the owner's copy. The expiry screen says "Be kind, rewind."
    2. **Legal tape trading.** A title may be marked shareable ONLY from a verifiable source: the band's taping policy (etree or Internet Archive Live Music Archive metadata), a Creative Commons or public-domain licence, or "my own recording" set by an admin for files in a home-video library. Shareable titles may replicate between peers, torrent style; everything else stays stream-only. This is the ONLY exception to milestone 9's no-copy line, never a free-form toggle on arbitrary titles, and milestone 9's route test must enforce it. **Status: needs Pete's direct sign-off before any design work,** because it relaxes milestone 9's no-copy constraint and arrived through a relay. Open question for that review: "my own recording" must not become a free-form toggle in practice by moving any file into a home-video library, so it needs its own provenance check. The shareable-provenance model is designed and security-reviewed BEFORE any replication code. Pete approved starting design work on 2026-10-31; the draft is `docs/design/tape-trading.md`, and replication code still waits on its security review and his sign-off.
    3. **Jam band mode.** Concert videos get per-song chapters from setlist data; "play every Tweezer" works across the friend network.
    4. **The video store.** Home as shelves: a new-release wall from what was recently added across friends' servers, a Staff Picks shelf per friend, and late fees as a joke.
    5. **Friend channels.** Scheduled linear channels per server ("Mark's 90s Action"), flipped through like cable, everyone tuned in at the same moment.
    6. **"Who has it?"** Ask the network for a title; a friend who owns it answers with one tap.
    7. **Taste signals,** opt-in and off by default per friend, from friends' watch history ("3 finished, 2 bailed at minute 40").
    8. **Federated skip-intro and credits markers:** computed once and shared between peers as metadata, never media.
    9. **A built-in MCP endpoint** so Claude can manage the library (collections, profiles, labels), with an admin-scoped token, read-only by default.
    10. **A built-in-public page:** every PR explained in plain English, live.

    Build order: 1, 8, 4, then 2 (only after its sign-off and review), then the rest.

### Merge order for the 2026-10-30 overnight stack

Server: #3, #4, #5, #6, #7, #8, #10, #11, #12, #13, each based on the one before; do not deploy #3 without #12. TV: #9, based on #3 only. After the stack is merged and deployed, add to nix1's `deploy/data/config/vcrx.yaml`: `auto_sign_in_user: pete`, `trusted_networks` naming the home LAN and TV subnets plus the Tailscale IPs of Pete's own devices (the exact list lives in the workspace, not this public repo), `imdb_ratings: true`, and the overlays from #7's description. The APK has to be signed on the NixOS box, which holds `tv/signing/`.

## Acceptance test

On nix1: `docker compose up -d`, two read-only library mounts, a scan of the real Movies and TV folders. Build and sideload the APK, connect over the LAN, browse movies and shows, direct play a file, seek, stop, and resume from the saved position. Plex keeps running untouched. Steps: `README.md`, "Acceptance test".
