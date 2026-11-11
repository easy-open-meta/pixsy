package model

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrUsernameTaken maps to HTTP 409 (§3.3).
var ErrUsernameTaken = errors.New("username already taken")

// scanUserCols is shared by every user SELECT.
const scanUserCols = `id, username, password_hash, role, created_at`

// CountUsers returns the total number of rows in users. Zero means the next
// registration becomes the first super admin (§3.2).
func CountUsers(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// CountUsersTx is CountUsers inside a transaction, so the first-admin decision
// and the insert share one snapshot.
func CountUsersTx(ctx context.Context, tx *sql.Tx) (int, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// CreateUserTx inserts a user with the given role inside tx.
func CreateUserTx(ctx context.Context, tx *sql.Tx, username, passwordHash, role string) (*User, error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`,
		username, passwordHash, role)
	if err != nil {
		if IsUniqueViolation(err) {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	u := &User{ID: id, Username: username, PasswordHash: passwordHash, Role: role}
	// created_at is defaulted by SQLite; read it back so the response is complete.
	if err := tx.QueryRowContext(ctx,
		`SELECT created_at FROM users WHERE id = ?`, id).Scan(&u.CreatedAt); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return u, nil
}

// GetUserByID loads a user by primary key; ErrNoRows when absent.
func GetUserByID(ctx context.Context, db *sql.DB, id int64) (*User, error) {
	var u User
	err := db.QueryRowContext(ctx,
		`SELECT `+scanUserCols+` FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUserByName loads a user for login. Names are matched case-insensitively so
// "Admin" and "admin" cannot become two accounts in practice. q 可以是
// *sql.DB 或 *sql.Tx，供创建用户前的重名预检查在事务内复用。
func GetUserByName(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, username string) (*User, error) {
	var u User
	err := q.QueryRowContext(ctx,
		`SELECT `+scanUserCols+` FROM users WHERE username = ? COLLATE NOCASE`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// IsUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
// modernc.org/sqlite wraps the constraint name into the message, so match on
// that instead of a vendor error code.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "constraint failed: unique")
}

// ---------------------------------------------------------------- Admin 用户管理

// escapeLike 转义 LIKE 通配符，配合 ESCAPE '\' 让搜索词按字面匹配，
// 防止用户输入的 %/_ 被当成通配符扫全表。
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// ListUsers 返回一页用户与匹配总数；search 非空时按用户名模糊匹配。
func ListUsers(ctx context.Context, db *sql.DB, page, limit int, search string) ([]*User, int, error) {
	// 分页参数来自 HTTP 查询串：夹紧防止异常值算出负 offset。
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 1
	}
	where := ""
	args := []any{}
	if term := strings.TrimSpace(search); term != "" {
		where = ` WHERE username LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(term)+"%")
	}

	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := db.QueryContext(ctx,
		`SELECT `+scanUserCols+` FROM users`+where+` ORDER BY id LIMIT ? OFFSET ?`,
		append(args, limit, (page-1)*limit)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]*User, 0, limit)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, &u)
	}
	return items, total, rows.Err()
}

// UpdateUser 按 pointer 是否非 nil 决定更新哪些列；两者都为 nil 时原样返回
// 当前用户。用户不存在返回 sql.ErrNoRows。
func UpdateUser(ctx context.Context, db *sql.DB, id int64, role, passwordHash *string) (*User, error) {
	if role == nil && passwordHash == nil {
		return GetUserByID(ctx, db, id)
	}
	sets := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if role != nil {
		sets = append(sets, `role = ?`)
		args = append(args, *role)
	}
	if passwordHash != nil {
		sets = append(sets, `password_hash = ?`)
		args = append(args, *passwordHash)
	}
	args = append(args, id)
	res, err := db.ExecContext(ctx,
		`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, sql.ErrNoRows
	}
	return GetUserByID(ctx, db, id)
}

// FileRef 是删除用户时需要检查的磁盘文件引用。内容寻址存储下同一 hash
// 可能被多个用户的记录共享，只有引用清零才允许删除物理文件。
type FileRef struct {
	Hash string
	Ext  string
}

// ListUserFiles 返回用户上传过的去重 (hash, ext) 列表，供删除后清理磁盘。
func ListUserFiles(ctx context.Context, db *sql.DB, userID int64) ([]FileRef, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT hash, ext FROM images WHERE user_id = ? GROUP BY hash`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	refs := []FileRef{}
	for rows.Next() {
		var f FileRef
		if err := rows.Scan(&f.Hash, &f.Ext); err != nil {
			return nil, err
		}
		refs = append(refs, f)
	}
	return refs, rows.Err()
}

// DeleteUser 在一个事务里先删该用户的图片记录再删用户行（foreign_keys
// 开启时顺序不能反），用户不存在返回 sql.ErrNoRows。物理文件由调用方
// 按 ListUserFiles + CountImagesByHash 的结果清理，存储层不进 model。
func DeleteUser(ctx context.Context, db *sql.DB, id int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM images WHERE user_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
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
	return tx.Commit()
}
