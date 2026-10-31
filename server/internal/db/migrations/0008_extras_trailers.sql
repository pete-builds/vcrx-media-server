-- Local extras: trailers, featurettes, deleted scenes and the like beside a movie
-- or show, plus a show's theme.mp3. They hang off their item and never become
-- items of their own. Like media_versions, a vanished file is marked missing and
-- kept for the retention period, and access follows the item it belongs to.
CREATE TABLE item_extras (
    id            INTEGER PRIMARY KEY,
    item_id       INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    library_id    INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('trailer', 'featurette', 'behind_the_scenes', 'deleted_scene',
                                                'interview', 'scene', 'short', 'other', 'theme')),
    title         TEXT NOT NULL,
    path          TEXT NOT NULL UNIQUE,
    rel_path      TEXT NOT NULL,
    size          INTEGER NOT NULL,
    mtime         INTEGER NOT NULL,
    container     TEXT,
    duration_ms   INTEGER,
    bitrate       INTEGER,
    video_codec   TEXT,
    video_profile TEXT,
    video_level   INTEGER,
    bit_depth     INTEGER,
    width         INTEGER,
    height        INTEGER,
    resolution    TEXT CHECK (resolution IS NULL OR resolution IN ('SD', '480p', '576p', '720p', '1080p', '4K', '8K')),
    frame_rate    REAL,
    hdr_type      TEXT CHECK (hdr_type IS NULL OR hdr_type IN ('hdr10', 'hdr10plus', 'hlg', 'dolby_vision')),
    -- The audio streams as JSON ([{"index", "codec", "language", "channels", "default"}]):
    -- enough for the playback decision, without a streams table of their own.
    audio_streams TEXT NOT NULL DEFAULT '[]',
    probe_status  TEXT NOT NULL DEFAULT 'pending' CHECK (probe_status IN ('pending', 'ok', 'error')),
    probe_error   TEXT,
    probed_at     INTEGER,
    missing_since INTEGER,
    added_at      INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX item_extras_item ON item_extras (item_id);
CREATE INDEX item_extras_library ON item_extras (library_id);

-- The best official YouTube trailer TMDb lists for a movie or show, when the
-- admin turns on trailers_youtube. A failed refresh keeps the last good key.
CREATE TABLE youtube_trailers (
    item_id      INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    -- The TMDb id the key was fetched for; Fix Match changing it means a refetch.
    tmdb_id      INTEGER NOT NULL,
    youtube_key  TEXT,
    name         TEXT,
    language     TEXT,
    published_at TEXT,
    status       TEXT NOT NULL CHECK (status IN ('found', 'none', 'error')),
    error        TEXT,
    checked_at   INTEGER NOT NULL
);

-- A playback session for an extra rather than a version: no watch state, and
-- converted streams find their file through this.
ALTER TABLE playback_sessions ADD COLUMN extra_id INTEGER REFERENCES item_extras(id) ON DELETE SET NULL;
