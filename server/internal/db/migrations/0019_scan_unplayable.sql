-- Titles a scan found on disk but cannot index: disc images, Blu-ray and DVD
-- folders, archives with no video beside them, and unfinished downloads. They
-- were left out in silence, so a library could be missing one title in eight
-- while every scan reported nothing failed. files_unplayable is the count;
-- unplayable is a JSON array of {path, reason}, sorted and capped at 500.
ALTER TABLE scan_runs ADD COLUMN files_unplayable INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scan_runs ADD COLUMN unplayable TEXT NOT NULL DEFAULT '[]';
