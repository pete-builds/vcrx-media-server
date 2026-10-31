-- Remove from Continue Watching on a show's next-episode card. The card stays
-- away until the user finishes another episode of that show after this time.
CREATE TABLE up_next_dismissed (
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    show_id      INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    dismissed_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, show_id)
);
