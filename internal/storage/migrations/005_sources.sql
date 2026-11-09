ALTER TABLE users ADD COLUMN sources_seeded BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE sources (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('rss', 'html', 'api')),
    url TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    "fetch" BOOLEAN NOT NULL DEFAULT true,
    article TEXT NOT NULL DEFAULT '',
    item TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    link TEXT NOT NULL DEFAULT '',
    date_sel TEXT NOT NULL DEFAULT '',
    text_sel TEXT NOT NULL DEFAULT '',
    items TEXT NOT NULL DEFAULT '',
    position INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT sources_user_url_key UNIQUE (user_id, url)
);
CREATE INDEX sources_user_idx ON sources(user_id, position, id);
