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
// images.user_id reference. _txlock=immediate makes transactions take the
// write lock up front — WAL 下 deferred 事务「先读后写」升级会撞
// SQLITE_BUSY_SNAPSHOT（busy_timeout 不生效），并发注册会 500。
const dsnSuffix = "?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)" +
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
-- 同用户同内容幂等（§3.4 step 6）：并发上传同文件靠唯一索引兜底，
-- 否则 record() 撞约束回读的注释只是空想，会产生重复行。
CREATE UNIQUE INDEX IF NOT EXISTS uq_images_user_hash ON images(user_id, hash);

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- 旧站点（如 Lsky Pro）的 URL 路径 → 内容哈希，用于无缝衔接已发布的旧链接。
CREATE TABLE IF NOT EXISTS image_aliases (
  alias      TEXT PRIMARY KEY,
  hash       TEXT NOT NULL,
  ext        TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_aliases_hash ON image_aliases(hash);
`

// Migrate creates missing tables and indexes.
func Migrate(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	// uq_images_user_hash 是后加的唯一索引：建索引前先按 (user_id, hash)
	// 去重存量数据（保留最小 id），否则历史库里已有重复行时建索引失败。
	if _, err := db.ExecContext(ctx, dedupeImagesSQL); err != nil {
		return fmt.Errorf("dedupe images before unique index: %w", err)
	}
	return nil
}

// dedupeImagesSQL 删除同 (user_id, hash) 的多余记录（幂等：无重复时删 0 行）。
const dedupeImagesSQL = `
DELETE FROM images WHERE id NOT IN (
  SELECT MIN(id) FROM images GROUP BY user_id, hash
);`

// Ping verifies the database is reachable.
func Ping(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}
