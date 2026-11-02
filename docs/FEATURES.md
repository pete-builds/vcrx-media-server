# VCRX features

What each part of VCRX does and how to configure it. Setup and the optional services (TMDb, OpenSubtitles, Radarr and Sonarr) are in [INSTALL.md](INSTALL.md).

## Works now, and what comes later

| Works now | Comes later |
|---|---|
| Movie and TV libraries, incremental scans with ffprobe, NFO and filename metadata, local artwork, TMDb enrichment (optional, your own key), IMDb ratings (optional), Kometa-style poster overlays, automatic franchise collections, smart collections and hidden labels from YAML rules, genre and decade filters | Online lists (IMDb Top 250 and the like), playlists, automatic director and studio collections |
| Local accounts and per-user access: library grants, a resolution ceiling, a content-rating ceiling, label allow and deny lists, converting allowed or not, all edited in Settings > Users and enforced on every route (a test walks them all) | Access settings in the TV app |
| Direct play over HTTP with Range; converted streams for clients that ask for them (fragmented MP4, or HLS for Safari: video copied, or transcoded to H.264 when the client cannot decode it; audio copied or converted to AAC); sidecar and embedded text subtitles (also as WebVTT for browsers), subtitle search and download from OpenSubtitles (optional), a per-user subtitle delay | Picture subtitle burn-in, VA-API hardware acceleration |
| Skip Intro and Skip Credits (web and TV): intros and end credits found from each season's audio at night, chapter titles and Plex-style NFO markers honoured, an admin editor, and a per-user choice of button, automatic or off | Skip markers for movies other than by hand |
| Every playback logged with its decision and reason, resume positions, Continue Watching. Activity API: now playing, history, per-title history, stats, per-user totals, stop a stream, webhooks, a watch state export for cleanup tools; Activity screens in the web app and the TV app | Taste signals from friends' watching (opt-in) |
| Household profiles with optional PINs, invited devices that need no account (a link or a QR code with its own libraries, resolution and expiry), friends' servers linked to each other with per-friend limits and streaming only, never copying, and lending one title to a friend for a day to a week (three days by default) | Playing from whichever friend's copy is fastest, direct connections between friends' servers with no relay |
| Music libraries: artists, albums and compilations from tags, live shows (date, venue, source, the taper's info file and checksums), covers, direct play or AAC/Opus conversion, play queues, a web player that keeps playing across pages, and a TV Music section | Setlists and per-song chapters, tape trading |
| Web app at `/`: browse, details, search, play in the browser with subtitles (including files whose video the browser cannot decode, transcoded on the server), Activity screens. REST API under `/api/v1`, OpenAPI 3.1 spec checked against the routes in CI | Other TV platforms |

A file the TV cannot play directly is converted on the server, as for the web app, in a fragmented MP4 stream: the container rewrapped, the audio converted to AAC, or the video transcoded to H.264, under the same policy and limits. It is refused, with a message saying why, only when the account may not use converted streams or the server cannot convert it (for example, with transcoding turned off).

## Web app

Open `http://<server>:3750/` in a browser. It is part of the server: no separate install, no build step, nothing loaded from the internet.

- **Browse** Home (see below), each library with sort and filters, search, and details pages for movies, shows, seasons and episodes. Mark things watched or unwatched.
- **Continue Watching** cards have a Remove button (on hover, always on touch screens, and reachable with Tab), and a details page with a resume position has Remove from Continue Watching. Either forgets your resume position without marking the title watched, and only for you (`DELETE /api/v1/items/{id}/progress`).
- **Search as you type:** after two characters the search box lists up to eight matching titles with poster, year or episode, and kind. Arrow keys and Enter open one; Enter with none highlighted shows every result; Escape closes the list.
- **Keyboard:** press `?` anywhere outside a text field for the list of shortcuts, `/` to jump to search. In the player: Space or K to play or pause, the arrow keys to skip back or forward, F for full screen, M to mute, Escape to leave.
- **Play** in the browser, resuming where any device left off. How it decides what to send is under Playing in the browser, below.
- **Activity** for admins: now playing with a Stop button, history, stats and users. A movie, show or episode page also shows admins its own History: the latest 20 plays with who, when, device, how much was watched and how far it got, and the totals.
- **Profiles,** like Plex Home: "Who's watching?" from the avatar in the top bar, or on opening when the server shows the picker. Each household member has their own Continue Watching and watched state, an optional 4 to 8 digit PIN, and an uploaded picture or their initials. Admins add kids' profiles (no password, a content-rating limit, PG unless set) and manage PINs in Settings > Users > Household. The TV app has the same picker, run with the remote.
- **Invited devices,** with no account: Settings > Devices mints an invite (a link and a QR code) for chosen libraries, a highest resolution, whether converting is allowed, and an expiry. The device that opens it joins with no username or password, sees only that, never the profile picker, and streams but cannot download. A TV chooses "Pair with a phone" and shows a code; scan it with a phone signed in as an admin, or type it under Settings > Devices, and approve it with a scope. Each device is revoked on its own, from Devices or from its stream in Activity.
- **Friends' servers:** each friend runs their own VCRX, and two servers link with a server invite. Under Settings > Friends, "Share with a friend's server" mints one with the same scope as a device's (libraries, highest resolution, converting, expiry) plus a bandwidth cap covering everything that server streams at once; the friend's admin pastes the link into "Watch a friend's server" on theirs. Their titles then show on your Home in a "From friends' servers" row, each badged with where it lives ("on Mark's server, 4K"), and play from their server through yours: your server relays the stream, and nothing is ever copied to it. Only your household (admins and profiles) sees them, under its own policy; invited devices and accounts outside the household never do. A server that is down shows as such and never holds Home up. Either side revokes or unlinks at any time, and anything playing stops. Friends' servers must be reachable from yours, on the LAN or over Tailscale: there is no relay or NAT traversal yet.
- **Sign-in:** with `auto_sign_in_user` set, a browser on a trusted network is signed straight in; otherwise it shows a sign-in form. The session is an HttpOnly cookie, and every change the page makes carries a header a cross-site page cannot send.

### Playing in the browser

- **Container or audio the browser cannot play.** The browser tells the server what it can decode. A file it cannot play directly because of its container or its audio (most often E-AC-3, AC-3 or DTS, which browsers do not decode) plays as a converted stream: the video is copied untouched and only the audio is converted to AAC, a fraction of one CPU core, with VCRX's own seek bar.
- **Video the browser cannot play.** Video the browser cannot decode at all (HEVC in Firefox, AV1 on older machines, VC-1 or MPEG-4 Part 2 anywhere) is transcoded to H.264 with libx264 in the same kind of stream: scaled down to the browser's reported size and to 1080p at most (`max_transcode_height` in `vcrx.yaml` or `VCRX_MAX_TRANSCODE_HEIGHT`, default 1080, fits the picture inside a 16:9 box that tall; 0 removes the cap), never up, with a keyframe every 2 seconds, and HDR tone mapped to SDR for a screen without HDR when the server's ffmpeg has the zscale filter (the Docker image's does; without it HDR looks washed out). A seek starts the new stream exactly where you asked. Each transcode takes one or more CPU cores, so only `VCRX_MAX_TRANSCODES` (default 2, 0 turns it off) run at once and the next viewer is told the server is busy; the server logs at startup whether its ffmpeg can transcode. The usual policy applies: a non-admin needs "May use converted streams", and "Including 4K" for a 4K file.
- **Safari.** Safari (on a Mac, iPhone or iPad, and every browser on an iPhone or iPad) plays video only from a server that answers byte ranges, which a stream made on the fly cannot, so it gets the same conversions as HLS, which it plays natively: segments of about 4 seconds written under `/cache/hls` while the session plays, with the same limits and policy. The server keeps each Safari stream's files to about 1.5 GB by pausing ffmpeg when it is two minutes ahead of the player and deleting segments a minute behind it, and deletes them all when playback stops, after two idle minutes, and at startup.
- **Subtitles.** Pick any text track, sidecar or inside the file; picture subtitles (PGS, VobSub, DVB) are listed greyed out with "Picture subtitles need the TV app", because a browser cannot draw them; "Search for subtitles…" finds more on OpenSubtitles; Subtitle timing moves them 250 ms at a time when they are out of sync.

## Music

A library with `kind: music` (see `deploy/vcrx.example.yaml`) holds FLAC, MP3, AAC and ALAC in M4A, Opus, Ogg Vorbis, WAV, AIFF, WavPack and SHN. Scans read the tags with ffprobe (artist, album artist, album, track and disc, date, genre, MusicBrainz ids, ReplayGain) and group tracks into albums by album artist and title, or by MusicBrainz release when tagged. Files with no album artist that name several artists under one album title in one folder, or carry the compilation flag, are a Various Artists compilation. Covers come from `cover.jpg`, `folder.jpg` or `front.jpg` beside the tracks, else from a picture inside a track; an artist's picture from `artist.jpg` or `folder.jpg` in their own folder. Scans are incremental, and a file that vanishes is hidden and kept for 30 days, as for video.

**Live recordings** are albums of their own: a folder with a show date in its name (`Artist - 2024-06-14 - Venue, City [SBD]`, or the etree shape `gh2024-06-14.sbd.miller.flac16`) and an info `.txt`, an `.ffp` or `.md5` file, or a source word (SBD, AUD, matrix, FM). The date, venue, place and source come from the folder name and the info file (Source, Lineage and Taper lines, and the usual artist, date, venue, place header), and every checksum the `.md5` and `.ffp` files declare is stored per file for checking copies later. Disc and set folders (`CD1`, `Set 2`) are part of their show.

**Playing:** a browser or the TV plays the file directly with byte ranges when it decodes it, and otherwise gets it converted on the fly to AAC (or Opus) through the same ffmpeg and policy as video: an account without "May use converted streams" is refused. Chrome does not decode ALAC, and nothing plays SHN, so those convert. Every track played is a listen in Activity. In the web app the player sits at the bottom of the window and keeps playing while you browse; Space plays and pauses anywhere outside a text field, and phones show the track on the lock screen (Media Session). The next track is loaded while the current one plays, so a live set flows from one track into the next without a gap you can hear. On the TV, a Music library shows artists, albums and live shows, an album screen, and Now Playing with the queue.

## Home

Home is built per user on the server, under their access policy, so the web app and the TV app show the same rows. In order: Continue Watching, Recently Added Movies and Recently Added TV (one row per library; several new episodes of a show are one card, "3 new episodes"), holiday and seasonal picks, New Seasons of shows you watch, Because You Watched (titles sharing a director, franchise, lead actors or genres with what you finished last, never ones you have finished), Trending on This Server (plays by everyone in the last week, recent plays weighing more), Recently Released, Top Rated Movies and Shows (IMDb, or TMDb when IMDb has too few votes), Unwatched Gems, three genres of the day, collections marked for Home, Most Watched, Most Watched by You, Rediscover (highly rated titles you watched over a year ago) and a Best of a decade. A row with fewer than four titles is left out (Continue Watching excepted), and each row's See all opens it as a grid of up to 100.

**Customize Home** (Settings > Home, or the button at the bottom of Home) shows or hides each row and moves it up or down, by keyboard or by dragging, and saves at once. The layout is stored on the server (`GET`, `PUT` and `DELETE /api/v1/layout/home`), so it follows you to the TV. An admin can make their layout the default for everyone without their own (`/api/v1/admin/layout/home`).

**Seasons** are matched offline from genres, titles, taglines, plots and NFO tags, by the server's local date: Halloween (October; horror, thriller and the supernatural, plus a family Halloween row, which is the only one a profile with a PG or lower rating ceiling gets), Thanksgiving week, Christmas and the holidays (Nov 25 to Dec 31), Valentine's week and summer blockbusters. `seasons.yaml` in the config directory changes them:

```yaml
seasons:
  - id: halloween          # an id from the table turns it off...
    enabled: false
  - id: christmas          # ...or replaces it
    title: Holiday Classics
    start: "12-01"         # MM-DD, or thanksgiving+N / thanksgiving-N
    end: "12-25"
    keywords: [christmas, santa]
  - id: hockey             # a new id adds a season
    title: Playoff Hockey
    start: "04-15"
    end: "06-15"
    genres: [Sport]
    keywords: [hockey]
```

**TMDb rows** (optional, off by default): `tmdb_home_lists: true` with a TMDb key adds Trending on TMDb and Popular on TMDb, limited to what is in your libraries. The lists refresh every six hours; offline, the last good copy stays.

## Poster overlays (Kometa style)

List `overlays` under a library in `vcrx.yaml` and the server draws badges onto that library's posters, the way Kometa does for Plex. Because the badges are part of the poster image, the TV app, the web app and anything else that shows posters get them with no change. `?overlays=0` on an artwork URL returns the clean poster.

| Type | Draws | Options |
|---|---|---|
| `rating` | A star and score with its source, top left by default | `sources`: preference order, default `[imdb, tmdb]` |
| `resolution` | The best version's resolution with an HDR, DV, DV HDR or HDR10+ tag, bottom right | `resolutions`: which get a badge, default `[4K]` |
| `ribbon` | A diagonal corner ribbon, default `TOP RATED` | `min_rating` (8.0), `min_votes` (25,000 for IMDb), `text` |

All three take `position`: `top_left`, `top_right`, `bottom_left` or `bottom_right`. Badges are VCRX's own lettering, not copies of third-party logos. Kometa's `ribbon` default draws on IMDb Top 250 and Rotten Tomatoes lists that VCRX has no source for, so VCRX's ribbon is a rule on the IMDb rating and vote count instead.

**IMDb ratings:** set `imdb_ratings: true` and the server downloads IMDb's public ratings file once a day (and only when it changed) and keeps the rating and vote count for every title with an IMDb id. NFO files and TMDb enrichment supply the ids. `vcrx metadata imdb` runs the sync now. IMDb licenses the file for personal, non-commercial use, so it is off by default.

## Trailers and extras

Extras laid out the way Plex reads them work with no internet: `<name>-trailer.mkv` beside a film, and the folders `Trailers`, `Featurettes`, `Behind The Scenes`, `Deleted Scenes`, `Interviews`, `Scenes`, `Shorts` and `Other` inside a film's or show's folder (a season's folder works too and attaches to the show). They never become titles of their own. Each film or show page gets a Trailer button and an Extras row; they play in the VCRX player through the same decision and access rules as the film, so a friend who cannot see a film cannot play its trailer either, and a trailer never moves the film's resume point. A show's `theme.mp3` plays quietly on its TV page (and on the web page when you press Theme music). Like any file, an extra that disappears is kept for 30 days before it is forgotten.

**YouTube trailers (optional):** with a TMDb key, `trailers_youtube: true` (or `VCRX_TRAILERS_YOUTUBE=true`) looks up each title's official trailer on TMDb and offers it when there is no local one. The web app plays it in YouTube's no-cookie player (the only outside frame the web app's security policy then allows, and only with this setting on); the TV app opens the YouTube app. Before keeping a trailer, the server asks YouTube's public oEmbed endpoint whether it still plays in another site's player, and passes over one that is gone or has embedding turned off for TMDb's next best, so a Trailer button is never dead. Off by default, because playing one needs the internet and YouTube then sees the request.

**Before a movie (optional, per profile):** each profile can choose up to three local trailers for other movies it may see and has not watched, and a pre-roll clip, to play before a movie. Pre-rolls come from `preroll_dir`, a folder of short clips mounted read-only (`VCRX_PREROLL_DIR` in `.env`). Off by default. Trailers come only from local trailer files, never YouTube, and follow the profile's access rules, so a kids' profile never sees a trailer for a title it cannot see. Nothing that plays first counts as watched.

## Live TV (in progress, optional)

Channels from M3U playlists you list under `live_tv` in `vcrx.yaml`, read at startup and every 12 hours. VCRX ships no playlist and suggests none; play what you have the right to watch. A playlist URL with an account's password goes in a file named by `m3u_url_file`, and neither it nor any stream URL ever reaches a client. Viewers need the Live TV grant in Settings > Users; admins always have it. Each playlist can name an XMLTV guide (`xmltv`, a URL or file, gzipped or not), read every 6 hours and matched to channels by tvg-id or name: channels list what is on now and next (`GET /api/v1/live/channels`) and `GET /api/v1/live/guide` gives the grid. To watch, `POST /api/v1/live/channels/{id}/watch` returns VCRX's own live HLS playlist: one pull per channel, shared by everyone watching it, video copied and audio converted to AAC, at most `max_streams` (4) channels at once. The web app has a Live TV page with the guide's now and next and a player with channel up and down, and the TV app has the same in a Live TV row (`docs/plans/2026-10-31-live-tv.md`).

## Collections

Every film series (a TMDb collection) with two or more of its films in a movie library becomes a collection there, in release order, like Kometa's `franchise` default. The membership is rebuilt every 30 minutes, so a new sequel joins on its own; a series that drops to one film goes away. Names come from TMDb once, so this needs `VCRX_TMDB_API_KEY`; without it, series wait rather than get a made-up name. `vcrx metadata collections` builds them now. The web app shows them under a library's Collections button and links each film's page to its series. Libraries also filter by genre and decade.

## Smart collections and hidden labels (Kometa style)

Write collections as YAML files in `/config/collections/` (`collections_dir` in `vcrx.yaml` or `VCRX_COLLECTIONS_DIR` moves it); `deploy/collections.example.yaml` is a commented start. The files are the source of truth: the database copy is rebuilt from them after every scan, every TMDb pass and every 15 minutes, so losing the database loses nothing.

```yaml
collections:
  - name: R-Rated Horror
    library: Movies
    smart:
      all:                  # every rule matches; any: at least one does; lists nest
        - genre: Horror
        - content_rating: R
    label: horror           # stamped on every match, taken off when a title stops matching
    visible: { library: false, home: false, shared: false }   # label only, never listed
```

Rules use local metadata only: genre, studio, director, writer, actor, country, network, decade, year or a year range, resolution (of any present version), HDR, content rating, label and `added_within_days`. A list of values means any of them. A file with a mistake is refused whole, with the file and line in the log, in `vcrx metadata collections` (which builds everything now) and in `GET /api/v1/admin/collections`; its collections keep building from the last good copy, and nothing else changes. An empty, comment-only or cut-short file counts as a mistake, because editors and sync tools leave one mid-save; to empty a file on purpose, write `collections: []`. Deleting a file removes its collections and labels once it has stayed gone for a minute and a later build still finds it gone. If the directory itself is missing, nothing changes at all.

Rebuilds also follow TMDb: after each enrichment pass that matched anything and after a Fix Match, a rebuild runs within seconds, so a deny label lands as soon as the genre or rating that triggers it arrives.

`label:` is how friends' access follows your rules: deny `horror` in a user's policy and a title disappears for them as soon as a rebuild finds it matches, and comes back when it stops matching. VCRX only ever takes off labels it stamped. A label you set by hand or that an NFO file carries is never removed, and an NFO naming a stamped label takes it over. Collections with `visible.library: false` are left out of library collection lists, title pages and Home; `visible.home: true` gives a collection its own row on Home, and `shared` (on unless set false) decides whether that row also reaches users other than admins.

## Skip Intro and Skip Credits

A season's episodes share their theme song and usually their end credits. VCRX compares the first 10 minutes of each episode with its neighbours' by audio fingerprint, and the last 8 minutes likewise: the stretch heard in most of them is the intro (or the credits). A cold open before the theme is fine, and a recap ("previously on") is not mistaken for the intro because it repeats only one other episode. Credits followed by a scene end before the scene, so Skip Credits lands on it; credits that run to the end of the file become Next Episode, straight into Up Next. When nothing matches, a final stretch of black picture (5 s or more) or silence (15 s or more) counts as credits. A season that shares nothing gets no markers: a wrong Skip button is worse than none.

How it stays out of the way: one season at a time, ffmpeg and the matching at `nice 19`, only between `marker_hours` (01:00 to 06:00 local time by default; set `TZ`), never in a library being scanned, and only for episodes it has not seen (fingerprints are cached under `/cache/markers`, about 30 KB per episode). It resumes after a restart. The existing library is backfilled the same way; Settings, Intros shows progress show by show (`GET /api/v1/admin/markers`). `detect_markers: false` turns it off.

Markers from the file win over detection: a chapter titled Intro, Opening, Credits, End Credits and the like, or Plex-style `<Marker type="intro" startTimeOffset="0" endTimeOffset="51000"/>` elements (milliseconds) in the episode's NFO. An admin's own markers win over everything: on an episode's page, set or clear each one, copy it to the rest of the season (VCRX finds the same audio in each episode), or detect the season again. Cleared stays cleared.

Each person chooses in Settings: show a Skip button (default), skip automatically, or off (`GET`/`PUT /api/v1/me/playback-settings`, field `skip_markers`: `button`, `auto` or `off`; the TV app reads the same value from each playback session). Skipping automatically acts only on intros detection is sure of (confidence 0.75 or more, or markers from the file or an admin); credits always wait for a press, so a scene after them is never skipped.

Check detection against your own library without storing anything: `docker compose exec vcrx vcrx markers detect --dry-run "/media/TV/Some Show"` prints each season folder's episodes with the intro and credits it finds.

## Activity and notifications

Like Tautulli, without a second program. Every play request is logged with the device, address, version, decision and reason, and the admin API reads it back (admin account, from the LAN):

| Endpoint | What it gives |
|---|---|
| `GET /api/v1/admin/activity/now` | What is playing now: who, what, where, progress, direct play or not, bitrate, and the total bandwidth |
| `GET /api/v1/admin/activity/history` | Finished plays, newest first, by user or by title (a show includes its episodes) |
| `GET /api/v1/admin/items/{id}/history` | One title's plays, newest first, with its totals: plays, people, watch time. What the details page shows |
| `GET /api/v1/admin/watch/export` | Every movie and show with its file paths, size, when it was added and last played, play count and who finished it, for a cleanup tool (see below) |
| `GET /api/v1/admin/activity/stats?days=30` | Plays and watch time, top movies, shows, users, clients, devices and platforms, plays by day, hour and weekday, direct play against converted, and the most streams at once |
| `GET /api/v1/admin/activity/users` | Every account with its totals and when and where it was last seen |
| `POST /api/v1/admin/activity/sessions/{id}/terminate` | Stop a stream now, with a message the viewer sees |

A play counts in stats once 30 seconds have actually played, and is watched at 90%. Days and hours follow the container's `TZ`. Each chart on the Stats page has a "Show as a table" view with the same numbers.

Addresses are labelled by the kind of network they are on: This server, Local network, Tailscale (100.64.0.0/10 and its IPv6 prefix) or Internet. That works offline; public addresses are not placed on a map.

**Watch state for cleanup tools:** a script that deletes watched or never-played media needs to know, per title, whether anyone watched it and when. `GET /api/v1/admin/watch/export` answers that for every movie and show still on disk, a show rolled up from its episodes: `item_id`, `kind`, `title`, `year`, `library`, `paths` (absolute, as the server sees them), `size_bytes`, `added_at`, `last_played_at` (any user, a stream in progress included; `null` if never), `play_count` (times finished, all users), `finished_by` (usernames), and for shows `episodes_total` and `episodes_watched`. `?library_id=` narrows it to one library, and `?unplayed_days=30` keeps only titles nobody has played in 30 days, never-played ones included. On the host, `vcrx watch export -library Movies -unplayed-days 30` prints the same rows as JSON lines. The paths are the container's; map them to the host's before deleting anything.

**Webhooks:** set `VCRX_WEBHOOK_URLS` in `.env` to one or more URLs, comma separated. Each gets a JSON post for `play`, `stop`, `refused`, `terminated` and `added` (new titles after a scan, such as `＋ New in Movies: The Film (2024), Show (3 new episodes)`; narrow the events with `webhook_events` in `vcrx.yaml`). A Discord webhook URL gets a plain Discord message instead, such as `▶ pete started The Film on Living Room TV (direct play)`.

**Newsletter:** Settings > Newsletter sends a weekly digest of what was added in the last seven days (new movies, and new episodes grouped by show) to the same webhooks, on a weekday and hour you pick in the server's time zone. It lists only what its audience may see: by default, titles every friend (non-admin) account can see, so a library only some are granted and a title with a label any of them is denied stay out; or pick one friend account whose view it uses. Add `newsletter` to `webhook_events` if you narrow the events. Discord gets a card per title with its TMDb poster (posters that exist only on disk have no public link), cut to fit Discord's limits with "and N more"; other URLs get the full list as JSON (`event: newsletter`). It is off by default, sends nothing in a week when nothing was added, and remembers the last week it sent, so a restart never sends it twice. "Send a preview now" posts the last seven days straight away.
