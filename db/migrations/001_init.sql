PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
    id           TEXT PRIMARY KEY,
    email        TEXT UNIQUE NOT NULL,
    password     TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    last_login   DATETIME,
    is_active    INTEGER DEFAULT 1
);

CREATE TABLE IF NOT EXISTS user_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL,
    device_name  TEXT,
    expires_at   DATETIME NOT NULL,
    revoked      INTEGER DEFAULT 0,
    created_at   DATETIME NOT NULL,
    last_seen_at DATETIME
);

CREATE TABLE IF NOT EXISTS user_settings (
    user_id              TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    bark_key_enc         TEXT,
    push_time            TEXT DEFAULT '08:00',
    timezone             TEXT DEFAULT 'Asia/Shanghai',
    ai_key_enc           TEXT,
    ai_model             TEXT DEFAULT 'deepseek-chat',
    ai_base_url          TEXT,
    sync_mode            TEXT DEFAULT 'full',
    excluded_folders     TEXT DEFAULT '[]',
    min_note_length      INTEGER DEFAULT 50,
    max_note_age_days    INTEGER DEFAULT 0,
    storage_quota_bytes  INTEGER DEFAULT 104857600,
    updated_at           DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS notes (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    path            TEXT NOT NULL,
    title           TEXT NOT NULL,
    content         TEXT NOT NULL,
    size_bytes      INTEGER NOT NULL,
    content_hash    TEXT NOT NULL,
    note_updated_at DATETIME NOT NULL,
    synced_at       DATETIME NOT NULL,
    is_deleted      INTEGER DEFAULT 0,
    UNIQUE(user_id, path)
);

CREATE INDEX IF NOT EXISTS idx_notes_user_id ON notes(user_id);
CREATE INDEX IF NOT EXISTS idx_notes_user_deleted ON notes(user_id, is_deleted);

CREATE TABLE IF NOT EXISTS push_jobs (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    note_id      TEXT NOT NULL REFERENCES notes(id),
    status       TEXT DEFAULT 'pending',
    summary      TEXT,
    retry_count  INTEGER DEFAULT 0,
    error_msg    TEXT,
    scheduled_at DATETIME NOT NULL,
    pushed_at    DATETIME,
    created_at   DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_push_jobs_status ON push_jobs(status, scheduled_at);
CREATE INDEX IF NOT EXISTS idx_push_jobs_user ON push_jobs(user_id, scheduled_at);

CREATE TABLE IF NOT EXISTS push_history (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    note_id    TEXT NOT NULL,
    note_path  TEXT NOT NULL,
    note_title TEXT NOT NULL,
    summary    TEXT,
    pushed_at  DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_push_history_user ON push_history(user_id, pushed_at DESC);

CREATE TABLE IF NOT EXISTS ai_usage (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    date       TEXT NOT NULL,
    call_count INTEGER DEFAULT 0,
    UNIQUE(user_id, date)
);

CREATE TABLE IF NOT EXISTS rss_feeds (
    user_id      TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT UNIQUE NOT NULL,
    token_enc    TEXT NOT NULL,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_rss_feeds_token_hash ON rss_feeds(token_hash);
