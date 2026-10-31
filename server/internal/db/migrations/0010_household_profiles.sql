-- Household profiles, like Plex Home: the people who share the server's
-- screens can switch between their own profiles on one device, each with its
-- own Continue Watching and watched state, behind an optional PIN.
--
-- Numbered 0010 after the open 0007 to 0009 (#41, #44, #42). Migrations must
-- run 1..n without gaps, so this fails until those merge; renumber if not.
ALTER TABLE users ADD COLUMN household INTEGER NOT NULL DEFAULT 0;
-- A managed profile has no password and is reached only by switching to it
-- from another household profile. Its password_hash holds a value no password
-- can match.
ALTER TABLE users ADD COLUMN managed INTEGER NOT NULL DEFAULT 0;
-- A kid's profile: managed, never admin, and limited by a content rating.
ALTER TABLE users ADD COLUMN kid INTEGER NOT NULL DEFAULT 0;
-- 4 to 8 digits, hashed with argon2id like a password. NULL means no PIN.
ALTER TABLE users ADD COLUMN pin_hash TEXT;
-- When an uploaded picture last changed, for cache busting. NULL: initials.
ALTER TABLE users ADD COLUMN avatar_updated_at INTEGER;

-- The server's admins are its household to begin with.
UPDATE users SET household = 1 WHERE is_admin = 1;
