// Package auth 提供密码哈希、JWT 签发校验、认证中间件与登录限速（§3.2）。
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	// bcryptCost follows §3.2.
	bcryptCost = 10
	// TokenTTL is the JWT lifetime (§3.2: 7 days).
	TokenTTL = 7 * 24 * time.Hour

	MinPasswordLen = 8
	MinUsernameLen = 3
	MaxUsernameLen = 32
	// minSecretLen keeps auto-generated and explicit secrets alike unguessable.
	minSecretLen = 16

	// tokenIssuer / tokenAudience 绑定 JWT 的签发方与受众，防跨服务混用密钥。
	tokenIssuer   = "pixsy"
	tokenAudience = "pixsy-api"

	// SessionCookieName 是登录成功时下发的会话 Cookie（值即 JWT，任何角色）。
	// 图片直出路由用它识别登录用户、放行外链防盗链（§3.5）——<img> 加载与
	// target=_blank 导航带不上 Authorization 头，只能借助浏览器自动携带的 Cookie。
	SessionCookieName = "pixsy_session"
)

// ErrInvalidToken is returned by Parse for any unusable token.
var ErrInvalidToken = errors.New("invalid token")

var (
	usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

	// ErrInvalidUsername / ErrInvalidPassword carry user-facing messages.
	ErrInvalidUsername = errors.New("用户名需为 3~32 位字母、数字或下划线")
	ErrInvalidPassword = errors.New("密码至少 8 位")
	ErrPasswordTooLong = errors.New("密码最多 72 字节（约 24 个汉字）")
)

// Manager issues and verifies HS256 tokens.
type Manager struct {
	secret []byte
}

// NewManager wraps a signing secret; it must be long enough to be safe.
func NewManager(secret []byte) (*Manager, error) {
	if len(secret) < minSecretLen {
		return nil, fmt.Errorf("jwt secret too short: need >= %d bytes", minSecretLen)
	}
	return &Manager{secret: secret}, nil
}

// Claims is the JWT payload (§3.2: uid + role).
type Claims struct {
	UID  int64  `json:"uid"`
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// Issue signs a 7-day token for uid/role.
func (m *Manager) Issue(uid int64, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		UID:  uid,
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(uid, 10),
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(TokenTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

// Parse validates a token and returns its claims.
func (m *Manager) Parse(token string) (*Claims, error) {
	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		// security: 绑定签发方/受众——同一 secret 被其他服务复用时，
		// 其他服务签发的 token 不会被本服务接受。
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenAudience))
	if err != nil || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// HashPassword hashes with bcrypt cost 10.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// CheckPassword reports whether password matches hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ValidateUsername enforces 3~32 chars of [A-Za-z0-9_].
func ValidateUsername(name string) error {
	if utf8.RuneCountInString(name) < MinUsernameLen || len(name) > MaxUsernameLen || !usernameRe.MatchString(name) {
		return ErrInvalidUsername
	}
	return nil
}

// ValidatePassword enforces a minimum length. bcrypt 只取前 72 字节，
// 超长密码会在注册时触发 ErrPasswordTooLong（500）——这里提前拦下。
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLen {
		return ErrInvalidPassword
	}
	if len(password) > 72 {
		return ErrPasswordTooLong
	}
	return nil
}

// NormalizeUsername trims surrounding spaces so " admin " and "admin" are the
// same account.
func NormalizeUsername(name string) string { return strings.TrimSpace(name) }

// LoadSecret returns the JWT signing secret: the explicit value when provided,
// otherwise dataDir/jwt.secret (created with 0600 permissions on first run,
// §3.2 / §3.8).
func LoadSecret(dataDir, explicit string) ([]byte, error) {
	if s := strings.TrimSpace(explicit); s != "" {
		return []byte(s), nil
	}
	path := filepath.Join(dataDir, "jwt.secret")
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		// Best effort：历史文件可能是宽松权限（umask 0022 建的旧版本），
		// 读取时顺手收紧到 0600；失败仅告警不阻塞启动。
		if fi, ferr := os.Stat(path); ferr == nil && fi.Mode().Perm() != 0o600 {
			if cerr := os.Chmod(path, 0o600); cerr == nil {
				slog.Info("tightened jwt.secret permissions", "path", path, "mode", "0600")
			}
		}
		return []byte(strings.TrimSpace(string(b))), nil
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read jwt secret: %w", err)
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generate jwt secret: %w", err)
	}
	secret := hex.EncodeToString(buf)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		return nil, fmt.Errorf("write jwt secret: %w", err)
	}
	// Best effort: os.WriteFile honours umask, re-tighten the mode explicitly.
	_ = os.Chmod(path, 0o600)
	return []byte(secret), nil
}
