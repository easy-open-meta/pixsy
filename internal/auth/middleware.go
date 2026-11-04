package auth

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/pixsy/pixsy/internal/httpx"
	"github.com/pixsy/pixsy/internal/model"
)

type ctxKey struct{}

// UserFrom returns the authenticated user attached by Require.
func UserFrom(ctx context.Context) *model.User {
	u, _ := ctx.Value(ctxKey{}).(*model.User)
	return u
}

// WithUser attaches a user to ctx (handy for tests and internal calls).
func WithUser(ctx context.Context, u *model.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// Require rejects requests without a valid Bearer token (401) and loads the
// current user into the request context (§3.2).
func Require(m *Manager, db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				httpx.Fail(w, http.StatusUnauthorized, "未登录")
				return
			}
			claims, err := m.Parse(token)
			if err != nil {
				httpx.Fail(w, http.StatusUnauthorized, "登录状态无效，请重新登录")
				return
			}
			u, err := model.GetUserByID(r.Context(), db, claims.UID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					httpx.Fail(w, http.StatusUnauthorized, "用户不存在")
					return
				}
				httpx.Fail(w, http.StatusInternalServerError, "读取用户失败")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
		})
	}
}

// RequireAdmin must run after Require.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFrom(r.Context())
		if u == nil {
			httpx.Fail(w, http.StatusUnauthorized, "未登录")
			return
		}
		if u.Role != model.RoleAdmin {
			httpx.Fail(w, http.StatusForbidden, "需要超级管理员权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

// ClientIP extracts the caller address used as the rate-limit key.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		if v := strings.TrimSpace(xff); v != "" {
			return net.ParseIP(v).String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
