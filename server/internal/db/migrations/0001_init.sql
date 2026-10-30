-- VCRX Media Server schema, version 1.
-- Conventions: timestamps are unix milliseconds UTC, positions and durations are
-- milliseconds, JSON columns hold arrays or objects as text. Forward-only: never
-- edit a migration after it ships; add a new numbered file instead.

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE libraries (
    id                    INTEGER PRIMARY KEY,
    name                  TEXT NOT NULL UNIQUE COLLATE NOCASE,
    kind                  TEXT NOT NULL CHECK (kind IN ('movie', 'show')),
    root_path             TEXT NOT NULL,
    -- How movie files become versions of one item. 'folder' groups only files that
    -- share a folder and filename prefix; 'title_year' also groups across folders.
    grouping              TEXT NOT NULL DEFAULT 'folder' CHECK (grouping IN ('folder', 'title_year')),
    scan_interval_minutes INTEGER NOT NULL DEFAULT 60,
    created_at            INTEGER NOT NULL,
    updated_at            INTEGER NOT NULL
);

-- Movies, shows, seasons and episodes. Shows own seasons, seasons own episodes.
CREATE TABLE items (
    id                 INTEGER PRIMARY KEY,
    library_id         INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    kind               TEXT NOT NULL CHECK (kind IN ('movie', 'show', 'season', 'episode')),
    parent_id          INTEGER REFERENCES items(id) ON DELETE CASCADE,
    -- Stable identity within (library, kind, parent): the version grouping key for a
    -- movie, the normalized show name for a show, the number for a season or episode.
    group_key          TEXT NOT NULL,
    title              TEXT NOT NULL,
    sort_title         TEXT NOT NULL,
    original_title     TEXT,
    year               INTEGER,
    season_number      INTEGER,
    episode_number     INTEGER,
    episode_number_end INTEGER,
    overview           TEXT,
    tagline            TEXT,
    content_rating     TEXT,
    runtime_ms         INTEGER,
    premiere_date      TEXT,
    tmdb_id            INTEGER,
    imdb_id            TEXT,
    tmdb_collection_id INTEGER,
    metadata_source    TEXT NOT NULL DEFAULT 'filename' CHECK (metadata_source IN ('filename', 'nfo', 'tmdb', 'manual')),
    -- Fields an admin edited by hand; scans and providers never overwrite them.
    locked_fields      TEXT NOT NULL DEFAULT '[]',
    extra              TEXT NOT NULL DEFAULT '{}',
    -- Set when every version of the item has gone missing; hidden from listings.
    missing_since      INTEGER,
    added_at           INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
);
CREATE UNIQUE INDEX items_identity ON items (library_id, kind, IFNULL(parent_id, 0), group_key);
CREATE INDEX items_parent ON items (parent_id);
CREATE INDEX items_library_kind ON items (library_id, kind, sort_title);
CREATE INDEX items_added ON items (library_id, added_at);

-- Genres, studios, directors, writers, cast and countries, one row per value, so
-- smart collections can filter on them with plain SQL.
CREATE TABLE item_tags (
    item_id  INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    kind     TEXT NOT NULL CHECK (kind IN ('genre', 'studio', 'director', 'writer', 'actor', 'country', 'network')),
    value    TEXT NOT NULL COLLATE NOCASE,
    position INTEGER NOT NULL DEFAULT 0,
    role     TEXT,
    PRIMARY KEY (item_id, kind, value)
);
CREATE INDEX item_tags_lookup ON item_tags (kind, value);

-- Ratings per source, kept separate; which one the clients show is a setting.
CREATE TABLE item_ratings (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    source  TEXT NOT NULL CHECK (source IN ('imdb', 'tmdb', 'nfo', 'user')),
    value   REAL NOT NULL,
    votes   INTEGER,
    PRIMARY KEY (item_id, source)
);

-- One row per media file. A title with a 4K and a 1080p file is one item with two
-- versions, and access rules apply per version.
CREATE TABLE media_versions (
    id            INTEGER PRIMARY KEY,
    item_id       INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    library_id    INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    path          TEXT NOT NULL UNIQUE,
    rel_path      TEXT NOT NULL,
    size          INTEGER NOT NULL,
    mtime         INTEGER NOT NULL,
    label         TEXT,
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
    dv_profile    INTEGER,
    dv_compat_id  INTEGER,
    probe_status  TEXT NOT NULL DEFAULT 'pending' CHECK (probe_status IN ('pending', 'ok', 'error')),
    probe_error   TEXT,
    probed_at     INTEGER,
    missing_since INTEGER,
    added_at      INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX media_versions_item ON media_versions (item_id);
CREATE INDEX media_versions_library ON media_versions (library_id);

-- Streams inside a file, plus sidecar subtitle files beside it (is_external = 1).
CREATE TABLE media_streams (
    id             INTEGER PRIMARY KEY,
    version_id     INTEGER NOT NULL REFERENCES media_versions(id) ON DELETE CASCADE,
    stream_index   INTEGER,
    kind           TEXT NOT NULL CHECK (kind IN ('video', 'audio', 'subtitle')),
    codec          TEXT,
    profile        TEXT,
    language       TEXT,
    title          TEXT,
    channels       INTEGER,
    channel_layout TEXT,
    sample_rate    INTEGER,
    bitrate        INTEGER,
    is_default     INTEGER NOT NULL DEFAULT 0,
    is_forced      INTEGER NOT NULL DEFAULT 0,
    is_hearing_impaired INTEGER NOT NULL DEFAULT 0,
    is_external    INTEGER NOT NULL DEFAULT 0,
    external_path  TEXT,
    is_image_based INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX media_streams_version ON media_streams (version_id);

CREATE TABLE labels (
    id     INTEGER PRIMARY KEY,
    name   TEXT NOT NULL UNIQUE COLLATE NOCASE,
    hidden INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE item_labels (
    item_id  INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    label_id INTEGER NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    source   TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'collection', 'nfo')),
    PRIMARY KEY (item_id, label_id)
);

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    -- Case-insensitive from day one: usernames differing only by case are one user.
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    display_name  TEXT,
    password_hash TEXT NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

-- Login sessions. Only a SHA-256 of the bearer token is stored.
CREATE TABLE auth_sessions (
    id             INTEGER PRIMARY KEY,
    user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash     BLOB NOT NULL UNIQUE,
    device_id      TEXT,
    device_name    TEXT,
    device_model   TEXT,
    platform       TEXT,
    client         TEXT,
    client_version TEXT,
    ip             TEXT,
    created_at     INTEGER NOT NULL,
    last_seen_at   INTEGER NOT NULL,
    expires_at     INTEGER NOT NULL
);
CREATE INDEX auth_sessions_user ON auth_sessions (user_id);

-- A user sees nothing until granted a library. Admins see everything.
CREATE TABLE library_grants (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, library_id)
);

CREATE TABLE user_policies (
    user_id              INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    max_resolution       TEXT CHECK (max_resolution IS NULL OR max_resolution IN ('SD', '480p', '576p', '720p', '1080p', '4K', '8K')),
    max_content_rating   TEXT,
    allow_labels         TEXT NOT NULL DEFAULT '[]',
    deny_labels          TEXT NOT NULL DEFAULT '[]',
    allow_transcode      INTEGER NOT NULL DEFAULT 1,
    allow_4k_transcode   INTEGER NOT NULL DEFAULT 0,
    updated_at           INTEGER NOT NULL
);

CREATE TABLE watch_state (
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id         INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    position_ms     INTEGER NOT NULL DEFAULT 0,
    duration_ms     INTEGER,
    played          INTEGER NOT NULL DEFAULT 0,
    play_count      INTEGER NOT NULL DEFAULT 0,
    last_version_id INTEGER REFERENCES media_versions(id) ON DELETE SET NULL,
    last_played_at  INTEGER,
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY (user_id, item_id)
);
CREATE INDEX watch_state_recent ON watch_state (user_id, last_played_at);

-- Activity. History cannot be backfilled, so every playback writes a session and its
-- events from the first milestone. Names and titles are snapshotted so history
-- survives the deletion of the user, item or file it describes.
CREATE TABLE playback_sessions (
    id                TEXT PRIMARY KEY,
    user_id           INTEGER REFERENCES users(id) ON DELETE SET NULL,
    auth_session_id   INTEGER REFERENCES auth_sessions(id) ON DELETE SET NULL,
    item_id           INTEGER REFERENCES items(id) ON DELETE SET NULL,
    version_id        INTEGER REFERENCES media_versions(id) ON DELETE SET NULL,
    username          TEXT,
    item_title        TEXT,
    item_kind         TEXT,
    version_summary   TEXT,
    device_id         TEXT,
    device_name       TEXT,
    device_model      TEXT,
    platform          TEXT,
    client            TEXT,
    client_version    TEXT,
    ip                TEXT,
    decision          TEXT NOT NULL CHECK (decision IN ('direct_play', 'remux', 'audio_transcode', 'transcode', 'refused')),
    decision_reason   TEXT NOT NULL,
    video_decision    TEXT,
    audio_decision    TEXT,
    bitrate           INTEGER,
    hw_accel          TEXT,
    capabilities      TEXT,
    state             TEXT NOT NULL DEFAULT 'created' CHECK (state IN ('created', 'playing', 'paused', 'buffering', 'stopped', 'error', 'refused')),
    start_position_ms INTEGER NOT NULL DEFAULT 0,
    last_position_ms  INTEGER NOT NULL DEFAULT 0,
    duration_ms       INTEGER,
    played_ms         INTEGER NOT NULL DEFAULT 0,
    started_at        INTEGER NOT NULL,
    last_event_at     INTEGER NOT NULL,
    ended_at          INTEGER
);
CREATE INDEX playback_sessions_user ON playback_sessions (user_id, started_at);
CREATE INDEX playback_sessions_item ON playback_sessions (item_id, started_at);
CREATE INDEX playback_sessions_open ON playback_sessions (ended_at);

CREATE TABLE playback_events (
    id               INTEGER PRIMARY KEY,
    session_id       TEXT NOT NULL REFERENCES playback_sessions(id) ON DELETE CASCADE,
    type             TEXT NOT NULL CHECK (type IN ('start', 'pause', 'resume', 'seek', 'progress', 'buffering', 'stop', 'error')),
    position_ms      INTEGER,
    from_position_ms INTEGER,
    bitrate          INTEGER,
    message          TEXT,
    created_at       INTEGER NOT NULL
);
CREATE INDEX playback_events_session ON playback_events (session_id, created_at);

-- Collections. Definitions live in YAML under /config/collections; these rows are a
-- cache rebuilt from it after every scan, so losing the database loses nothing.
CREATE TABLE collections (
    id              INTEGER PRIMARY KEY,
    library_id      INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    slug            TEXT NOT NULL,
    name            TEXT NOT NULL,
    sort_title      TEXT NOT NULL,
    source          TEXT NOT NULL CHECK (source IN ('yaml', 'auto')),
    definition      TEXT NOT NULL DEFAULT '{}',
    label_id        INTEGER REFERENCES labels(id) ON DELETE SET NULL,
    visible_library INTEGER NOT NULL DEFAULT 1,
    visible_home    INTEGER NOT NULL DEFAULT 0,
    visible_shared  INTEGER NOT NULL DEFAULT 1,
    last_built_at   INTEGER,
    last_error      TEXT,
    updated_at      INTEGER NOT NULL,
    UNIQUE (library_id, slug)
);

CREATE TABLE collection_items (
    collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    item_id       INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    position      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (collection_id, item_id)
);

-- Where each item's artwork comes from. Originals are never modified; resized and
-- overlaid variants live under /cache/artwork keyed on their inputs.
CREATE TABLE artwork (
    id          INTEGER PRIMARY KEY,
    item_id     INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('poster', 'backdrop', 'thumb', 'logo')),
    source      TEXT NOT NULL CHECK (source IN ('local', 'tmdb', 'extracted')),
    source_path TEXT,
    url         TEXT,
    cache_path  TEXT,
    width       INTEGER,
    height      INTEGER,
    updated_at  INTEGER NOT NULL,
    UNIQUE (item_id, kind)
);

CREATE TABLE scan_runs (
    id            INTEGER PRIMARY KEY,
    library_id    INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    trigger       TEXT NOT NULL CHECK (trigger IN ('startup', 'schedule', 'manual', 'webhook', 'cli')),
    path_filter   TEXT,
    status        TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'ok', 'error', 'aborted')),
    error         TEXT,
    files_seen    INTEGER NOT NULL DEFAULT 0,
    files_added   INTEGER NOT NULL DEFAULT 0,
    files_changed INTEGER NOT NULL DEFAULT 0,
    files_missing INTEGER NOT NULL DEFAULT 0,
    files_failed  INTEGER NOT NULL DEFAULT 0,
    started_at    INTEGER NOT NULL,
    finished_at   INTEGER
);
CREATE INDEX scan_runs_library ON scan_runs (library_id, started_at);
