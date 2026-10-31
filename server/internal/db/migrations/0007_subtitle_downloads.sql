-- Subtitles fetched from OpenSubtitles, a per-user subtitle delay, and the
-- automatic fetch's memory of what it already tried.
--
-- Downloaded files live under the config dir, never beside the media (the
-- library mounts are read-only). Each one is also a media_streams row with
-- is_external = 1 and downloaded_id set, so every client lists it like a
-- sidecar; the scanner leaves those rows alone.
CREATE TABLE downloaded_subtitles (
    id                INTEGER PRIMARY KEY,
    version_id        INTEGER NOT NULL REFERENCES media_versions(id) ON DELETE CASCADE,
    provider          TEXT NOT NULL CHECK (provider IN ('opensubtitles')),
    -- The provider's own file id; downloading it again for the same version is free.
    provider_file_id  INTEGER NOT NULL,
    language          TEXT,
    release_name      TEXT,
    hearing_impaired  INTEGER NOT NULL DEFAULT 0,
    hash_match        INTEGER NOT NULL DEFAULT 0,
    -- Path relative to the download dir, chosen by the server, never by the provider.
    rel_path          TEXT NOT NULL,
    downloaded_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at        INTEGER NOT NULL,
    UNIQUE (version_id, provider, provider_file_id)
);

ALTER TABLE media_streams ADD COLUMN downloaded_id INTEGER REFERENCES downloaded_subtitles(id) ON DELETE CASCADE;

-- How far to shift a user's subtitles for one file, in ms. Positive shows them later.
CREATE TABLE subtitle_offsets (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    version_id INTEGER NOT NULL REFERENCES media_versions(id) ON DELETE CASCADE,
    offset_ms  INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, version_id)
);

-- One row per version and language the automatic fetch has looked for. done
-- means never again (found, or given up); otherwise next_attempt_at is when.
CREATE TABLE subtitle_fetch_attempts (
    version_id      INTEGER NOT NULL REFERENCES media_versions(id) ON DELETE CASCADE,
    language        TEXT NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    done            INTEGER NOT NULL DEFAULT 0,
    last_result     TEXT,
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY (version_id, language)
);

-- Off by default: downloads spend the server's shared daily quota.
ALTER TABLE user_policies ADD COLUMN allow_subtitle_download INTEGER NOT NULL DEFAULT 0;
