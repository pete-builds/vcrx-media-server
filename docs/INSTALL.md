# Installing and running VCRX

Everything about the server after the README's quick start: users and access, the CLI, configuration and backups, updating, and the optional outside services.

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

## More users

```bash
docker compose exec vcrx vcrx user add -username friend
docker compose exec vcrx vcrx user grant -username friend -libraries Movies,TV
```

A new user sees nothing until granted a library. Admins see everything. Set what each user sees in the web app under Settings > Users (from the local network): libraries, a highest resolution (a friend capped at 1080p never sees, streams or gets artwork from the 4K version), a highest content rating (PG-13 also allows TV-14; while one is set, titles with no rating are hidden), labels to hide or to require, and whether the server may convert streams for them. Each user gets a one-line summary such as "Sees Movies and TV, up to 1080p and PG-13, never titles labelled horror." The same settings are in the API:

```bash
curl -X PUT http://<server-ip>:3750/api/v1/admin/users/<id>/policy \
  -H "Authorization: Bearer <admin token>" -H "Content-Type: application/json" \
  -d '{"max_resolution": "1080p", "max_content_rating": "PG-13", "deny_labels": ["horror"], "allow_transcode": true}'
```

## CLI

Inside the container (`docker compose exec vcrx vcrx ...`):

```
vcrx migrate                  back up the database, apply migrations, exit
vcrx user add|grant|passwd|list
vcrx library add|list|scan
vcrx metadata refresh [-all]  fetch posters and plots from TMDb now
vcrx metadata status          matched and unmatched counts, and why each miss was skipped
vcrx watch export [-library NAME] [-unplayed-days N]
                              every movie and show with its files and watch state, as JSON lines
vcrx healthcheck              what the container HEALTHCHECK runs
```

`vcrx library scan -name Movies -path "Some Folder"` rescans one folder. A scan refuses to mark files missing when the library root is unreachable or suddenly empty, or when more than half the library would be missing, counting files earlier scans already marked missing (so a share that loses folders a scan at a time is caught too); `-force` overrides the last two after you have checked the mount.

## Configuration

Environment variables (`deploy/.env.example`) and `/config/vcrx.yaml` (`deploy/vcrx.example.yaml`). The database, its backups (before every migration, and a nightly snapshot kept for seven days, in `/config/backups`) and the settings file live in `/config`, as does `peer-token.key` once a friend's server is linked (see [Friends' servers](FEATURES.md#web-app): it is kept out of the database backups on purpose); resized artwork, Safari's HLS segments while they play, and other caches live in `/cache` and can be deleted at any time.

**Restoring a backup.** Each backup is a complete database. From `deploy/`, with `/config` being `${VCRX_DATA_DIR:-./data}/config` on the host:

1. `docker compose stop vcrx`.
2. Move `vcrx.db`, `vcrx.db-wal` and `vcrx.db-shm` out of `/config`. Never leave an old `-wal` or `-shm` beside a restored database: SQLite would replay it into the restored file.
3. Copy the backup you want (for example `backups/vcrx-nightly-20261031T201000Z.db`) to `/config/vcrx.db`.
4. `docker compose start vcrx`. A backup from before an upgrade is migrated forward at startup, and copied to `backups` first.

Anything written after the backup was taken is gone, so pick the newest one from before the problem. `TestRestoreDrill` in `server/internal/db` runs these steps against a backup taken at an older schema.

## Optional outside services

VCRX needs none of these. Each one is off until you give it a key or a token.

### TMDb posters and plots (optional)

Set `VCRX_TMDB_API_KEY` in `.env` to your own [TMDb](https://www.themoviedb.org/settings/api) API key or API Read Access Token, then `docker compose up -d`. Two minutes after startup, and every 15 minutes after that, the server fills new movies and shows with plot, tagline, genres, cast, studio or network, content rating, TMDb rating, poster and backdrop, and episodes with titles, plots and stills. The first pass over a large library takes a while; `vcrx metadata refresh` runs it in the foreground.

- **Your own data wins.** A value from an NFO, a local poster, or a field an admin locked is never replaced. Enrichment only writes a field that is empty or still holds what it wrote last time.
- **No guessing.** A movie or show is matched by the TMDb or IMDb id in its NFO, or else by title and year, and only when exactly one result has that title in that year (or one year off). Anything doubtful is left alone and listed by `vcrx metadata status` with the reason; a year in the folder name usually fixes it.
- Images are kept in `/config/metadata/tmdb`. `VCRX_TMDB_LANGUAGE` (default `en-US`) and `VCRX_TMDB_COUNTRY` (default `US`, for content ratings) are optional.

**Fix Match:** when matching found nothing or the wrong title, an admin opens the title in the web app, presses Fix match, searches TMDb and picks. The item fills from that title at once and later passes keep the choice. Settings, Libraries lists every unmatched title with the reason and a link straight to its Fix Match. As everywhere, values from an NFO or set by hand are kept.

This product uses the TMDB API but is not endorsed or certified by TMDB.

### Subtitles from OpenSubtitles (optional)

Set `VCRX_OPENSUBTITLES_API_KEY` in `.env` to a consumer key from [OpenSubtitles](https://www.opensubtitles.com/consumers) and the player's subtitle menu gets **Search for subtitles…**: it searches by the file's OpenSubtitles hash first (a subtitle made for that exact file), then by the IMDb or TMDb id (the show's, with season and episode, for an episode), then by title and year. Results list a hash match first, then trusted uploaders, then by download count, with badges for the language, a hash match and hearing-impaired. One click downloads a subtitle and switches to it.

- **Downloads** need `VCRX_OPENSUBTITLES_USERNAME` and `VCRX_OPENSUBTITLES_PASSWORD` too, because the daily quota belongs to an account (20 a day on a free one, shared by everyone on the server). Admins may download; for anyone else tick **May download subtitles** in Settings, Users. Anyone who can play a title can search and use subtitles already downloaded for it.
- **Where they go:** under `/config/subtitles/<version id>/`, never beside the media, which is mounted read-only. Each becomes a subtitle track every client lists, the TV app included, and scans leave it alone. An admin sees them on the movie or episode page with the quota left and can delete them.
- **Automatic:** with `subtitle_auto_fetch: true`, files added from then on that have no subtitle in `subtitle_languages` (default `en`) get the best hash or id match, once per file. Nothing found is retried after 6 hours, a day, 4 days and 16 days, then left alone. It stops for the day when the quota runs out.
- **Offline:** search says OpenSubtitles cannot be reached; nothing else changes.

**Subtitle timing:** in the player, Subtitle timing moves subtitles 250 ms earlier (−) or later (+), and Reset puts them back. The delay is kept per user and per file on the server, which shifts the WebVTT cues it serves, and the playback start response carries it as `subtitle_offset_ms` for players that read the subtitle files themselves.

### Radarr and Sonarr

New imports can show up in seconds instead of at the next scan. Set `VCRX_ARR_WEBHOOK_TOKEN` in `.env` (16 characters or more), restart, then in Radarr and in Sonarr add Settings, Connect, Webhook:

- URL: `http://<server-ip>:3750/api/v1/hooks/arr`, method POST
- Password: the token (the username can be anything), or put `?token=<token>` on the URL
- Triggers: On Import, On Upgrade, On Rename, and the delete triggers

VCRX scans just that movie's or series' folder; for a delete (a movie, a series or a file removed) it scans the folder's parent instead, usually the whole library, because the folder itself may be gone. It finds the folder by its name inside a library of the right kind; if Radarr or Sonarr see the NAS at a different path and the names differ, map the prefix with `arr_path_map` in `vcrx.yaml`. Webhooks are accepted only from the LAN. A Test from Radarr or Sonarr answers 200.

## Run the TV app

Build the APK with one command and sideload it with ADB: [tv/README.md](../tv/README.md). On the TV, enter `http://<server-ip>:3750` and sign in.

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
