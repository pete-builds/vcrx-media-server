-- Lend a tape (milestone 10, item 1). The owner lends one movie: a printed
-- card carries an invite (0011) scoped to that one title, and while the loan
-- is out nobody else can stream the owner's copy, the owner included.
--
-- The borrower is an invited device like any other (a users row with
-- device_token = 1), so its libraries, resolution and converting rules sit in
-- library_grants and user_policies; this table adds the one title it may see
-- and the block on everyone else. Both are read by the one policy path.
--
-- Loans are kept after they end, for Activity: who borrowed what, when, and
-- whether it came back early or ran out. Titles and names are copied in so the
-- history reads the same after the movie or the device is gone.
--
-- Migrations must run 1..n without gaps: this follows 0012 and 0013, which
-- other open pull requests hold.
CREATE TABLE loans (
    id               INTEGER PRIMARY KEY,
    item_id          INTEGER REFERENCES items(id) ON DELETE SET NULL,
    token_id         INTEGER UNIQUE REFERENCES device_tokens(id) ON DELETE SET NULL,
    borrower_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    title            TEXT NOT NULL,
    borrower         TEXT NOT NULL,
    lent_by          TEXT,
    created_at       INTEGER NOT NULL,
    due_at           INTEGER NOT NULL,
    -- Set when the tape comes back: returned early by an admin (or by the
    -- borrower signing out), or expired at due_at.
    ended_at         INTEGER,
    end_reason       TEXT CHECK (end_reason IS NULL OR end_reason IN ('returned', 'expired')),
    ended_by         TEXT
);

-- One loan per copy: a second loan of a title that is out cannot be written.
-- A loan past its due date still holds this until the sweep (or the next loan
-- of the same title) marks it expired.
CREATE UNIQUE INDEX loans_one_per_copy ON loans (item_id) WHERE ended_at IS NULL;
CREATE INDEX loans_borrower ON loans (borrower_user_id);
CREATE INDEX loans_created ON loans (created_at);

-- The borrower's sign-in sessions, by the SHA-256 of their token, so that once
-- the loan is over the device's next request can be told "Be kind, rewind."
-- instead of a bare "sign in again". The hash grants nothing: the session it
-- names is refused like any other ended device's.
CREATE TABLE loan_sessions (
    token_hash BLOB PRIMARY KEY,
    loan_id    INTEGER NOT NULL REFERENCES loans(id) ON DELETE CASCADE
);
