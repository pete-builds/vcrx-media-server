-- Live TV, step 2 (docs/plans/2026-10-31-live-tv.md): the guide, from the
-- XMLTV file or URL an admin gives a playlist in vcrx.yaml (xmltv). Programmes
-- are matched to channels by tvg-id, else by the guide's display name, and
-- kept from a day ago to 14 days ahead.
ALTER TABLE live_sources ADD COLUMN guide_url TEXT;
ALTER TABLE live_sources ADD COLUMN guide_refreshed_at INTEGER;
ALTER TABLE live_sources ADD COLUMN guide_error TEXT;
ALTER TABLE live_sources ADD COLUMN program_count INTEGER NOT NULL DEFAULT 0;

CREATE TABLE programs (
    id          INTEGER PRIMARY KEY,
    channel_id  INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    start_ms    INTEGER NOT NULL,
    stop_ms     INTEGER NOT NULL CHECK (stop_ms > start_ms),
    title       TEXT NOT NULL,
    sub_title   TEXT,
    description TEXT,
    categories  TEXT NOT NULL DEFAULT '[]',
    episode     TEXT
);
CREATE INDEX programs_by_channel ON programs (channel_id, start_ms);
