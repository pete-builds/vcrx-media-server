-- Podcasts: RSS feeds an account user subscribes to by URL. A feed is kept
-- once per server however many people subscribe; subscriptions, places and
-- played flags are each user's own. A feed nobody subscribes to any more is
-- deleted with its episodes. Episode audio is never stored: it is played
-- through the server from the podcast's host (docs/SECURITY-AND-PRIVACY.md).
CREATE TABLE podcasts (
    id            INTEGER PRIMARY KEY,
    feed_url      TEXT NOT NULL UNIQUE,
    title         TEXT NOT NULL,
    author        TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    link          TEXT NOT NULL DEFAULT '',
    language      TEXT NOT NULL DEFAULT '',
    explicit      INTEGER NOT NULL DEFAULT 0 CHECK (explicit IN (0, 1)),
    image_url     TEXT NOT NULL DEFAULT '',
    image_file    TEXT NOT NULL DEFAULT '',
    new_feed_url  TEXT NOT NULL DEFAULT '',
    etag          TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    refreshed_at  INTEGER,
    checked_at    INTEGER,
    last_error    TEXT,
    added_at      INTEGER NOT NULL
);

-- Episodes are matched across refreshes by guid. Only the newest
-- podcasts.max_episodes of a feed are kept.
CREATE TABLE podcast_episodes (
    id               INTEGER PRIMARY KEY,
    podcast_id       INTEGER NOT NULL REFERENCES podcasts(id) ON DELETE CASCADE,
    guid             TEXT NOT NULL,
    title            TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    published_at     INTEGER,
    duration_seconds INTEGER,
    enclosure_url    TEXT NOT NULL,
    enclosure_type   TEXT NOT NULL DEFAULT '',
    enclosure_length INTEGER,
    image_url        TEXT NOT NULL DEFAULT '',
    image_file       TEXT NOT NULL DEFAULT '',
    season           INTEGER,
    episode          INTEGER,
    episode_type     TEXT NOT NULL DEFAULT 'full',
    chapters_url     TEXT NOT NULL DEFAULT '',
    explicit         INTEGER NOT NULL DEFAULT 0 CHECK (explicit IN (0, 1)),
    added_at         INTEGER NOT NULL,
    UNIQUE (podcast_id, guid)
);
CREATE INDEX podcast_episodes_newest ON podcast_episodes (podcast_id, published_at DESC);

CREATE TABLE podcast_subscriptions (
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    podcast_id    INTEGER NOT NULL REFERENCES podcasts(id) ON DELETE CASCADE,
    subscribed_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, podcast_id)
);
CREATE INDEX podcast_subscriptions_by_podcast ON podcast_subscriptions (podcast_id);

-- A user's place in an episode, and whether they played it.
CREATE TABLE podcast_episode_state (
    user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    episode_id       INTEGER NOT NULL REFERENCES podcast_episodes(id) ON DELETE CASCADE,
    position_seconds REAL NOT NULL DEFAULT 0,
    played           INTEGER NOT NULL DEFAULT 0 CHECK (played IN (0, 1)),
    updated_at       INTEGER NOT NULL,
    PRIMARY KEY (user_id, episode_id)
);
