CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE CHECK (email = lower(email)),
    name TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_user_id_idx ON sessions(user_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('running', 'done', 'failed')),
    started_at TIMESTAMPTZ NOT NULL,
    snapshot JSONB NOT NULL,
    request JSONB NOT NULL,
    UNIQUE (id, user_id),
    FOREIGN KEY (parent_id, user_id) REFERENCES runs(id, user_id)
);
CREATE INDEX runs_user_started_idx ON runs(user_id, started_at DESC, id DESC);
