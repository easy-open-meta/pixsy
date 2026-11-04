// Package model holds pixsy's data model and the SQLite schema/migration.
package model

import "time"

// Roles as declared by the users.role CHECK constraint (§3.1).
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User is an account row. PasswordHash is bcrypt and must never be serialized.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

// Image is one upload record. Several records may share the same Hash (§3.1):
// the file on disk is stored once, so deleting a record only removes the file
// when no other record references that hash.
type Image struct {
	ID        int64     `json:"id"`
	Hash      string    `json:"hash"`
	Ext       string    `json:"ext"`
	Filename  string    `json:"filename"`
	Mime      string    `json:"mime"`
	Size      int64     `json:"size"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}
