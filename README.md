<p align="center"><img src="brand/generated/vcrx-wordmark-on-black.png" alt="VCRX" width="420"></p>

# VCRX Media Server

**Your media. Your hardware. Your rules.**

A self-hosted media server and a native Google TV app, built from scratch. No subscriptions, no ads, no telemetry, no vendor accounts and no required cloud service: it starts, scans and plays on a LAN with the internet unplugged. Your media, your server, your accounts, your data.

VCRX exists for the things other servers need add-ons and scripts for: per-version access rules (a friend sees the 1080p copy, never the 4K one), built-in activity history and playback policy, and collections defined in files you own. Those land milestone by milestone; see [docs/PLAN.md](docs/PLAN.md).

Licence: AGPL-3.0-or-later. If you run a modified VCRX as a service, share your changes.

## Status

Milestones 1 and 2 of [docs/PLAN.md](docs/PLAN.md): the server slice and the TV slice.

| Works now | Comes later |
|---|---|
| Movie and TV libraries, incremental scans with ffprobe, NFO and filename metadata, local artwork, TMDb enrichment (optional, your own key), IMDb ratings (optional), Kometa-style poster overlays, automatic franchise collections, genre and decade filters | Collections from YAML rules |
| Local accounts, per-library grants, a per-user resolution ceiling, label deny lists | Content-rating ceilings, playback policy UI |
| Direct play over HTTP with Range; converted streams for clients that ask for them (video copied, audio converted to AAC, fragmented MP4); sidecar and embedded text subtitles (also as WebVTT for browsers) | Video transcoding, picture subtitle burn-in, VA-API hardware acceleration |
| Every playback logged with its decision and reason, resume positions, Continue Watching. Activity API: now playing, history, stats, per-user totals, stop a stream, webhooks | Activity screens in a client |
| Web app at `/`: browse, details, search, play in the browser with subtitles, Activity screens. REST API under `/api/v1`, OpenAPI 3.1 spec checked against the routes in CI | Browser playback of files the browser cannot decode, other TV platforms |

A file the TV cannot play directly is refused with a message saying why ("needs video conversion: the device has no av1 video decoder"). Transcoding arrives in milestone 3.

## Run the server

Needs Docker with Compose on a Linux host that can read your media.

```bash
git clone https://github.com/pete-builds/vcrx-media-server.git
cd vcrx-media-server/deploy
cp .env.example .env                       # set VCRX_MOVIES_DIR and VCRX_TV_DIR
mkdir -p data/config data/cache
cp vcrx.example.yaml data/config/vcrx.yaml # declares the Movies and TV libraries
docker compose up -d --build
docker compose exec vcrx vcrx user add -username you -admin
docker compose logs -f vcrx                # watch the first scan
```

The container runs as `VCRX_UID:VCRX_GID` (1000 by default), which must be able to read the media and write `data/`. Media is mounted read-only. The server listens on port 3750 on the host's network.

Check it:

```bash
curl -s http://<server-ip>:3750/readyz     # {"status":"ok", "checks": {...}}
docker compose exec vcrx vcrx library list # item counts and the last scan
```

Update later with `deploy/deploy.sh`, run on the server from this clone. It refuses a checkout with local changes or commits origin does not have, refuses while someone is watching (checked as an admin whose password you keep in `deploy/.flexbot-password`; `--force` overrides), fast-forwards to `origin/main`, builds with the commit as the version, waits for the server and runs `scripts/smoke-api.sh`.

### More users

```bash
docker compose exec vcrx vcrx user add -username friend
docker compose exec vcrx vcrx user grant -username friend -libraries Movies,TV
```

A new user sees nothing until granted a library. Admins see everything. To keep a user off 4K versions, set their policy through the API:

```bash
curl -X PUT http://<server-ip>:3750/api/v1/admin/users/<id>/policy \
  -H "Authorization: Bearer <admin token>" -H "Content-Type: application/json" \
  -d '{"max_resolution": "1080p", "allow_transcode": true}'
```

### CLI

Inside the container (`docker compose exec vcrx vcrx ...`):

```
vcrx migrate                  back up the database, apply migrations, exit
vcrx user add|grant|passwd|list
vcrx library add|list|scan
vcrx metadata refresh [-all]  fetch posters and plots from TMDb now
vcrx metadata status          matched and unmatched counts, and why each miss was skipped
vcrx healthcheck              what the container HEALTHCHECK runs
```

`vcrx library scan -name Movies -path "Some Folder"` rescans one folder. A scan refuses to mark files missing when the library root is unreachable or suddenly empty, or when more than half the library would vanish at once; `-force` overrides the last two after you have checked the mount.

### Configuration

Environment variables (`deploy/.env.example`) and `/config/vcrx.yaml` (`deploy/vcrx.example.yaml`). The database, its automatic pre-migration backups and the settings file live in `/config`; resized artwork and other caches live in `/cache` and can be deleted at any time.

### TMDb posters and plots (optional)

Set `VCRX_TMDB_API_KEY` in `.env` to your own [TMDb](https://www.themoviedb.org/settings/api) API key or API Read Access Token, then `docker compose up -d`. Two minutes after startup, and every 15 minutes after that, the server fills new movies and shows with plot, tagline, genres, cast, studio or network, content rating, TMDb rating, poster and backdrop, and episodes with titles, plots and stills. The first pass over a large library takes a while; `vcrx metadata refresh` runs it in the foreground.

- **Your own data wins.** A value from an NFO, a local poster, or a field an admin locked is never replaced. Enrichment only writes a field that is empty or still holds what it wrote last time.
- **No guessing.** A movie or show is matched by the TMDb or IMDb id in its NFO, or else by title and year, and only when exactly one result has that title in that year (or one year off). Anything doubtful is left alone and listed by `vcrx metadata status` with the reason; a year in the folder name usually fixes it.
- Images are kept in `/config/metadata/tmdb`. `VCRX_TMDB_LANGUAGE` (default `en-US`) and `VCRX_TMDB_COUNTRY` (default `US`, for content ratings) are optional.

**Fix Match:** when matching found nothing or the wrong title, an admin opens the title in the web app, presses Fix match, searches TMDb and picks. The item fills from that title at once and later passes keep the choice. Settings, Libraries lists every unmatched title with the reason and a link straight to its Fix Match. As everywhere, values from an NFO or set by hand are kept.

This product uses the TMDB API but is not endorsed or certified by TMDB.

### Web app

Open `http://<server>:3750/` in a browser. It is part of the server: no separate install, no build step, nothing loaded from the internet.

- **Browse** Home (Continue Watching, recently added), each library with sort and filters, search, and details pages for movies, shows, seasons and episodes. Mark things watched or unwatched.
- **Play** in the browser, resuming where any device left off. The browser tells the server what it can decode. A file it cannot play directly because of its container or its audio (most often E-AC-3, AC-3 or DTS, which browsers do not decode) plays as a converted stream: the video is copied untouched and only the audio is converted to AAC, a fraction of one CPU core, with VCRX's own seek bar. Video the browser cannot decode at all is refused with the reason until full transcoding lands; the TV app still plays it. Subtitles: pick any text track, sidecar or inside the file.
- **Activity** for admins: now playing with a Stop button, history, stats and users.
- **Sign-in:** with `auto_sign_in_user` set, a browser on a trusted network is signed straight in; otherwise it shows a sign-in form. The session is an HttpOnly cookie, and every change the page makes carries a header a cross-site page cannot send.

### Poster overlays (Kometa style)

List `overlays` under a library in `vcrx.yaml` and the server draws badges onto that library's posters, the way Kometa does for Plex. Because the badges are part of the poster image, the TV app, the web app and anything else that shows posters get them with no change. `?overlays=0` on an artwork URL returns the clean poster.

| Type | Draws | Options |
|---|---|---|
| `rating` | A star and score with its source, top left by default | `sources`: preference order, default `[imdb, tmdb]` |
| `resolution` | The best version's resolution with an HDR, DV, DV HDR or HDR10+ tag, bottom right | `resolutions`: which get a badge, default `[4K]` |
| `ribbon` | A diagonal corner ribbon, default `TOP RATED` | `min_rating` (8.0), `min_votes` (25,000 for IMDb), `text` |

All three take `position`: `top_left`, `top_right`, `bottom_left` or `bottom_right`. Badges are VCRX's own lettering, not copies of third-party logos. Kometa's `ribbon` default draws on IMDb Top 250 and Rotten Tomatoes lists that VCRX has no source for, so VCRX's ribbon is a rule on the IMDb rating and vote count instead.

**IMDb ratings:** set `imdb_ratings: true` and the server downloads IMDb's public ratings file once a day (and only when it changed) and keeps the rating and vote count for every title with an IMDb id. NFO files and TMDb enrichment supply the ids. `vcrx metadata imdb` runs the sync now. IMDb licenses the file for personal, non-commercial use, so it is off by default.

### Collections

Every film series (a TMDb collection) with two or more of its films in a movie library becomes a collection there, in release order, like Kometa's `franchise` default. The membership is rebuilt every 30 minutes, so a new sequel joins on its own; a series that drops to one film goes away. Names come from TMDb once, so this needs `VCRX_TMDB_API_KEY`; without it, series wait rather than get a made-up name. `vcrx metadata collections` builds them now. The web app shows them under a library's Collections button and links each film's page to its series. Libraries also filter by genre and decade.

### Radarr and Sonarr

New imports can show up in seconds instead of at the next scan. Set `VCRX_ARR_WEBHOOK_TOKEN` in `.env` (16 characters or more), restart, then in Radarr and in Sonarr add Settings, Connect, Webhook:

- URL: `http://<server-ip>:3750/api/v1/hooks/arr`, method POST
- Password: the token (the username can be anything), or put `?token=<token>` on the URL
- Triggers: On Import, On Upgrade, On Rename, and the delete triggers

VCRX scans just that movie's or series' folder. It finds the folder by its name inside a library of the right kind; if Radarr or Sonarr see the NAS at a different path and the names differ, map the prefix with `arr_path_map` in `vcrx.yaml`. Webhooks are accepted only from the LAN. A Test from Radarr or Sonarr answers 200.

### Activity and notifications

Like Tautulli, without a second program. Every play request is logged with the device, address, version, decision and reason, and the admin API reads it back (admin account, from the LAN):

| Endpoint | What it gives |
|---|---|
| `GET /api/v1/admin/activity/now` | What is playing now: who, what, where, progress, direct play or not, bitrate, and the total bandwidth |
| `GET /api/v1/admin/activity/history` | Finished plays, newest first, by user or by title (a show includes its episodes) |
| `GET /api/v1/admin/activity/stats?days=30` | Plays and watch time, top movies, shows, users, clients, devices and platforms, plays by day, hour and weekday, direct play against converted, and the most streams at once |
| `GET /api/v1/admin/activity/users` | Every account with its totals and when and where it was last seen |
| `POST /api/v1/admin/activity/sessions/{id}/terminate` | Stop a stream now, with a message the viewer sees |

A play counts in stats once 30 seconds have actually played, and is watched at 90%. Days and hours follow the container's `TZ`. Each chart on the Stats page has a "Show as a table" view with the same numbers.

Addresses are labelled by the kind of network they are on: This server, Local network, Tailscale (100.64.0.0/10 and its IPv6 prefix) or Internet. That works offline; public addresses are not placed on a map.

**Webhooks:** set `VCRX_WEBHOOK_URLS` in `.env` to one or more URLs, comma separated. Each gets a JSON post for `play`, `stop`, `refused`, `terminated` and `added` (new titles after a scan, such as `＋ New in Movies: The Film (2024), Show (3 new episodes)`; narrow the events with `webhook_events` in `vcrx.yaml`). A Discord webhook URL gets a plain Discord message instead, such as `▶ pete started The Film on Living Room TV (direct play)`.

**Newsletter:** Settings > Newsletter sends a weekly digest of what was added in the last seven days (new movies, and new episodes grouped by show) to the same webhooks, on a weekday and hour you pick in the server's time zone. Discord gets a card per title with its TMDb poster (posters that exist only on disk have no public link), cut to fit Discord's limits with "and N more"; other URLs get the full list as JSON (`event: newsletter`). It is off by default, sends nothing in a week when nothing was added, and remembers the last week it sent, so a restart never sends it twice. "Send a preview now" posts the last seven days straight away.

## Run the TV app

Build the APK with one command and sideload it with ADB: [tv/README.md](tv/README.md). On the TV, enter `http://<server-ip>:3750` and sign in.

## Acceptance test

The MVP is done when this passes end to end on real hardware:

1. On the server: `docker compose up -d --build` with read-only media mounts, then `vcrx library list` shows both libraries scanned with nonzero item counts.
2. Build the APK (`tv/build-apk.sh`) and sideload it (`adb install -r tv/build/out/vcrx-tv-release.apk`).
3. On the TV: connect to `http://<server-ip>:3750`, sign in, see Recently Added on Home.
4. Browse Movies and TV, open a show, pick a season and an episode.
5. Play a file that direct plays. Seek forward. Press Back to stop.
6. Open the same item again: it offers Resume at the position you stopped at, and Home shows it under Continue Watching.
7. The activity log has the session: `GET /api/v1/admin/activity/sessions` lists the device, the decision (`direct_play`), its reason, and the positions.

Any other media server on the same host keeps running untouched; VCRX only reads the media folders.

## Security

- Optional password-free sign-in on your own networks: set `auto_sign_in_user` and `trusted_networks` in `vcrx.yaml` and clients on those networks are signed in as that user, each device with its own session. Off by default. There is no default network list on purpose: "every private range" on a server host also covers its Docker and Incus bridges, so a compromised container could sign in. List your LAN subnets and your Tailscale devices, and leave out loopback unless nothing on the host proxies to VCRX. Whoever is on those networks gets that user's access, admin included if the user is an admin.
- The server answers only to IP addresses, `localhost` and the names in `allowed_hosts`. That stops DNS rebinding, where a web page points its own domain at your server and calls it from your browser. Add a MagicDNS or local name there if you use one.
- Stopping a stream in Activity ends that session; it does not lock the user out. Disable the account to do that.
- Browsers scope cookies by host, not port: every other web app on the same host and address receives the web app's session cookie. Give VCRX its own host name if other apps there are not trusted.
- LAN-first. Never forward VCRX's port on your router. Admin routes answer only from loopback and private addresses unless `VCRX_ADMIN_ALLOW_REMOTE=true`.
- Passwords are hashed with argon2id. Tokens are random, stored only as SHA-256 hashes, and expire after 90 idle days. Failed logins are rate limited per address and per username.
- Every file is reached through a version id; paths are resolved with symlinks evaluated and must stay inside their library root, which must sit under `VCRX_MEDIA_ROOTS`.
- Remote access later: put VCRX behind a reverse proxy that terminates HTTPS (Caddy, nginx), add the proxy's address to `VCRX_TRUSTED_PROXIES` so client IPs are logged correctly, and keep admin routes LAN-only. Use a VPN rather than a port forward where you can.

## Privacy

VCRX sends nothing anywhere unless you add a TMDb key. No telemetry, no usage reports, no update checks. With a key, the server sends TMDb the titles and years it searches for and the TMDb ids it looks up, and downloads images from TMDb; it sends nothing about users or what they watch. Activity history (who played what, from which device and IP) stays in your database.

## Development

The server is Go 1.26 with a pure-Go SQLite driver; no cgo.

```bash
cd server
go test -race ./...      # includes the OpenAPI drift test
go vet ./...
golangci-lint run
govulncheck ./...
```

To work on the web app or the API against a few generated test clips: `./scripts/dev-server.sh`, then open http://127.0.0.1:3751/. It builds the clips with ffmpeg on first run, serves the web files from `server/web/static` so an edit shows on reload, and signs the local browser in. `server/web/check-js.sh` is the web app's compile check, and `node scripts/web-smoke.mjs` drives every page and both playback paths in headless Chrome against the dev server; CI runs both.

On NixOS, run `nix-shell` at the repo root first: [shell.nix](shell.nix) provides these tools, plus the Docker client and adb for the TV app. The API contract is [server/api/openapi.yaml](server/api/openapi.yaml), served at `/api/v1/openapi.yaml`; a test fails when it and the registered routes disagree. Migrations are forward-only SQL files in `server/internal/db/migrations`.

Design notes and the verified facts the build relies on: [docs/REFERENCE.md](docs/REFERENCE.md).

## Brand

The VCRX wordmark, standalone V mark, app icons and TV tiles live in [brand/](brand/README.md): VCRX Orange `#FF5F00` on Black, Rajdhani for display type and Inter for interface text. The kit is raster artwork, so the logo is always the image, never retyped in a font. `brand/tools/make-assets.py` derives the Android launcher icons, TV banners and in-app logo files from it; rerun it after changing the kit.
