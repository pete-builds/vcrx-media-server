# Feature batch, asked for 2026-10-31

Pete's list, in his words first, then what exists and the plan. Build in the
order below; each item is its own PR with its own checks. Read `docs/PLAN.md`
first. Tests run in parallel since #57: a new API test calls `t.Parallel()`
unless it reads process-wide state.

> "closed captions to be able to be turned on when you are playing the movie.
> the ability to turn on trailers before the movies. more information when i
> select a movie like a button for the trailer from youtube. i don't like the
> spinning loading logo, maybe an upper left hand corner loading bar, something
> subtle but cool looking. add the hero section to the home page for the
> google tv app. the posters/tiles on the android tv are a bit too big, make it
> look similar to plex."

## 1. YouTube trailer button (config first, then the page)

**Exists:** `trailers_youtube` (config.go:116) plus a TMDb key turns on a
YouTube trailer, embedded on the web from youtube-nocookie.com and opened in
the YouTube app on the TV. On nix1, `deploy/.env` names `VCRX_TMDB_API_KEY`
but `vcrx.yaml` has no `trailers_youtube`, so the feature is likely just off.
**Plan:** confirm the key has a value (without printing it), set
`trailers_youtube: true`, restart, and check a movie page on the web and the
TV. Then the "more information" half: add a details panel to the movie page
on both clients (cast, director, studio, runtime, IMDb rating and votes,
audio and video badges), from fields the scanner and TMDb already store.
**Done when:** the Trailer button plays on both clients for a title with a
TMDb trailer, and a title without one shows no dead button.

## 2. Captions while playing (TV first)

**Exists:** both players have a subtitle choice (`player.js:79`,
`tv/.../player/SubtitleSelection.kt`), plus search and timing.
**Gap to confirm on the TV:** it is likely buried in a menu. **Plan:** a
one-press CC button on the TV player's control bar that toggles the last-used
track (or the first matching the profile's language), remembered per profile;
the same toggle as a CC button on the web player. Captions must keep working
under converted and HLS playback (`ConvertedSubtitles.kt`).
**Done when:** on the TV, CC on and off mid-movie in two presses or fewer,
direct play and converted both, verified on the living room TV when nobody is
watching.

## 3. Trailers before the movie (pre-rolls)

**Exists:** local extras (`-trailer` files) and YouTube trailers; Pete has a
Pre-Roll folder on the NAS. Nothing plays before a feature yet.
**Plan:** a per-profile setting, off by default: before a movie, play N
trailers (0 to 3) for other unwatched titles in the library from local
extras, then the pre-roll clip if one is configured (`preroll_path`, mounted
read-only), then the movie. Skippable with one press. Never before an
episode, never for a kid profile's restricted titles, never a trailer the
profile's rating limit would hide. Watch state counts only the feature.
**Done when:** the access walk probes any new route, a test proves a hidden
title's trailer never plays, and the TV and web both play the chain.

## 4. Loading bar instead of the spinner

**Exists:** web uses skeletons and a `.loading` text state (app.css:106); the
TV uses Material `CircularProgressIndicator`.
**Plan:** one thin VCRX Orange bar pinned to the top left, growing to the
right with an eased, indeterminate shimmer, shown only after 150 ms so fast
loads show nothing. The same bar on the web (CSS only, respects
`prefers-reduced-motion`) and the TV (a Compose component replacing every
`CircularProgressIndicator`). Skeletons stay.
**Done when:** no spinner remains on either client (a grep for
`CircularProgressIndicator` finds none), checked by screenshot.

## 5. TV Home: the web's hero, and smaller tiles

**Exists:** the web Home hero; the TV Home has rows only.
**Plan:** a hero on the TV Home matching the web's (backdrop art, title,
year, rating, overview, Play and More buttons), cycling through the same
featured titles the web uses, from the same endpoint. Shrink poster tiles
about 20 to 25 percent, toward Plex's density: about 7 posters across a
1080p screen instead of the current count, focus scale 1.1, titles under the
poster only for the focused tile. Measure first: screenshot the current Home
over adb, count tiles per row, then set the target.
**Done when:** before and after screenshots from the TV, and the TV's
`lint, unit tests, assembleDebug` job green.

## Still queued from before

Milestone 9 item 5 (NAT traversal), milestone 10 item 8 (federated skip
markers), 10 item 4 (video store shelves), federation token encryption at rest
(decided in PLAN.md), watchlist, shuffle. Pre-rolls moved up into item 3.
