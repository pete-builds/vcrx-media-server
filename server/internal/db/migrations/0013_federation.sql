-- Federated libraries (milestone 9, item 3): friends' servers link by
-- exchanging tokens, and each streams to the other, never copies.
--
-- The serving side. A linked server is an invited device's principal like any
-- other (a users row with device_token = 1, its scope in library_grants and
-- user_policies, its token checked on every request), marked as a peer. Its
-- invite is stored only as a SHA-256 hash and works once, exactly as a
-- device's does. bandwidth_kbps caps everything it streams at once; NULL is
-- no cap.
--
-- Migrations must run 1..n without gaps: this follows 0007 to 0012, which
-- other open pull requests hold.
ALTER TABLE device_tokens ADD COLUMN peer INTEGER NOT NULL DEFAULT 0;
ALTER TABLE device_tokens ADD COLUMN bandwidth_kbps INTEGER;

-- The watching side: servers this one has linked to, by redeeming their
-- server invite. base_url is the peer's address exactly as an admin entered
-- it (scheme, host and port, nothing else), and every request goes to that
-- host and no other. token is the bearer the peer issued: this server has to
-- present it, so it cannot be a hash, as a TV keeps its own token. The peer
-- holds only its hash.
CREATE TABLE peers (
    id              INTEGER PRIMARY KEY,
    name            TEXT NOT NULL,
    base_url        TEXT NOT NULL,
    remote_name     TEXT,
    token           TEXT NOT NULL,
    expires_at      INTEGER NOT NULL,
    created_by      TEXT,
    created_at      INTEGER NOT NULL,
    last_checked_at INTEGER,
    last_ok_at      INTEGER,
    -- online, offline, bad_answer or unlinked (the peer ended the link)
    state           TEXT NOT NULL DEFAULT 'unknown',
    last_error      TEXT
);
