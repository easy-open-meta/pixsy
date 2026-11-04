package model

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO, keeps cross-compiling possible)
)

// dsn pragmas matter for a low-spec box: WAL keeps readers unblocked by the
// writer, busy_timeout absorbs write contention, and foreign_keys enforces the
// images.user_id reference.
const dsnSuffix = "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)" +
	"&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"

// Open opens (creating if needed) the SQLite database at dataDir/pixsy.db and
// applies the schema.
func Open(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dbPath := filepath.Join(dataDir, "pixsy.db")
	db, err := sql.Open("sqlite", dbPath+dsnSuffix)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite is single-writer; a small pool plus context timeouts avoids
	// "database is locked" under concurrent uploads.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)

	if err := Migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// schema is idempotent: CREATE TABLE/INDEX IF NOT EXISTS (§3.1).
const schema = `
CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT    NOT NULL UNIQUE,
  password_hash TEXT    NOT NULL,
  role          TEXT    NOT NULL DEFAULT 'user' CHECK (role IN ('admin','user')),
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS images (
  id         INTEGER  PRIMARY KEY AUTOINCREMENT,
  hash       TEXT     NOT NULL,
  ext        TEXT     NOT NULL,
  filename   TEXT     NOT NULL,
  mime       TEXT     NOT NULL,
  size       INTEGER  NOT NULL,
  width      INTEGER  NOT NULL DEFAULT 0,
  height     INTEGER  NOT NULL DEFAULT 0,
  user_id    INTEGER  NOT NULL REFERENCES users(id),
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_images_user ON images(user_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_images_hash ON images(hash);

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

// Migrate creates missing tables and indexes.
func Migrate(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

// Ping verifies the database is reachable.
func Ping(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}
