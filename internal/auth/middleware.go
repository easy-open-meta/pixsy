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
// X-Forwarded-For 只有在连接来自可信代理（见 config.TrustedProxies）时才采信，
// 且取从右往左第一个非可信地址——客户端伪造的最左段因此失效。
// security: 直接暴露部署时完全忽略 XFF，防止伪造头绕过登录限速。
func ClientIP(r *http.Request, trusted []*net.IPNet) string {
	remote := hostOnly(r.RemoteAddr)
	if len(trusted) == 0 {
		return remote
	}
	ip := net.ParseIP(remote)
	if ip == nil || !ipInNets(ip, trusted) {
		return remote
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remote
	}
	// XFF 从左到右为 client, proxy1, proxy2...；最右端由可信代理追加。
	// 从右往左跳过可信代理，取第一个不可信地址作为客户端真实 IP。
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		if hop := normalizeIP(parts[i]); hop != "" {
			if hopIP := net.ParseIP(hop); hopIP != nil && ipInNets(hopIP, trusted) {
				continue
			}
			return hop
		}
		// security: 段格式不合法时退回连接地址，避免 net.IP(nil).String()
		// 把异常请求挤进 "<nil>" 同一个桶。
		return remote
	}
	return remote
}

func ipInNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// RemoteAddrInNets reports whether the TCP peer address falls in nets.
// 供非 auth 场景（如外链 host 推断）复用同一可信代理判定。
func RemoteAddrInNets(r *http.Request, nets []*net.IPNet) bool {
	if len(nets) == 0 {
		return false
	}
	ip := net.ParseIP(hostOnly(r.RemoteAddr))
	return ip != nil && ipInNets(ip, nets)
}

// normalizeIP accepts "1.2.3.4", "1.2.3.4:443" and "[::1]:443"; "" when unusable.
func normalizeIP(s string) string {
	s = strings.TrimSpace(s)
	if ip := net.ParseIP(s); ip != nil {
		return ip.String()
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
	}
	return ""
}

// hostOnly strips the port from RemoteAddr, tolerating addresses without one.
func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
