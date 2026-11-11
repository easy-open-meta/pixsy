package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/pixsy/pixsy/internal/model"
)

// adminEnv 搭好一个管理员 + 若干普通用户的基础环境，返回管理员 token 与用户信息。
func adminEnv(t *testing.T) (*testEnv, string, userOut) {
	t.Helper()
	env := newEnv(t)
	admin := env.register(t, "admin", testPassword)
	token := env.login(t, "admin", testPassword)
	return env, token, admin
}

// adminCreate 由管理员创建一个用户，失败即 Fatal。
func adminCreate(t *testing.T, env *testEnv, token, username, password, role string) userOut {
	t.Helper()
	res, body := env.rawJSON(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": username, "password": password, "role": role,
	}, token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("admin create %s: status = %d, body = %s", username, res.StatusCode, body)
	}
	var out struct {
		Data struct {
			User userOut `json:"user"`
		} `json:"data"`
	}
	decode(t, body, &out)
	return out.Data.User
}

type userListOut struct {
	Data struct {
		Items []userOut `json:"items"`
		Total int       `json:"total"`
		Page  int       `json:"page"`
		Limit int       `json:"limit"`
	} `json:"data"`
}

func adminList(t *testing.T, env *testEnv, token, query string) (*http.Response, userListOut) {
	t.Helper()
	res, body := env.getAuth(t, "/api/v1/admin/users"+query, token)
	var out userListOut
	decode(t, body, &out)
	return res, out
}

// TestAdminUsersListPaginationAndSearch 覆盖分页默认值、翻页与用户名模糊搜索。
func TestAdminUsersListPaginationAndSearch(t *testing.T) {
	env, token, _ := adminEnv(t)
	for i := 0; i < 25; i++ {
		adminCreate(t, env, token, fmt.Sprintf("user%02d", i), testPassword, model.RoleUser)
	}

	// 默认 page=1、limit=20。
	res, out := adminList(t, env, token, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d", res.StatusCode)
	}
	if out.Data.Total != 26 || out.Data.Page != 1 || out.Data.Limit != 20 {
		t.Fatalf("defaults: total=%d page=%d limit=%d, want 26/1/20",
			out.Data.Total, out.Data.Page, out.Data.Limit)
	}
	if len(out.Data.Items) != 20 {
		t.Fatalf("items len = %d, want 20", len(out.Data.Items))
	}

	// 第二页拿到剩余 6 条。
	_, out2 := adminList(t, env, token, "?page=2")
	if len(out2.Data.Items) != 6 || out2.Data.Total != 26 {
		t.Fatalf("page 2: items=%d total=%d, want 6/26", len(out2.Data.Items), out2.Data.Total)
	}

	// search 模糊匹配。
	_, out3 := adminList(t, env, token, "?search=user0")
	if out3.Data.Total != 10 { // user00..user09
		t.Fatalf("search user0: total=%d, want 10", out3.Data.Total)
	}
	// 精确命中一个。
	_, out4 := adminList(t, env, token, "?search=user07")
	if out4.Data.Total != 1 || len(out4.Data.Items) != 1 || out4.Data.Items[0].Username != "user07" {
		t.Fatalf("search user07: total=%d items=%v", out4.Data.Total, out4.Data.Items)
	}
	// LIKE 通配符按字面处理，不放大结果集（URL 里 % 需编码为 %25）。
	_, out5 := adminList(t, env, token, "?search=user%25")
	if out5.Data.Total != 0 {
		t.Fatalf("search with %% wildcard: total=%d, want 0", out5.Data.Total)
	}
}

// TestAdminUsersCreateValidation 覆盖新建用户的角色默认值与参数校验。
func TestAdminUsersCreateValidation(t *testing.T) {
	env, token, _ := adminEnv(t)

	// 缺省 role 默认为 user。
	u := adminCreate(t, env, token, "plainuser", testPassword, "")
	if u.Role != model.RoleUser {
		t.Fatalf("default role = %q, want user", u.Role)
	}

	// 非法角色。
	res, body := env.rawJSON(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "badrole", "password": testPassword, "role": "root",
	}, token)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad role: status = %d, body = %s", res.StatusCode, body)
	}

	// 非法用户名。
	res, body = env.rawJSON(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "ab", "password": testPassword,
	}, token)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad username: status = %d, body = %s", res.StatusCode, body)
	}

	// 非法密码。
	res, body = env.rawJSON(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "shortpw", "password": "short",
	}, token)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad password: status = %d, body = %s", res.StatusCode, body)
	}
}

// TestAdminUsersCreateConflict 重名（含大小写不敏感）返回 409。
func TestAdminUsersCreateConflict(t *testing.T) {
	env, token, _ := adminEnv(t)
	adminCreate(t, env, token, "alice", testPassword, model.RoleUser)

	res, body := env.rawJSON(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "alice", "password": testPassword,
	}, token)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate: status = %d, body = %s", res.StatusCode, body)
	}

	// 大小写不同也算重名（与登录的 NOCASE 匹配一致）。
	res, body = env.rawJSON(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": "ALICE", "password": testPassword,
	}, token)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("case-insensitive duplicate: status = %d, body = %s", res.StatusCode, body)
	}
}

// TestAdminUsersUpdateRoleAndPassword 覆盖角色更新、密码更新与 404。
func TestAdminUsersUpdateRoleAndPassword(t *testing.T) {
	env, token, _ := adminEnv(t)
	u := adminCreate(t, env, token, "bob", testPassword, model.RoleUser)

	// 升级为 admin。
	res, body := env.rawJSON(t, http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%d", u.ID),
		map[string]any{"role": model.RoleAdmin}, token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("promote: status = %d, body = %s", res.StatusCode, body)
	}
	var out struct {
		Data struct {
			User userOut `json:"user"`
		} `json:"data"`
	}
	decode(t, body, &out)
	if out.Data.User.Role != model.RoleAdmin {
		t.Fatalf("role = %q, want admin", out.Data.User.Role)
	}

	// 新角色立即生效：bob 用旧密码登录后应能访问 admin 接口。
	bobToken := env.login(t, "bob", testPassword)
	res, _ = env.getAuth(t, "/api/v1/admin/users", bobToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("promoted user list: status = %d", res.StatusCode)
	}

	// 改密码：旧密码失效、新密码可登录。
	res, body = env.rawJSON(t, http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%d", u.ID),
		map[string]any{"password": "new-password-9"}, token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("change password: status = %d, body = %s", res.StatusCode, body)
	}
	if res2, _ := env.rawJSON(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "bob", "password": testPassword,
	}, ""); res2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password still valid: status = %d", res2.StatusCode)
	}
	if res3, _ := env.rawJSON(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "bob", "password": "new-password-9",
	}, ""); res3.StatusCode != http.StatusOK {
		t.Fatalf("new password rejected: status = %d", res3.StatusCode)
	}

	// 空更新体。
	res, body = env.rawJSON(t, http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%d", u.ID),
		map[string]any{}, token)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty patch: status = %d, body = %s", res.StatusCode, body)
	}

	// 不存在的用户。
	res, body = env.rawJSON(t, http.MethodPut, "/api/v1/admin/users/99999",
		map[string]any{"role": model.RoleUser}, token)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing user: status = %d, body = %s", res.StatusCode, body)
	}
}

// TestAdminUsersSelfDemoteForbidden 管理员不能把自己降级为普通用户。
func TestAdminUsersSelfDemoteForbidden(t *testing.T) {
	env, token, admin := adminEnv(t)

	res, body := env.rawJSON(t, http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%d", admin.ID),
		map[string]any{"role": model.RoleUser}, token)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("self demote: status = %d, body = %s", res.StatusCode, body)
	}

	// 改自己的密码是允许的。
	res, body = env.rawJSON(t, http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%d", admin.ID),
		map[string]any{"password": "changed-pw-1"}, token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("self password change: status = %d, body = %s", res.StatusCode, body)
	}
}

// TestAdminUsersDeleteCascades 覆盖删除用户、级联清理图片记录与磁盘文件。
func TestAdminUsersDeleteCascades(t *testing.T) {
	env, token, admin := adminEnv(t)
	victim := adminCreate(t, env, token, "victim", testPassword, model.RoleUser)
	adminCreate(t, env, token, "keeper", testPassword, model.RoleUser)

	victimToken := env.login(t, "victim", testPassword)
	keeperToken := env.login(t, "keeper", testPassword)

	// victim 与 keeper 上传同一张图（同 hash，跨用户共享磁盘文件），
	// 再各自上传一张独有图。
	shared := pngBytes(t, 8, 8)
	env.mustUpload(t, victimToken, "shared.png", shared)
	env.mustUpload(t, keeperToken, "shared.png", shared)
	only := env.mustUpload(t, victimToken, "only.png", pngBytes(t, 9, 9))

	// 删除 victim。
	res, body := env.rawJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/admin/users/%d", victim.ID), nil, token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete: status = %d, body = %s", res.StatusCode, body)
	}

	// victim 的图片记录全部级联删除。
	var count struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	res, body = env.getAuth(t, "/api/v1/images?page_size=100", keeperToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("keeper list: status = %d, body = %s", res.StatusCode, body)
	}
	decode(t, body, &count)
	if count.Data.Total != 1 {
		t.Fatalf("keeper images total = %d, want 1 (shared.png)", count.Data.Total)
	}

	// 共享 hash 的磁盘文件必须保留（keeper 仍引用），独有图的文件被清理。
	if f, _, err := env.store.OpenFile(only.Hash, only.Ext); err == nil {
		f.Close()
		t.Fatal("orphan file for deleted user's unique image still exists")
	}
	sharedHash := env.mustUpload(t, keeperToken, "shared2.png", shared).Hash
	if f, _, err := env.store.OpenFile(sharedHash, "png"); err != nil {
		t.Fatal("shared file was removed while keeper still references it")
	} else {
		f.Close()
	}

	// victim 的登录态失效（用户行已删）。
	if res, _ := env.getAuth(t, "/api/v1/auth/me", victimToken); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("deleted user token still valid: status = %d", res.StatusCode)
	}

	// 管理员不能删除自己。
	res, body = env.rawJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/admin/users/%d", admin.ID), nil, token)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("self delete: status = %d, body = %s", res.StatusCode, body)
	}

	// 删除不存在的用户返回 404。
	res, body = env.rawJSON(t, http.MethodDelete, "/api/v1/admin/users/99999", nil, token)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing: status = %d, body = %s", res.StatusCode, body)
	}
}

// TestAdminUsersRequireAdmin 非管理员访问全部四个接口都被 403 拦截。
func TestAdminUsersRequireAdmin(t *testing.T) {
	env, adminToken, _ := adminEnv(t)
	plain := adminCreate(t, env, adminToken, "plain", testPassword, model.RoleUser)
	plainToken := env.login(t, "plain", testPassword)

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"list", http.MethodGet, "/api/v1/admin/users"},
		{"create", http.MethodPost, "/api/v1/admin/users"},
		{"update", http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%d", plain.ID)},
		{"delete", http.MethodDelete, fmt.Sprintf("/api/v1/admin/users/%d", plain.ID)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 普通用户 → 403。
			res, body := env.rawJSON(t, tc.method, tc.path, map[string]any{"role": model.RoleAdmin}, plainToken)
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("%s as user: status = %d, body = %s", tc.name, res.StatusCode, body)
			}
			// 未登录 → 401。
			res, body = env.rawJSON(t, tc.method, tc.path, map[string]any{"role": model.RoleAdmin}, "")
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s anonymous: status = %d, body = %s", tc.name, res.StatusCode, body)
			}
		})
	}
}
