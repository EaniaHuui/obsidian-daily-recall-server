CREATE TABLE IF NOT EXISTS user_channel_settings (
    user_id            TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enable_rss         INTEGER NOT NULL DEFAULT 1,
    enable_cubox       INTEGER NOT NULL DEFAULT 0,
    cubox_api_url_enc  TEXT NOT NULL DEFAULT '',
    cubox_folder       TEXT NOT NULL DEFAULT '',
    cubox_tags         TEXT NOT NULL DEFAULT '[]',
    updated_at         DATETIME NOT NULL
);
