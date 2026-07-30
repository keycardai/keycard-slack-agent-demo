package db

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/keycardai/slack-agent/app"
	"github.com/keycardai/slack-agent/crypto"
)

type DB struct {
	conn   *sql.DB
	cipher *crypto.Cipher
}

type UserTokens struct {
	KeycardIdentity string
	AccessToken     string
	RefreshToken    string
	ExpiresAt       int64
}

type Session struct {
	ID        string
	TeamID    string
	ChannelID string
	ThreadTS  string
	Model     string
}

type SessionIndex struct {
	SessionID     string
	TeamID        string
	ChannelID     string
	ThreadTS      string
	UserID        string
	IsPrivate     bool
	LastMessageTS string
	LastSeenAt    int64
}

// schema is embedded so the binary carries its own DDL. Reading schema.sql from the working
// directory meant the agent only started when launched from the repo root.
//
//go:embed schema.sql
var schema string

// sqliteParams are set on the connection because two of them cannot be set anywhere else.
//
//	_foreign_keys is per connection, not stored in the file, so the REFERENCES clauses in
//	schema.sql were decorative until this was added.
//	_busy_timeout stops a concurrent write from failing outright as SQLITE_BUSY, which a Slack
//	agent handling several events at once will otherwise hit.
//	_journal_mode is persistent once set, and is repeated here so a fresh file gets it too.
const sqliteParams = "_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL"

func Open(path string, cipher *crypto.Cipher) (*DB, error) {
	conn, err := sql.Open("sqlite3", dsn(path))
	if err != nil {
		return nil, err
	}
	if err := conn.Ping(); err != nil {
		return nil, err
	}
	if err := applySchema(conn); err != nil {
		return nil, err
	}
	return &DB{conn: conn, cipher: cipher}, nil
}

// dsn appends the connection parameters, preserving any the caller already supplied.
func dsn(path string) string {
	if strings.Contains(path, "?") {
		return path + "&" + sqliteParams
	}
	return path + "?" + sqliteParams
}

func (d *DB) Close() error {
	if d.conn == nil {
		return nil
	}
	return d.conn.Close()
}

func applySchema(conn *sql.DB) error {
	_, err := conn.Exec(schema)
	return err
}

func (d *DB) StoreUserTokens(teamID, userID string, tokens *UserTokens) error {
	if tokens == nil {
		return errors.New("tokens required")
	}
	encAccess, err := d.cipher.Encrypt([]byte(tokens.AccessToken))
	if err != nil {
		return err
	}
	encRefresh, err := d.cipher.Encrypt([]byte(tokens.RefreshToken))
	if err != nil {
		return err
	}

	_, err = d.conn.Exec(`
		INSERT INTO users (team_id, user_id, keycard_identity, access_token, refresh_token, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(team_id, user_id) DO UPDATE SET
			keycard_identity = excluded.keycard_identity,
			access_token = excluded.access_token,
			refresh_token = excluded.refresh_token,
			expires_at = excluded.expires_at
	`, teamID, userID, tokens.KeycardIdentity, encAccess, encRefresh, tokens.ExpiresAt)
	if err != nil {
		return err
	}
	// A successful link ends the suppression, so a future disconnect prompts immediately.
	return d.ClearAuthPrompts(teamID, userID)
}

func (d *DB) GetUserTokens(teamID, userID string) (*UserTokens, error) {
	row := d.conn.QueryRow(`
		SELECT keycard_identity, access_token, refresh_token, expires_at
		FROM users WHERE team_id = ? AND user_id = ?
	`, teamID, userID)

	var keycardIdentity string
	var accessBlob []byte
	var refreshBlob []byte
	var expiresAt int64
	if err := row.Scan(&keycardIdentity, &accessBlob, &refreshBlob, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	access, err := d.cipher.Decrypt(accessBlob)
	if err != nil {
		return nil, err
	}
	refresh, err := d.cipher.Decrypt(refreshBlob)
	if err != nil {
		return nil, err
	}

	return &UserTokens{
		KeycardIdentity: keycardIdentity,
		AccessToken:     string(access),
		RefreshToken:    string(refresh),
		ExpiresAt:       expiresAt,
	}, nil
}

func (d *DB) DeleteUserTokens(teamID, userID string) error {
	_, err := d.conn.Exec(`DELETE FROM users WHERE team_id = ? AND user_id = ?`, teamID, userID)
	return err
}

func (d *DB) SessionExists(sessionID string) (bool, error) {
	var count int
	err := d.conn.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, sessionID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (d *DB) GetOrCreateSession(sessionID, teamID, channelID, threadTS string) (*Session, error) {
	_, err := d.conn.Exec(`
		INSERT OR IGNORE INTO sessions (id, team_id, channel_id, thread_ts)
		VALUES (?, ?, ?, ?)
	`, sessionID, teamID, channelID, nullIfEmpty(threadTS))
	if err != nil {
		return nil, err
	}

	row := d.conn.QueryRow(`
		SELECT id, team_id, channel_id, COALESCE(thread_ts, ''), model
		FROM sessions WHERE id = ?
	`, sessionID)

	var sess Session
	if err := row.Scan(&sess.ID, &sess.TeamID, &sess.ChannelID, &sess.ThreadTS, &sess.Model); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (d *DB) AddMessage(sessionID, role, content, userID, slackTS string) error {
	id := newID()
	_, err := d.conn.Exec(`
		INSERT INTO messages (id, session_id, role, content, user_id, slack_ts)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, sessionID, role, content, nullIfEmpty(userID), nullIfEmpty(slackTS))
	return err
}

func (d *DB) UpsertSessionIndex(entry SessionIndex) error {
	_, err := d.conn.Exec(`
		INSERT INTO session_index (
			session_id, team_id, channel_id, thread_ts, user_id, is_private, last_message_ts, last_seen_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			team_id = excluded.team_id,
			channel_id = excluded.channel_id,
			thread_ts = excluded.thread_ts,
			user_id = excluded.user_id,
			is_private = excluded.is_private,
			last_message_ts = excluded.last_message_ts,
			last_seen_at = excluded.last_seen_at,
			updated_at = excluded.updated_at
	`, entry.SessionID, entry.TeamID, entry.ChannelID, nullIfEmpty(entry.ThreadTS), entry.UserID, boolToInt(entry.IsPrivate), nullIfEmpty(entry.LastMessageTS), entry.LastSeenAt, time.Now().Unix())
	return err
}

func (d *DB) SaveContext(sessionID string, messages []app.StoredMessage) error {
	payload, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	id := newID()
	_, err = d.conn.Exec(`
		INSERT INTO context (id, session_id, messages)
		VALUES (?, ?, ?)
	`, id, sessionID, string(payload))
	return err
}

func (d *DB) GetContext(sessionID string) ([]app.StoredMessage, error) {
	row := d.conn.QueryRow(`
		SELECT messages FROM context WHERE session_id = ?
		ORDER BY created_at DESC LIMIT 1
	`, sessionID)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var messages []app.StoredMessage
	if err := json.Unmarshal([]byte(payload), &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// AuthPromptCooldown is how long a "connect your account" prompt suppresses the next one.
//
// The suppression used to be permanent. sent_at was recorded and never read, so a person who
// ignored the prompt once was never prompted again, and every later message did nothing with no
// feedback. Suppression exists to avoid nagging inside one conversation, not to give up on someone.
const AuthPromptCooldown = 24 * time.Hour

func (d *DB) HasAuthPromptSent(teamID, userID, prompt string) (bool, error) {
	if teamID == "" || userID == "" || prompt == "" {
		return false, nil
	}
	var sentAt int64
	err := d.conn.QueryRow(`
		SELECT sent_at FROM auth_prompts WHERE team_id = ? AND user_id = ? AND prompt = ? LIMIT 1
	`, teamID, userID, prompt).Scan(&sentAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return time.Now().Before(time.Unix(sentAt, 0).Add(AuthPromptCooldown)), nil
}

// ClearAuthPrompts drops a user's prompt suppression. Called once they have linked, so a later
// disconnect prompts them again straight away rather than after the cooldown.
func (d *DB) ClearAuthPrompts(teamID, userID string) error {
	if teamID == "" || userID == "" {
		return nil
	}
	_, err := d.conn.Exec(`DELETE FROM auth_prompts WHERE team_id = ? AND user_id = ?`, teamID, userID)
	return err
}

func (d *DB) MarkAuthPromptSent(teamID, userID, prompt string) error {
	if teamID == "" || userID == "" || prompt == "" {
		return errors.New("team_id, user_id, and prompt required")
	}
	_, err := d.conn.Exec(`
		INSERT INTO auth_prompts (team_id, user_id, prompt, sent_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(team_id, user_id, prompt) DO UPDATE SET sent_at = excluded.sent_at
	`, teamID, userID, prompt, time.Now().Unix())
	return err
}

func nullIfEmpty(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func nullIfZero(value int64) interface{} {
	if value == 0 {
		return nil
	}
	return value
}

func newID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
