package api_test

import (
	"net/http"
	"testing"
)

// §7.1：首个注册用户 role=admin；第二个 role=user。
func TestRegisterFirstUserBecomesAdmin(t *testing.T) {
	env := newEnv(t)

	u1 := env.register(t, "alice", testPassword)
	if u1.Role != "admin" {
		t.Fatalf("first user role = %q, want admin", u1.Role)
	}
	u2 := env.register(t, "bob", testPassword)
	if u2.Role != "user" {
		t.Fatalf("second user role = %q, want user", u2.Role)
	}
}

// §7.1：已有用户且 allow_register=false 时注册被拒。
func TestRegisterRejectedWhenClosed(t *testing.T) {
	env := newEnv(t)
	env.register(t, "root", testPassword)
	token := env.login(t, "root", testPassword)
	env.putSettings(t, token, map[string]any{"allow_register": false})

	res, body := env.rawJSON(t, http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "late", "password": testPassword,
	}, "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("register with allow_register=false: status = %d, body = %s", res.StatusCode, body)
	}
}

// §7.1：用户数为 0 时 allow_register=false 仍可注册（首注兜底）。
func TestRegisterFirstUserWhenRegisterClosed(t *testing.T) {
	env := newEnvSeeded(t, func(env *testEnv) {
		if _, err := env.db.Exec(`INSERT INTO settings(key, value) VALUES
			('site_name','pixsy'),('allow_register','0'),('allow_upload','1'),
			('max_upload_mb','5'),('max_batch','10')
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`); err != nil {
			t.Fatalf("seed settings: %v", err)
		}
	})

	u := env.register(t, "first", testPassword)
	if u.Role != "admin" {
		t.Fatalf("first user role = %q, want admin", u.Role)
	}
}

// §7.1：重复用户名 409。
func TestRegisterDuplicateUsername(t *testing.T) {
	env := newEnv(t)
	env.register(t, "alice", testPassword)

	res, body := env.rawJSON(t, http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": "alice", "password": testPassword,
	}, "")
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate register: status = %d, body = %s", res.StatusCode, body)
	}
}

// §7.1：登录成功返回 token；错误密码 401。
func TestLogin(t *testing.T) {
	env := newEnv(t)
	env.register(t, "alice", testPassword)

	if token := env.login(t, "alice", testPassword); token == "" {
		t.Fatal("login returned empty token")
	}

	res, body := env.rawJSON(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "alice", "password": wrongPassword,
	}, "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad password: status = %d, body = %s", res.StatusCode, body)
	}
}

// §7.1：/auth/me 无 token 401。
func TestMeRequiresToken(t *testing.T) {
	env := newEnv(t)
	res, body := env.rawJSON(t, http.MethodGet, "/api/v1/auth/me", nil, "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me without token: status = %d, body = %s", res.StatusCode, body)
	}
}

// §7.1：/auth/me 带 token 返回当前用户。
func TestMeWithToken(t *testing.T) {
	env := newEnv(t)
	env.register(t, "alice", testPassword)
	token := env.login(t, "alice", testPassword)

	res, body := env.rawJSON(t, http.MethodGet, "/api/v1/auth/me", nil, token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("me: status = %d, body = %s", res.StatusCode, body)
	}
	var out struct {
		Data struct {
			User userOut `json:"user"`
		} `json:"data"`
	}
	decode(t, body, &out)
	if out.Data.User.Username != "alice" || out.Data.User.Role != "admin" {
		t.Fatalf("me user = %+v", out.Data.User)
	}
}

// §7.1：登录限速 5 次/分钟，超出返回 429。
func TestLoginRateLimited(t *testing.T) {
	env := newEnv(t)
	env.register(t, "alice", testPassword)

	var last int
	for i := 0; i < 8; i++ {
		res, _ := env.rawJSON(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
			"username": "alice", "password": wrongPassword,
		}, "")
		last = res.StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("after 8 attempts status = %d, want 429", last)
	}
}
