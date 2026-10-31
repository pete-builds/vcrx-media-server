-- vcrx:step music_library_kind
-- Music libraries. The step above rebuilds the libraries table so its kind may
-- be 'music' (SQLite cannot change a CHECK in place); the tables below hold what
-- a music scan finds.
--
-- Music does not use items and media_versions: an album is not a movie, and
-- keeping the shapes apart means no movie, show or enrichment query can trip
-- over a track. Access is the same rule, though: a library grant.
--
-- Like media_versions, a track whose file vanishes is marked missing and kept
-- for the retention period, never deleted on the first miss.

CREATE TABLE music_artists (
    id             INTEGER PRIMARY KEY,
    library_id     INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    -- The name folded for matching: case, punctuation and spacing do not split
    -- one artist in two.
    group_key      TEXT NOT NULL,
    name           TEXT NOT NULL,
    sort_name      TEXT NOT NULL,
    musicbrainz_id TEXT,
    -- artist.jpg or folder.jpg in the artist's own folder, relative to the root.
    image_path     TEXT,
    -- Set when the artist has no album or track with a file left.
    missing_since  INTEGER,
    added_at       INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (library_id, group_key)
);

-- A studio release, a compilation, or a live show (one folder of one source of
-- one show, as tapers ship it).
CREATE TABLE music_albums (
    id                 INTEGER PRIMARY KEY,
    library_id         INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    artist_id          INTEGER NOT NULL REFERENCES music_artists(id) ON DELETE CASCADE,
    -- mb:<release id>, album:<artist key>|<title key>, or live:<folder>.
    group_key          TEXT NOT NULL,
    title              TEXT NOT NULL,
    sort_title         TEXT NOT NULL,
    year               INTEGER,
    -- YYYY, YYYY-MM or YYYY-MM-DD, as precise as the tags or the folder say.
    date               TEXT,
    genre              TEXT,
    compilation        INTEGER NOT NULL DEFAULT 0,
    live               INTEGER NOT NULL DEFAULT 0,
    -- For live shows, from the folder name and the info file.
    venue              TEXT,
    location           TEXT,
    -- SBD, AUD, MTX or FM; source_detail is the info file's whole Source line.
    source             TEXT,
    source_detail      TEXT,
    lineage            TEXT,
    taper              TEXT,
    -- The show's .txt info file, relative to the root, and its text (capped).
    info_path          TEXT,
    info_text          TEXT,
    -- The album's folder, relative to the root.
    folder             TEXT NOT NULL,
    musicbrainz_id     TEXT,
    musicbrainz_release_group_id TEXT,
    replaygain_gain    REAL,
    replaygain_peak    REAL,
    -- 'file' is cover.jpg or the like beside the tracks; 'embedded' is a picture
    -- inside a track. cover_path is that file, relative to the root.
    cover_source       TEXT CHECK (cover_source IS NULL OR cover_source IN ('file', 'embedded')),
    cover_path         TEXT,
    missing_since      INTEGER,
    added_at           INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    UNIQUE (library_id, group_key)
);
CREATE INDEX music_albums_artist ON music_albums (artist_id);
CREATE INDEX music_albums_added ON music_albums (library_id, added_at);

CREATE TABLE music_tracks (
    id             INTEGER PRIMARY KEY,
    library_id     INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    album_id       INTEGER NOT NULL REFERENCES music_albums(id) ON DELETE CASCADE,
    -- Who performs this track; on a compilation, not the album's artist.
    artist_id      INTEGER NOT NULL REFERENCES music_artists(id) ON DELETE CASCADE,
    path           TEXT NOT NULL UNIQUE,
    rel_path       TEXT NOT NULL,
    size           INTEGER NOT NULL,
    mtime          INTEGER NOT NULL,
    title          TEXT NOT NULL,
    artist_name    TEXT NOT NULL,
    disc_number    INTEGER,
    track_number   INTEGER,
    duration_ms    INTEGER,
    container      TEXT,
    codec          TEXT,
    bitrate        INTEGER,
    sample_rate    INTEGER,
    channels       INTEGER,
    bit_depth      INTEGER,
    -- The tags as read, so a rescan can regroup without probing every file again.
    tags           TEXT NOT NULL DEFAULT '{}',
    musicbrainz_recording_id TEXT,
    replaygain_track_gain REAL,
    replaygain_track_peak REAL,
    embedded_cover INTEGER NOT NULL DEFAULT 0,
    probe_status   TEXT NOT NULL DEFAULT 'pending' CHECK (probe_status IN ('pending', 'ok', 'error')),
    probe_error    TEXT,
    probed_at      INTEGER,
    missing_since  INTEGER,
    added_at       INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX music_tracks_album ON music_tracks (album_id, disc_number, track_number);
CREATE INDEX music_tracks_artist ON music_tracks (artist_id);
CREATE INDEX music_tracks_library ON music_tracks (library_id);

-- The checksums a show's .md5 and .ffp files declare, one row per file and
-- algorithm, so tape trading can verify copies later. ffp is the FLAC
-- fingerprint (the MD5 of the decoded audio in the file's STREAMINFO).
CREATE TABLE music_checksums (
    album_id  INTEGER NOT NULL REFERENCES music_albums(id) ON DELETE CASCADE,
    -- The file as the checksum file names it, relative to the album folder.
    file_name TEXT NOT NULL,
    algorithm TEXT NOT NULL CHECK (algorithm IN ('md5', 'ffp')),
    -- Lowercase hex.
    value     TEXT NOT NULL,
    -- The checksum file it came from, relative to the album folder.
    source    TEXT NOT NULL,
    PRIMARY KEY (album_id, file_name, algorithm)
);

-- A listen is a playback session for a track: no item, no version.
ALTER TABLE playback_sessions ADD COLUMN track_id INTEGER REFERENCES music_tracks(id) ON DELETE SET NULL;
CREATE INDEX playback_sessions_track ON playback_sessions (track_id, started_at);
