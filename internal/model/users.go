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
// "Admin" and "admin" cannot become two accounts in practice.
func GetUserByName(ctx context.Context, db *sql.DB, username string) (*User, error) {
	var u User
	err := db.QueryRowContext(ctx,
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
