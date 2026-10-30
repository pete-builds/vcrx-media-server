-- Matching now settles ties by runtime, seasons on disk, a country tag or a clear
-- popularity lead. Retry everything it skipped before, on the next pass.
UPDATE tmdb_state SET checked_at = 0 WHERE status != 'matched';
