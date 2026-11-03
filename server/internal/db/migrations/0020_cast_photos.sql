-- Actor photos. TMDb names each actor with a person id and, often, a
-- headshot; one row per person, shared by every title they appear in, with
-- the photo downloaded once into the metadata folder. cache_path is NULL
-- when TMDb has no photo or the download failed.
CREATE TABLE people (
    tmdb_id      INTEGER PRIMARY KEY,
    name         TEXT NOT NULL,
    profile_path TEXT,
    cache_path   TEXT,
    updated_at   INTEGER NOT NULL
);

-- Which TMDb person an actor tag is. NULL for actors an NFO or an admin
-- listed: they get a name and no photo, as before.
ALTER TABLE item_tags ADD COLUMN person_id INTEGER;

-- Shows now take their cast from every season, not only the latest, and
-- every title picks up its actors' photos, so matched titles are due again on
-- the next pass instead of in up to 30 days.
UPDATE tmdb_state SET checked_at = 0 WHERE status = 'matched';
