-- Invite tokens (milestone 9, item 1): the invite is the account. An admin
-- mints an invite link, or approves a TV that shows a pairing code, and the
-- device that redeems it becomes its own principal: no username, password or
-- email, never a household profile, and limited to the scope the admin chose.
--
-- Each device is a users row with device_token = 1, so watch state, history
-- and the one policy path (library_grants and user_policies) work for it as
-- for anyone. It has no password and is never in the household, so it can
-- neither sign in nor switch into a profile.
--
-- Migrations must run 1..n without gaps: this follows 0007 to 0010, which
-- other open pull requests hold.
ALTER TABLE users ADD COLUMN device_token INTEGER NOT NULL DEFAULT 0;

CREATE TABLE device_tokens (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    created_by   TEXT,
    -- 'invite': redeemed from a link or QR code; 'pairing': a TV a phone approved.
    source       TEXT NOT NULL CHECK (source IN ('invite', 'pairing')),
    -- SHA-256 of the invite secret. The secret itself is shown once, at mint,
    -- and never stored. NULL for a paired TV, which never had one.
    invite_hash  BLOB UNIQUE,
    expires_at   INTEGER NOT NULL,
    redeemed_at  INTEGER,
    revoked_at   INTEGER,
    revoked_by   TEXT,
    last_seen_at INTEGER,
    last_ip      TEXT,
    device_name  TEXT,
    device_model TEXT,
    platform     TEXT,
    client       TEXT,
    created_at   INTEGER NOT NULL
);

-- Who played: an account ('user'), a household profile with no password of
-- its own ('profile'), or an invited device ('token'), and which device.
ALTER TABLE playback_sessions ADD COLUMN principal_kind TEXT NOT NULL DEFAULT 'user'
    CHECK (principal_kind IN ('user', 'profile', 'token'));
ALTER TABLE playback_sessions ADD COLUMN token_id INTEGER REFERENCES device_tokens(id) ON DELETE SET NULL;
UPDATE playback_sessions SET principal_kind = 'profile'
    WHERE user_id IN (SELECT id FROM users WHERE managed = 1);
CREATE INDEX playback_sessions_token ON playback_sessions (token_id, ended_at);
