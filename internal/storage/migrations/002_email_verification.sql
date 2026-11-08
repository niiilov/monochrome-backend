ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;

CREATE TABLE email_verifications (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
