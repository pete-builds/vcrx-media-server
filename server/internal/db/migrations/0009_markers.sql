-- Skip Intro and Skip Credits: at most one marker of each kind per episode.
-- source says where it came from, and decides what may replace it:
--   detected  audio fingerprints shared across the season (internal/markers)
--   chapter   a chapter in the file titled Intro, Opening, Credits and so on
--   nfo       Plex-style <Marker> elements in the episode's NFO
--   manual    set by an admin; detection never touches it
-- An admin who clears a marker leaves a manual row with cleared = 1, so
-- detection does not put it back.
CREATE TABLE item_markers (
    item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('intro', 'credits')),
    start_ms   INTEGER NOT NULL,
    end_ms     INTEGER NOT NULL,
    confidence REAL NOT NULL DEFAULT 1,
    source     TEXT NOT NULL CHECK (source IN ('detected', 'chapter', 'nfo', 'manual')),
    cleared    INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (item_id, kind),
    CHECK (cleared = 1 OR (start_ms >= 0 AND end_ms > start_ms))
);

-- The file each episode's markers were last worked out from. Detection runs
-- once per file, and after a restart it carries on with the episodes that
-- have no row (or whose file changed).
CREATE TABLE marker_scans (
    item_id    INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    version_id INTEGER REFERENCES media_versions(id) ON DELETE SET NULL,
    size       INTEGER NOT NULL,
    mtime      INTEGER NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('done', 'error')),
    error      TEXT,
    checked_at INTEGER NOT NULL
);

-- Each person's choice for intros: show a Skip button (the default), skip
-- automatically (only markers detection is sure of), or neither.
ALTER TABLE users ADD COLUMN skip_markers TEXT NOT NULL DEFAULT 'button' CHECK (skip_markers IN ('button', 'auto', 'off'));
