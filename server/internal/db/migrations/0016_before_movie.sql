-- Before a movie (feature batch item 3): each profile chooses whether a
-- pre-roll clip from the admin's preroll_dir plays before a movie, and how
-- many local trailers for other unwatched titles it may see first, 0 to 3.
-- Both off by default; the feature itself counts as the only thing watched.
ALTER TABLE users ADD COLUMN before_movie_trailers INTEGER NOT NULL DEFAULT 0 CHECK (before_movie_trailers BETWEEN 0 AND 3);
ALTER TABLE users ADD COLUMN before_movie_preroll INTEGER NOT NULL DEFAULT 0 CHECK (before_movie_preroll IN (0, 1));
