CREATE TABLE IF NOT EXISTS sources (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    slug                 TEXT    NOT NULL UNIQUE,
    name                 TEXT    NOT NULL,
    kind                 TEXT    NOT NULL,
    url                  TEXT    NOT NULL,
    engine               TEXT    NOT NULL DEFAULT 'http',
    enabled              INTEGER NOT NULL DEFAULT 1,
    request_json         TEXT    NOT NULL DEFAULT '{}',
    extract_json         TEXT    NOT NULL DEFAULT '{}',
    rules_json           TEXT    NOT NULL DEFAULT '[]',
    filter_json          TEXT    NOT NULL DEFAULT '{}',
    article_json         TEXT    NOT NULL DEFAULT '{}',
    policy_json          TEXT    NOT NULL DEFAULT '{}',
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL,
    last_run_at          INTEGER NOT NULL DEFAULT 0,
    next_run_at          INTEGER NOT NULL DEFAULT 0,
    consecutive_failures INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_sources_due ON sources (enabled, next_run_at);

CREATE TABLE IF NOT EXISTS entries (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id    INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    entry_key    TEXT    NOT NULL,
    guid         TEXT    NOT NULL,
    title        TEXT,
    link         TEXT,
    summary      TEXT,
    content_html TEXT,
    content_text TEXT,
    category     TEXT,
    published_at INTEGER,
    first_seen_at INTEGER NOT NULL,
    updated_at   INTEGER,
    content_hash TEXT,
    notified_at  INTEGER,
    UNIQUE (source_id, entry_key)
);

CREATE INDEX IF NOT EXISTS idx_entries_feed ON entries (source_id, COALESCE(published_at, first_seen_at) DESC);

CREATE TABLE IF NOT EXISTS snapshots (
    source_id   INTEGER PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE,
    hash        TEXT    NOT NULL,
    text        TEXT    NOT NULL,
    captured_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id     INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    started_at    INTEGER NOT NULL,
    finished_at   INTEGER,
    status        TEXT    NOT NULL,
    trigger       TEXT,
    items_found   INTEGER NOT NULL DEFAULT 0,
    items_new     INTEGER NOT NULL DEFAULT 0,
    items_updated INTEGER NOT NULL DEFAULT 0,
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    error         TEXT,
    warnings_json TEXT NOT NULL DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_runs_source ON runs (source_id, started_at DESC);

CREATE TABLE IF NOT EXISTS targets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    url_enc    BLOB    NOT NULL,
    kind       TEXT,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
