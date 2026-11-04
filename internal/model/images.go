package model

import (
	"context"
	"database/sql"
)

const scanImageCols = `id, hash, ext, filename, mime, size, width, height, user_id, created_at`

func scanImage(row interface {
	Scan(dest ...any) error
}) (*Image, error) {
	var img Image
	if err := row.Scan(&img.ID, &img.Hash, &img.Ext, &img.Filename, &img.Mime,
		&img.Size, &img.Width, &img.Height, &img.UserID, &img.CreatedAt); err != nil {
		return nil, err
	}
	return &img, nil
}

// FindImageByUserHash returns the uploader's existing record for the same
// content, so re-uploading a file is idempotent (§3.4 step 6).
func FindImageByUserHash(ctx context.Context, db *sql.DB, userID int64, hash string) (*Image, error) {
	row := db.QueryRowContext(ctx,
		`SELECT `+scanImageCols+` FROM images WHERE user_id = ? AND hash = ? ORDER BY id LIMIT 1`,
		userID, hash)
	return scanImage(row)
}

// InsertImage writes a new images row and fills ID/CreatedAt.
func InsertImage(ctx context.Context, db *sql.DB, img *Image) error {
	res, err := db.ExecContext(ctx,
		`INSERT INTO images (hash, ext, filename, mime, size, width, height, user_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		img.Hash, img.Ext, img.Filename, img.Mime, img.Size, img.Width, img.Height, img.UserID)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	img.ID = id
	return db.QueryRowContext(ctx, `SELECT created_at FROM images WHERE id = ?`, id).Scan(&img.CreatedAt)
}

// GetImage loads one image by id; sql.ErrNoRows when absent.
func GetImage(ctx context.Context, db *sql.DB, id int64) (*Image, error) {
	row := db.QueryRowContext(ctx, `SELECT `+scanImageCols+` FROM images WHERE id = ?`, id)
	return scanImage(row)
}

// ListImages returns one page plus the total row count. all=true ignores the
// owner filter (admin scope=all, §3.3).
func ListImages(ctx context.Context, db *sql.DB, userID int64, all bool, limit, offset int) ([]*Image, int, error) {
	where := ` WHERE user_id = ?`
	args := []any{userID}
	if all {
		where = ``
		args = nil
	}

	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM images`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := db.QueryContext(ctx,
		`SELECT `+scanImageCols+` FROM images`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]*Image, 0, limit)
	for rows.Next() {
		img, err := scanImage(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, img)
	}
	return items, total, rows.Err()
}

// DeleteImage removes one record; sql.ErrNoRows when the id does not exist.
func DeleteImage(ctx context.Context, db *sql.DB, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM images WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// CountImagesByHash reports how many records still reference the hash; the
// on-disk file may only be removed when it drops to zero (§3.1).
func CountImagesByHash(ctx context.Context, db *sql.DB, hash string) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM images WHERE hash = ?`, hash).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
