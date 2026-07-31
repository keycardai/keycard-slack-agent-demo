-- User tokens (Slack → Keycard mapping)
CREATE TABLE IF NOT EXISTS users (
    team_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    keycard_identity TEXT NOT NULL,
    access_token BLOB NOT NULL,
    refresh_token BLOB NOT NULL,
    expires_at INTEGER NOT NULL,
    created_at INTEGER DEFAULT (unixepoch()),
    PRIMARY KEY (team_id, user_id)
);

CREATE TABLE IF NOT EXISTS auth_prompts (
    team_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    prompt TEXT NOT NULL,
    sent_at INTEGER NOT NULL,
    PRIMARY KEY (team_id, user_id, prompt)
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    team_id TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    thread_ts TEXT,
    created_at INTEGER DEFAULT (unixepoch()),
    updated_at INTEGER DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS session_index (
    session_id TEXT PRIMARY KEY,
    team_id TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    thread_ts TEXT,
    user_id TEXT NOT NULL,
    is_private INTEGER NOT NULL,
    last_message_ts TEXT,
    last_seen_at INTEGER NOT NULL,
    updated_at INTEGER DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    user_id TEXT,
    slack_ts TEXT,
    created_at INTEGER DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS context (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    messages TEXT NOT NULL,
    created_at INTEGER DEFAULT (unixepoch())
);

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_session_index_seen ON session_index(last_seen_at);
CREATE INDEX IF NOT EXISTS idx_users_team ON users(team_id);

