-- vcrx:step audiobook_library_kind
-- Audiobook libraries. The step above rebuilds the libraries table so its kind
-- may be 'audiobook'; the tables below hold what an audiobook scan finds, and
-- each listener's place in each book.
--
-- Like music, a book is not an item: it has no versions, seasons or ratings,
-- and keeping it apart means no movie or show query can trip over one. Access
-- is the same rule as music: a library grant.
--
-- A book is one file with its own chapter marks (an M4B), or a folder of
-- files played in order, each a chapter when it has none of its own. A file
-- whose path vanishes is marked missing and kept for the retention period.

CREATE TABLE audiobook_books (
    id             INTEGER PRIMARY KEY,
    library_id     INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    -- dir:<folder> for a folder of files, file:<path> for a book in one file.
    group_key      TEXT NOT NULL,
    -- The book's folder, relative to the root ('' for a loose file at the root).
    folder         TEXT NOT NULL,
    title          TEXT NOT NULL,
    sort_title     TEXT NOT NULL,
    subtitle       TEXT,
    author         TEXT NOT NULL,
    author_sort    TEXT NOT NULL,
    narrator       TEXT,
    series         TEXT,
    -- A REAL, since series number books 1.5 and the like.
    series_index   REAL,
    year           INTEGER,
    date           TEXT,
    genre          TEXT,
    publisher      TEXT,
    description    TEXT,
    -- The whole book, every file that is present, in milliseconds.
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    -- 'file' is cover.jpg or the like in the folder; 'embedded' is a picture
    -- inside a file. cover_path is that file, relative to the root.
    cover_source   TEXT CHECK (cover_source IS NULL OR cover_source IN ('file', 'embedded')),
    cover_path     TEXT,
    missing_since  INTEGER,
    added_at       INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (library_id, group_key)
);
CREATE INDEX audiobook_books_added ON audiobook_books (library_id, added_at);
CREATE INDEX audiobook_books_author ON audiobook_books (library_id, author_sort);

-- The files of a book in play order. start_ms is where the file begins in the
-- whole book, so a position in the book maps onto one file and an offset.
CREATE TABLE audiobook_files (
    id             INTEGER PRIMARY KEY,
    library_id     INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    book_id        INTEGER NOT NULL REFERENCES audiobook_books(id) ON DELETE CASCADE,
    path           TEXT NOT NULL UNIQUE,
    rel_path       TEXT NOT NULL,
    size           INTEGER NOT NULL,
    mtime          INTEGER NOT NULL,
    position       INTEGER NOT NULL DEFAULT 0,
    start_ms       INTEGER NOT NULL DEFAULT 0,
    title          TEXT NOT NULL,
    disc_number    INTEGER,
    track_number   INTEGER,
    duration_ms    INTEGER,
    container      TEXT,
    codec          TEXT,
    bitrate        INTEGER,
    sample_rate    INTEGER,
    channels       INTEGER,
    -- The tags and chapter marks as read, so a rescan can regroup without
    -- probing every file again.
    tags           TEXT NOT NULL DEFAULT '{}',
    chapters       TEXT NOT NULL DEFAULT '[]',
    embedded_cover INTEGER NOT NULL DEFAULT 0,
    probe_status   TEXT NOT NULL DEFAULT 'pending' CHECK (probe_status IN ('pending', 'ok', 'error')),
    probe_error    TEXT,
    probed_at      INTEGER,
    missing_since  INTEGER,
    added_at       INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX audiobook_files_book ON audiobook_files (book_id, position);
CREATE INDEX audiobook_files_library ON audiobook_files (library_id);

-- A book's table of contents, in order, in milliseconds from the start of the
-- whole book. Rewritten on every scan of the book.
CREATE TABLE audiobook_chapters (
    book_id   INTEGER NOT NULL REFERENCES audiobook_books(id) ON DELETE CASCADE,
    idx       INTEGER NOT NULL,
    title     TEXT NOT NULL,
    start_ms  INTEGER NOT NULL,
    end_ms    INTEGER NOT NULL,
    file_id   INTEGER REFERENCES audiobook_files(id) ON DELETE CASCADE,
    PRIMARY KEY (book_id, idx)
);

-- Where each listener is in each book: one position across the whole book,
-- in seconds, and whether they finished it.
CREATE TABLE audiobook_progress (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id    INTEGER NOT NULL REFERENCES audiobook_books(id) ON DELETE CASCADE,
    position_s REAL NOT NULL DEFAULT 0,
    finished   INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, book_id)
);
CREATE INDEX audiobook_progress_recent ON audiobook_progress (user_id, updated_at);

-- A listen to a book is a playback session for one of its files.
ALTER TABLE playback_sessions ADD COLUMN book_file_id INTEGER REFERENCES audiobook_files(id) ON DELETE SET NULL;
CREATE INDEX playback_sessions_book_file ON playback_sessions (book_file_id, started_at);
