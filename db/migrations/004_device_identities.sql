CREATE TABLE IF NOT EXISTS device_identities (
    client_id     TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_name   TEXT NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL,
    last_seen_at  DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_device_identities_user_id
ON device_identities(user_id);
