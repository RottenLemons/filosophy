// Package whatsapp provides WhatsApp indexing for Filosophy.
// This file owns the local SQLite schema for messages and chats —
// separate from the main filosophy.db so it can be wiped independently.
package whatsapp

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Message is a single WhatsApp message stored locally.
type Message struct {
	MessageID string    `json:"messageId"`
	ChatJID   string    `json:"chatJid"`
	ChatName  string    `json:"chatName"`
	Sender    string    `json:"sender"`
	SenderName string   `json:"senderName"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
	IsGroup   bool      `json:"isGroup"`
	MediaType string    `json:"mediaType"` // "", "image", "video", "audio", "document"
	MediaName string    `json:"mediaName"` // original filename for documents
}

// Chat is a summary row — one per conversation.
type Chat struct {
	JID         string    `json:"jid"`
	Name        string    `json:"name"`
	IsGroup     bool      `json:"isGroup"`
	LastMessage string    `json:"lastMessage"`
	LastAt      time.Time `json:"lastAt"`
	MsgCount    int       `json:"msgCount"`
}

// Store wraps the local whatsapp SQLite database.
type Store struct {
	db *sql.DB
}

// OpenStore opens (or creates) the whatsapp SQLite DB at dir/whatsapp.db.
func OpenStore(dir string) (*Store, error) {
	path := filepath.Join(dir, "whatsapp.db")
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("whatsapp store: open: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite — single writer
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close shuts down the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ── Schema ────────────────────────────────────────────────────────────────────

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			jid          TEXT PRIMARY KEY,
			name         TEXT NOT NULL DEFAULT '',
			is_group     INTEGER NOT NULL DEFAULT 0,
			last_message TEXT NOT NULL DEFAULT '',
			last_at      INTEGER NOT NULL DEFAULT 0,
			msg_count    INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS messages (
			message_id   TEXT NOT NULL,
			chat_jid     TEXT NOT NULL,
			sender       TEXT NOT NULL DEFAULT '',
			sender_name  TEXT NOT NULL DEFAULT '',
			text         TEXT NOT NULL DEFAULT '',
			timestamp    INTEGER NOT NULL DEFAULT 0,
			is_group     INTEGER NOT NULL DEFAULT 0,
			media_type   TEXT NOT NULL DEFAULT '',
			media_name   TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (message_id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid) ON DELETE CASCADE
		);

		CREATE INDEX IF NOT EXISTS idx_messages_chat    ON messages(chat_jid, timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_messages_ts      ON messages(timestamp DESC);

		-- FTS5 table for full-text search over message bodies
		CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			message_id UNINDEXED,
			chat_jid   UNINDEXED,
			sender_name,
			text,
			content='messages',
			content_rowid='rowid',
			tokenize='unicode61'
		);

		-- Keep FTS in sync with the messages table
		CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, message_id, chat_jid, sender_name, text)
			VALUES (new.rowid, new.message_id, new.chat_jid, new.sender_name, new.text);
		END;

		CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, message_id, chat_jid, sender_name, text)
			VALUES ('delete', old.rowid, old.message_id, old.chat_jid, old.sender_name, old.text);
		END;

		CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, message_id, chat_jid, sender_name, text)
			VALUES ('delete', old.rowid, old.message_id, old.chat_jid, old.sender_name, old.text);
			INSERT INTO messages_fts(rowid, message_id, chat_jid, sender_name, text)
			VALUES (new.rowid, new.message_id, new.chat_jid, new.sender_name, new.text);
		END;
	`)
	return err
}

// ── Writes ────────────────────────────────────────────────────────────────────

// UpsertChat creates or updates a chat row.
func (s *Store) UpsertChat(jid, name string, isGroup bool) error {
	_, err := s.db.Exec(`
		INSERT INTO chats(jid, name, is_group)
		VALUES (?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			name     = CASE WHEN excluded.name != '' THEN excluded.name ELSE chats.name END,
			is_group = excluded.is_group
	`, jid, name, boolInt(isGroup))
	return err
}

// InsertMessage inserts a message, ignoring duplicates.
// It also updates the parent chat's summary counters.
func (s *Store) InsertMessage(m Message) (inserted bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	res, err := tx.Exec(`
		INSERT OR IGNORE INTO messages
			(message_id, chat_jid, sender, sender_name, text, timestamp, is_group, media_type, media_name)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		m.MessageID, m.ChatJID, m.Sender, m.SenderName,
		m.Text, m.Timestamp.Unix(),
		boolInt(m.IsGroup), m.MediaType, m.MediaName,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		tx.Rollback()
		return false, nil // duplicate
	}

	// Update chat summary
	_, err = tx.Exec(`
		UPDATE chats SET
			last_message = CASE WHEN ? > last_at THEN ? ELSE last_message END,
			last_at      = MAX(last_at, ?),
			msg_count    = msg_count + 1
		WHERE jid = ?
	`, m.Timestamp.Unix(), m.Text, m.Timestamp.Unix(), m.ChatJID)
	if err != nil {
		return false, err
	}

	return true, tx.Commit()
}

// BulkInsert inserts many messages efficiently inside a single transaction.
// Returns the number of newly inserted rows.
func (s *Store) BulkInsert(msgs []Message) (int, error) {
	if len(msgs) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO messages
			(message_id, chat_jid, sender, sender_name, text, timestamp, is_group, media_type, media_name)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	inserted := 0
	chatSummaries := make(map[string]struct{ lastTs int64; lastText string })

	for _, m := range msgs {
		res, err := stmt.Exec(
			m.MessageID, m.ChatJID, m.Sender, m.SenderName,
			m.Text, m.Timestamp.Unix(),
			boolInt(m.IsGroup), m.MediaType, m.MediaName,
		)
		if err != nil {
			return inserted, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
			if cur, ok := chatSummaries[m.ChatJID]; !ok || m.Timestamp.Unix() > cur.lastTs {
				chatSummaries[m.ChatJID] = struct{ lastTs int64; lastText string }{m.Timestamp.Unix(), m.Text}
			}
		}
	}

	// Batch-update chat summaries
	for jid, s2 := range chatSummaries {
		_, err = tx.Exec(`
			UPDATE chats SET
				last_message = CASE WHEN ? > last_at THEN ? ELSE last_message END,
				last_at      = MAX(last_at, ?),
				msg_count    = msg_count + (
					SELECT COUNT(*) FROM messages WHERE chat_jid = ? AND timestamp = ?
				)
			WHERE jid = ?
		`, s2.lastTs, s2.lastText, s2.lastTs, jid, s2.lastTs, jid)
		if err != nil {
			return inserted, err
		}
	}

	return inserted, tx.Commit()
}

// ── Reads ─────────────────────────────────────────────────────────────────────

// SearchMessages performs FTS5 full-text search over message bodies.
// Returns up to limit results ordered by recency.
func (s *Store) SearchMessages(query string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT m.message_id, m.chat_jid, COALESCE(c.name,''), m.sender, m.sender_name,
		       m.text, m.timestamp, m.is_group, m.media_type, m.media_name
		FROM messages_fts f
		JOIN messages m ON m.rowid = f.rowid
		LEFT JOIN chats c ON c.jid = m.chat_jid
		WHERE messages_fts MATCH ?
		ORDER BY m.timestamp DESC
		LIMIT ?
	`, ftsQuery(query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// GetChats returns all known chats, most-recently-active first.
func (s *Store) GetChats() ([]Chat, error) {
	rows, err := s.db.Query(`
		SELECT jid, name, is_group, last_message, last_at, msg_count
		FROM chats
		ORDER BY last_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chats []Chat
	for rows.Next() {
		var c Chat
		var lastAt int64
		var isGroup int
		if err := rows.Scan(&c.JID, &c.Name, &isGroup, &c.LastMessage, &lastAt, &c.MsgCount); err != nil {
			return nil, err
		}
		c.IsGroup = isGroup == 1
		c.LastAt = time.Unix(lastAt, 0)
		chats = append(chats, c)
	}
	return chats, rows.Err()
}

// GetChatMessages returns all messages in a chat, newest first.
func (s *Store) GetChatMessages(chatJID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT m.message_id, m.chat_jid, COALESCE(c.name,''), m.sender, m.sender_name,
		       m.text, m.timestamp, m.is_group, m.media_type, m.media_name
		FROM messages m
		LEFT JOIN chats c ON c.jid = m.chat_jid
		WHERE m.chat_jid = ?
		ORDER BY m.timestamp DESC
		LIMIT ?
	`, chatJID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// ChatTextForIndex returns a single concatenated string of the most recent
// messages in a chat — used to (re)build the semantic embedding.
func (s *Store) ChatTextForIndex(chatJID string, maxMessages int) (string, error) {
	if maxMessages <= 0 {
		maxMessages = 200
	}
	rows, err := s.db.Query(`
		SELECT sender_name, text FROM (
			SELECT sender_name, text, timestamp
			FROM messages
			WHERE chat_jid = ? AND text != ''
			ORDER BY timestamp DESC
			LIMIT ?
		) ORDER BY timestamp ASC
	`, chatJID, maxMessages)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var sb []byte
	for rows.Next() {
		var sender, text string
		if err := rows.Scan(&sender, &text); err != nil {
			return "", err
		}
		if sender != "" {
			sb = append(sb, sender+": "+text+"\n"...)
		} else {
			sb = append(sb, text+"\n"...)
		}
	}
	return string(sb), rows.Err()
}

// MessageCount returns total stored messages.
func (s *Store) MessageCount() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n)
	return n, err
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ftsQuery wraps a raw query string for FTS5 — each word is prefix-matched.
func ftsQuery(q string) string {
	// FTS5 MATCH syntax: wrap each token with double-quotes for exact phrase,
	// or append * for prefix. We do simple prefix matching on the whole phrase.
	return `"` + q + `"*`
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	var msgs []Message
	for rows.Next() {
		var m Message
		var ts int64
		var isGroup int
		if err := rows.Scan(
			&m.MessageID, &m.ChatJID, &m.ChatName,
			&m.Sender, &m.SenderName,
			&m.Text, &ts, &isGroup,
			&m.MediaType, &m.MediaName,
		); err != nil {
			return nil, err
		}
		m.Timestamp = time.Unix(ts, 0)
		m.IsGroup = isGroup == 1
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
