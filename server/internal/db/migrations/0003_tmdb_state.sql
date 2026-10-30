-- One row per item TMDb enrichment has looked at. It records the match, what
-- enrichment wrote (so a later refresh only replaces its own values, never an
-- NFO's or an admin's), and when to look again.
CREATE TABLE tmdb_state (
    item_id    INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    status     TEXT NOT NULL CHECK (status IN ('matched', 'unmatched', 'error')),
    tmdb_id    INTEGER,
    -- The title and year the match was made from; a rename triggers a new match.
    match_key  TEXT NOT NULL DEFAULT '',
    -- Column and tag values enrichment wrote last time, as JSON.
    written    TEXT NOT NULL DEFAULT '{}',
    reason     TEXT,
    checked_at INTEGER NOT NULL
);
CREATE INDEX tmdb_state_status ON tmdb_state (status, checked_at);
