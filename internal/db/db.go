package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Open opens (or creates) the SQLite database and runs migrations.
func Open(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dbPath := filepath.Join(dataDir, "copa.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite WAL supports concurrent reads but single writer
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS participant (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    token            TEXT    NOT NULL UNIQUE,
    name             TEXT    NOT NULL,
    nickname         TEXT    NOT NULL DEFAULT '',
    photo_path       TEXT    NOT NULL DEFAULT '',
    active           INTEGER NOT NULL DEFAULT 1,
    claimed_device_id INTEGER REFERENCES device(id),
    created_at       TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS device (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    cookie_token   TEXT    NOT NULL UNIQUE,
    participant_id INTEGER NOT NULL REFERENCES participant(id),
    created_at     TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS collection (
    owner_id     INTEGER NOT NULL REFERENCES participant(id),
    sticker_id   INTEGER NOT NULL REFERENCES participant(id),
    collected_at TEXT    NOT NULL,
    PRIMARY KEY (owner_id, sticker_id)
);

CREATE TABLE IF NOT EXISTS setting (
    id                   INTEGER PRIMARY KEY CHECK (id = 1),
    base_url             TEXT    NOT NULL DEFAULT 'http://localhost:8080',
    kickoff_at           TEXT,
    roster_locked        INTEGER NOT NULL DEFAULT 0,
    admin_password_hash  TEXT    NOT NULL DEFAULT ''
);

INSERT OR IGNORE INTO setting (id, base_url, kickoff_at, roster_locked, admin_password_hash)
VALUES (1, 'http://localhost:8080', NULL, 0, '');

CREATE INDEX IF NOT EXISTS idx_collection_owner ON collection(owner_id);
CREATE INDEX IF NOT EXISTS idx_collection_sticker ON collection(sticker_id);
CREATE INDEX IF NOT EXISTS idx_participant_token ON participant(token);
CREATE INDEX IF NOT EXISTS idx_device_cookie ON device(cookie_token);
`
	_, err := db.Exec(schema)
	return err
}

// TimeToString converts time.Time to RFC3339 string for SQLite storage.
func TimeToString(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// StringToTime parses a RFC3339 string from SQLite.
func StringToTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}
