package model

import (
	"context"
	"database/sql"
)

// Alias 把旧站点的 URL 路径（如 Lsky 的 2025/06/20/x.png）映射到内容哈希。
// 路径不变、域名换掉即可让历史文章里的图片继续可用。
type Alias struct {
	Hash string
	Ext  string
}

// InsertAlias 记录路径别名；同一路径重复导入时以最新内容为准。
func InsertAlias(ctx context.Context, db *sql.DB, alias, hash, ext string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO image_aliases(alias, hash, ext) VALUES(?, ?, ?)
		 ON CONFLICT(alias) DO UPDATE SET hash = excluded.hash, ext = excluded.ext`,
		alias, hash, ext)
	return err
}

// FindAlias 按旧路径查哈希；不存在返回 sql.ErrNoRows。
func FindAlias(ctx context.Context, db *sql.DB, alias string) (*Alias, error) {
	var a Alias
	if err := db.QueryRowContext(ctx,
		`SELECT hash, ext FROM image_aliases WHERE alias = ?`, alias).Scan(&a.Hash, &a.Ext); err != nil {
		return nil, err
	}
	return &a, nil
}

// DeleteAliasesByHash 在磁盘文件被删除时清理指向它的别名。
func DeleteAliasesByHash(ctx context.Context, db *sql.DB, hash string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM image_aliases WHERE hash = ?`, hash)
	return err
}
