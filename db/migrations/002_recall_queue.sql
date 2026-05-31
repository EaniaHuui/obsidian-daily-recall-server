CREATE TABLE IF NOT EXISTS user_prompt_settings (
    user_id          TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    daily_push_count INTEGER NOT NULL DEFAULT 1,
    summary_prompt   TEXT NOT NULL DEFAULT '',
    updated_at       DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS scheduled_recalls (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    path            TEXT NOT NULL,
    title           TEXT NOT NULL,
    content         TEXT NOT NULL,
    content_hash    TEXT NOT NULL,
    note_updated_at DATETIME NOT NULL,
    scheduled_date  TEXT NOT NULL, -- YYYY-MM-DD in user timezone
    slot_index      INTEGER NOT NULL,
    status          TEXT NOT NULL DEFAULT 'queued', -- queued/summarizing/pushing/done/failed
    summary         TEXT,
    error_msg       TEXT,
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL,
    UNIQUE(user_id, scheduled_date, slot_index)
);

CREATE INDEX IF NOT EXISTS idx_scheduled_recalls_due
ON scheduled_recalls(user_id, scheduled_date, status);

CREATE INDEX IF NOT EXISTS idx_scheduled_recalls_hash
ON scheduled_recalls(user_id, content_hash);
