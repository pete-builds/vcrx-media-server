-- Live TV, step 1 (milestone 11, docs/plans/2026-10-31-live-tv.md): channels
-- from the M3U playlists an admin lists under live_tv in vcrx.yaml. VCRX never
-- ships or suggests a playlist. Playlist and stream URLs stay on the server:
-- clients only ever see VCRX's own routes.
CREATE TABLE live_sources (
    id            INTEGER PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    url           TEXT NOT NULL,
    refreshed_at  INTEGER,
    last_error    TEXT,
    channel_count INTEGER NOT NULL DEFAULT 0
);

-- A channel is matched across refreshes by source_key: its tvg-id, else a
-- hash of its stream URL. The admin's own number and enabled flag survive a
-- refresh; gone marks a channel the playlist no longer lists.
CREATE TABLE channels (
    id              INTEGER PRIMARY KEY,
    source_id       INTEGER NOT NULL REFERENCES live_sources(id) ON DELETE CASCADE,
    source_key      TEXT NOT NULL,
    name            TEXT NOT NULL,
    playlist_number TEXT,
    number          TEXT,
    tvg_id          TEXT,
    group_title     TEXT,
    stream_url      TEXT NOT NULL,
    logo_url        TEXT,
    logo_file       TEXT,
    enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    gone            INTEGER NOT NULL DEFAULT 0 CHECK (gone IN (0, 1)),
    added_at        INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    UNIQUE (source_id, source_key)
);
CREATE INDEX channels_listed ON channels (enabled, gone);

-- Live TV is a grant, like a library: off for everyone but admins until an
-- admin turns it on for a user, a profile or an invited device.
ALTER TABLE user_policies ADD COLUMN allow_live_tv INTEGER NOT NULL DEFAULT 0;
