package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// lskyStatusJSON 对应 GET /admin/migrate/lsky 的响应体。
type lskyStatusJSON struct {
	Running    bool   `json:"running"`
	Total      int    `json:"total"`
	Done       int    `json:"done"`
	Failed     int    `json:"failed"`
	Error      string `json:"error"`
	Mapping    int    `json:"mapping_count"`
	FinishedAt string `json:"finished_at"`
}

// fakeLsky 模拟 Lsky Pro 2.x 的 token / 图片清单 / 图片下载接口。
func fakeLsky(t *testing.T) *httptest.Server {
	t.Helper()
	pngs := map[string][]byte{
		"/i/a.png": pngBytes(t, 8, 8),
		"/i/b.png": pngBytes(t, 12, 6),
	}

	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/api/v1/tokens", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// 模拟 Laravel：没有 JSON Accept 时重定向到登录页的 HTML
		if r.Header.Get("Accept") != "application/json" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["email"] != "lsky@example.com" || in["password"] != "lsky-pass" {
			_, _ = w.Write([]byte(`{"status":false,"message":"邮箱或密码错误"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":true,"message":"success","data":{"token":"lsky-token"}}`))
	})

	mux.HandleFunc("/api/v1/images", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer lsky-token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":false,"message":"Unauthenticated.","data":{}}`))
			return
		}
		// size 用浮点字面量（70.0），复现 Lsky 模型把 size cast 成 float 的真实响应。
		items := fmt.Sprintf(`[
			{"name":"a.png","origin_name":"a.png","pathname":"2024/01/a.png","extension":"png","size":70.0,"mimetype":"image/png","links":{"url":"%[1]s/i/a.png"}},
			{"name":"b.png","origin_name":"b.png","pathname":"2024/01/b.png","extension":"png","size":34.0,"mimetype":"image/png","links":{"url":"%[1]s/i/b.png"}}
		]`, srv.URL)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":true,"message":"success","data":{"data":%s,"current_page":1,"last_page":1,"total":2}}`, items)
	})

	for path, data := range pngs {
		body := data
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(body)
		})
	}

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// waitLskyDone 轮询状态接口直到任务结束，返回最终的进度。
func (e *testEnv) waitLskyDone(t *testing.T, token string) lskyStatusJSON {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		res, body := e.getAuth(t, "/api/v1/admin/migrate/lsky", token)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status: HTTP %d, body = %s", res.StatusCode, body)
		}
		var out struct {
			Data lskyStatusJSON `json:"data"`
		}
		decode(t, body, &out)
		if !out.Data.Running {
			return out.Data
		}
		if time.Now().After(deadline) {
			t.Fatalf("迁移超时未结束: %s", body)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func TestLskyMigration(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword) // 首个用户自动成为管理员
	token := env.login(t, "admin", testPassword)

	lsky := fakeLsky(t)
	res, body := env.rawJSON(t, http.MethodPost, "/api/v1/admin/migrate/lsky", map[string]any{
		"base_url": lsky.URL + "/",
		"email":    "lsky@example.com",
		"password": "lsky-pass",
	}, token)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("start migration: HTTP %d, body = %s", res.StatusCode, body)
	}

	status := env.waitLskyDone(t, token)
	if status.Error != "" {
		t.Fatalf("迁移报错: %s", status.Error)
	}
	if status.Total != 2 || status.Done != 2 || status.Failed != 0 {
		t.Fatalf("迁移结果 total=%d done=%d failed=%d", status.Total, status.Done, status.Failed)
	}
	if status.Mapping != 2 {
		t.Fatalf("mapping_count = %d, want 2", status.Mapping)
	}

	// 图片已入库，且原图可访问。
	res, body = env.getAuth(t, "/api/v1/images", token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list images: HTTP %d, body = %s", res.StatusCode, body)
	}
	var list struct {
		Data struct {
			Images []uploaded `json:"images"`
		} `json:"data"`
	}
	decode(t, body, &list)
	if len(list.Data.Images) != 2 {
		t.Fatalf("图片数 = %d, want 2", len(list.Data.Images))
	}
	for _, img := range list.Data.Images {
		res, _ := env.get("/i/" + img.Hash + "." + img.Ext)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("原图 %s 不可访问: HTTP %d", img.Filename, res.StatusCode)
		}
	}

	// 映射 CSV 同时包含旧链接与新链接。
	res, csvBody := env.getAuth(t, "/api/v1/admin/migrate/lsky/csv", token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("csv: HTTP %d, body = %s", res.StatusCode, csvBody)
	}
	for _, want := range []string{"lsky_url", lsky.URL + "/i/a.png", lsky.URL + "/i/b.png", "ok"} {
		if !bytes.Contains(csvBody, []byte(want)) {
			t.Fatalf("CSV 缺少 %q:\n%s", want, csvBody)
		}
	}
}

func TestLskyMigrationRequiresAdmin(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	env.register(t, "alice", testPassword)
	alice := env.login(t, "alice", testPassword)

	res, body := env.rawJSON(t, http.MethodPost, "/api/v1/admin/migrate/lsky", map[string]any{
		"base_url": "https://lsky.example.com", "email": "a@example.com", "password": "x",
	}, alice)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("普通用户启动迁移应 403, got %d, body = %s", res.StatusCode, body)
	}
}

func TestLskyMigrationRejectsBadInput(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	token := env.login(t, "admin", testPassword)

	for name, payload := range map[string]map[string]any{
		"空地址":    {"email": "a@example.com", "password": "x"},
		"非 http": {"base_url": "ftp://lsky.example.com", "email": "a@example.com", "password": "x"},
		"缺邮箱":    {"base_url": "https://lsky.example.com", "password": "x"},
	} {
		t.Run(name, func(t *testing.T) {
			res, body := env.rawJSON(t, http.MethodPost, "/api/v1/admin/migrate/lsky", payload, token)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("应 400, got %d, body = %s", res.StatusCode, body)
			}
		})
	}
}
