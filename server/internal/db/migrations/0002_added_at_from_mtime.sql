-- added_at follows when a file arrived on disk (its mtime), not when the first
-- scan happened to see it. The first build stamped every row of an initial scan
-- with the same time, which left Recently Added in walk order; scans now use the
-- mtime, and this backfills rows written before that.

UPDATE media_versions SET added_at = mtime WHERE mtime > 0 AND mtime < added_at;

UPDATE items SET added_at = (SELECT MIN(v.added_at) FROM media_versions v WHERE v.item_id = items.id)
WHERE kind IN ('movie', 'episode') AND EXISTS (SELECT 1 FROM media_versions v WHERE v.item_id = items.id);

UPDATE items SET added_at = (SELECT MIN(e.added_at) FROM items e WHERE e.parent_id = items.id)
WHERE kind = 'season' AND EXISTS (SELECT 1 FROM items e WHERE e.parent_id = items.id);

UPDATE items SET added_at = (SELECT MIN(s.added_at) FROM items s WHERE s.parent_id = items.id)
WHERE kind = 'show' AND EXISTS (SELECT 1 FROM items s WHERE s.parent_id = items.id);
