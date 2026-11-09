CREATE TABLE digest_smtp_settings (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    host TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    username TEXT NOT NULL,
    password TEXT NOT NULL,
    sender TEXT NOT NULL
);
