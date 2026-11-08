CREATE TABLE recipients (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email TEXT NOT NULL CHECK (email = lower(email)),
    status TEXT NOT NULL CHECK (status IN ('send', 'skip')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT recipients_user_email_key UNIQUE (user_id, email)
);
CREATE INDEX recipients_user_idx ON recipients(user_id, created_at, id);
