-- Directors are drawn beside the cast with their photos, the way actors
-- already are, and episodes now take their own directors from TMDb. Matched
-- titles are due again on the next pass so the existing library picks both up.
UPDATE tmdb_state SET checked_at = 0 WHERE status = 'matched';
