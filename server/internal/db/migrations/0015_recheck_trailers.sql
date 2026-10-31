-- The trailer pass now asks YouTube whether a trailer still plays embedded and
-- passes over one that is gone or has embedding off. Trailers found before
-- that were never asked, so they are due again on the next pass rather than
-- in up to 30 days. Only the check time moves; every key keeps playing until
-- its title is looked at again.
UPDATE youtube_trailers SET checked_at = 0 WHERE status = 'found';
