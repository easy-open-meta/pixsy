package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pixsy/pixsy/internal/api"
	"github.com/pixsy/pixsy/internal/auth"
	"github.com/pixsy/pixsy/internal/model"
	"github.com/pixsy/pixsy/internal/server"
	"github.com/pixsy/pixsy/internal/setting"
	"github.com/pixsy/pixsy/internal/storage"
)

const (
	testPassword  = "password123"
	wrongPassword = "wrong-password"
)

// testEnv 是临时数据目录 + 真实 SQLite 搭出的完整处理器（§7.1）。
type testEnv struct {
	t        *testing.T
	db       *sql.DB
	settings *setting.Store
	store    *storage.Store
	auth     *auth.Manager
	handler  http.Handler
}

type userOut struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func newEnv(t *testing.T) *testEnv { return newEnvSeeded(t, nil) }

// newEnvSeeded 允许在加载设置前播种数据行（用于模拟已存在的历史状态）。
func newEnvSeeded(t *testing.T, seed func(*testEnv)) *testEnv {
	t.Helper()
	dir := t.TempDir()

	db, err := model.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	mgr, err := auth.NewManager([]byte("test-secret-do-not-use"))
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}

	env := &testEnv{t: t, db: db, store: store, auth: mgr}
	if seed != nil {
		seed(env)
	}

	settings, err := setting.Load(context.Background(), db, 20)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	env.settings = settings

	// AllowPrivateHosts: 测试里的假 Lsky 是 httptest 的 127.0.0.1 服务。
	apiSrv := api.New(api.Options{DB: db, Settings: settings, Store: store, Auth: mgr, AllowPrivateHosts: true})
	srv := server.New(server.Options{API: apiSrv.Handler(), Store: store, DB: db})
	env.handler = srv.Handler()
	return env
}

// do 发送请求并返回响应与响应体。
func (e *testEnv) do(t *testing.T, req *http.Request) (*http.Response, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	res := rec.Result()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	res.Body.Close()
	return res, body
}

// get 发送匿名 GET 请求（图片直出、页面等）。
func (e *testEnv) get(path string) (*http.Response, []byte) {
	return e.getAuth(e.t, path, "")
}

func (e *testEnv) rawJSON(t *testing.T, method, path string, payload any, token string) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, rdr)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return e.do(t, req)
}

// getAuth 发送带 bearer token 的 GET 请求。
func (e *testEnv) getAuth(t *testing.T, path, token string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return e.do(t, req)
}

func decode(t *testing.T, body []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("decode %s: %v", string(body), err)
	}
}

func (e *testEnv) register(t *testing.T, username, password string) userOut {
	t.Helper()
	res, body := e.rawJSON(t, http.MethodPost, "/api/v1/auth/register", map[string]any{
		"username": username, "password": password,
	}, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register %s: status = %d, body = %s", username, res.StatusCode, body)
	}
	var out struct {
		Data struct {
			User userOut `json:"user"`
		} `json:"data"`
	}
	decode(t, body, &out)
	return out.Data.User
}

func (e *testEnv) login(t *testing.T, username, password string) string {
	t.Helper()
	res, body := e.rawJSON(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": username, "password": password,
	}, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status = %d, body = %s", username, res.StatusCode, body)
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decode(t, body, &out)
	if out.Data.Token == "" {
		t.Fatal("login returned empty token")
	}
	return out.Data.Token
}

func (e *testEnv) putSettings(t *testing.T, token string, patch map[string]any) setting.Values {
	t.Helper()
	res, body := e.rawJSON(t, http.MethodPut, "/api/v1/admin/settings", patch, token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put settings: status = %d, body = %s", res.StatusCode, body)
	}
	var out struct {
		Data setting.Values `json:"data"`
	}
	decode(t, body, &out)
	return out.Data
}

// uploadFile 是一个待上传的内存文件。
type uploadFile struct {
	name string
	data []byte
}

// pngBytes 生成一张 w×h 的 PNG。
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 5), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func (e *testEnv) upload(t *testing.T, token string, files ...uploadFile) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range files {
		part, err := mw.CreateFormFile("file", f.name)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(f.data); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/images", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return e.do(t, req)
}

// uploaded 是一张成功上传后的响应视图。
type uploaded struct {
	ID       int64  `json:"id"`
	Hash     string `json:"hash"`
	Ext      string `json:"ext"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
	ThumbURL string `json:"thumb_url"`
	UserID   int64  `json:"user_id"`
}

func decodeUploaded(t *testing.T, body []byte) []uploaded {
	t.Helper()
	var out struct {
		Data struct {
			Images []uploaded `json:"images"`
		} `json:"data"`
	}
	decode(t, body, &out)
	return out.Data.Images
}

// mustUpload 上传单个文件并返回响应视图。
func (e *testEnv) mustUpload(t *testing.T, token, name string, data []byte) uploaded {
	t.Helper()
	res, body := e.upload(t, token, uploadFile{name: name, data: data})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("upload %s: status = %d, body = %s", name, res.StatusCode, body)
	}
	imgs := decodeUploaded(t, body)
	if len(imgs) != 1 {
		t.Fatalf("upload returned %d images, want 1", len(imgs))
	}
	return imgs[0]
}
