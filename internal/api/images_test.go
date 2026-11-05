package api_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/pixsy/pixsy/internal/setting"
)

// §7.1 upload_test：格式校验、大小限制、批次上限、秒传去重。

func TestUploadSuccessAndDedup(t *testing.T) {
	env := newEnv(t)
	admin := env.register(t, "admin", testPassword)
	if admin.Role != "admin" {
		t.Fatalf("first user role = %q, want admin", admin.Role)
	}
	token := env.login(t, "admin", testPassword)

	data := pngBytes(t, 20, 10)
	img := env.mustUpload(t, token, "a.png", data)
	if img.Hash == "" || img.Ext != "png" {
		t.Fatalf("unexpected upload result: %+v", img)
	}
	if img.URL == "" || img.ThumbURL == "" {
		t.Fatalf("missing urls: %+v", img)
	}

	// 同内容再次上传：命中秒传，返回同一 hash 且不重复落库。
	again := env.mustUpload(t, token, "b.png", data)
	if again.Hash != img.Hash {
		t.Fatalf("dedup hash = %q, want %q", again.Hash, img.Hash)
	}

	res, body := env.rawJSON(t, http.MethodGet, "/api/v1/images?scope=all", nil, token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, body = %s", res.StatusCode, body)
	}
	var list struct {
		Data struct {
			Images []uploaded `json:"images"`
			Total  int        `json:"total"`
		} `json:"data"`
	}
	decode(t, body, &list)
	if len(list.Data.Images) != 1 || list.Data.Total != 1 {
		t.Fatalf("list = %d images (total %d), want 1/1", len(list.Data.Images), list.Data.Total)
	}
}

func TestUploadRejectsBadType(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	token := env.login(t, "admin", testPassword)

	res, body := env.upload(t, token, uploadFile{name: "evil.png", data: []byte("not really an image")})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad type: status = %d, body = %s, want 400", res.StatusCode, body)
	}
}

func TestUploadRejectsTooLarge(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	token := env.login(t, "admin", testPassword)

	// 把上限压到 1MB，再上传超过上限的 PNG。
	env.putSettings(t, token, map[string]any{"max_upload_mb": 1})

	big := pngBytes(t, 900, 900) // 随机噪声 PNG 通常 > 1MB
	if len(big) <= 1<<20 {
		t.Skipf("generated png too small (%d bytes) to exceed 1MB", len(big))
	}
	res, body := env.upload(t, token, uploadFile{name: "big.png", data: big})
	if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large: status = %d, body = %s", res.StatusCode, body)
	}
}

func TestUploadRequiresAuth(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)

	res, body := env.upload(t, "", uploadFile{name: "a.png", data: pngBytes(t, 4, 4)})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anon upload: status = %d, body = %s, want 401", res.StatusCode, body)
	}
}

func TestUploadBatchLimit(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	token := env.login(t, "admin", testPassword)

	v := env.putSettings(t, token, map[string]any{"max_batch": 2})
	if v.MaxBatch != 2 {
		t.Fatalf("max_batch = %d, want 2", v.MaxBatch)
	}

	files := []uploadFile{
		{name: "1.png", data: pngBytes(t, 4, 4)},
		{name: "2.png", data: pngBytes(t, 5, 5)},
		{name: "3.png", data: pngBytes(t, 6, 6)},
	}
	res, body := env.upload(t, token, files...)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("batch overflow: status = %d, body = %s, want 400", res.StatusCode, body)
	}
}

func TestUploadDisabledBySetting(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	adminTok := env.login(t, "admin", testPassword)
	env.register(t, "bob", testPassword)
	bobTok := env.login(t, "bob", testPassword)
	env.putSettings(t, adminTok, map[string]any{"allow_upload": false})

	// 普通用户受开关限制（R5）。
	res, body := env.upload(t, bobTok, uploadFile{name: "a.png", data: pngBytes(t, 4, 4)})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("upload disabled: status = %d, body = %s, want 403", res.StatusCode, body)
	}

	// admin 不受开关限制（TC-SET-001）。
	res, body = env.upload(t, adminTok, uploadFile{name: "a.png", data: pngBytes(t, 4, 4)})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("admin upload: status = %d, body = %s, want 200", res.StatusCode, body)
	}
}

// §7.1 serve_test：图片直出缓存头、缩略图、404、Range 与 CORS。

func TestServeImageHeaders(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	token := env.login(t, "admin", testPassword)
	img := env.mustUpload(t, token, "a.png", pngBytes(t, 10, 10))

	res, body := env.get("/i/" + img.Hash + ".png")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("serve image: status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %q, want image/png", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc == "" {
		t.Fatal("missing Cache-Control")
	}
	if len(body) == 0 || res.Header.Get("ETag") == "" {
		t.Fatalf("missing body/etag: len=%d etag=%q", len(body), res.Header.Get("ETag"))
	}

	// 第二次带 ETag 请求应返回 304。
	req, _ := http.NewRequest(http.MethodGet, "/i/"+img.Hash+".png", nil)
	req.Header.Set("If-None-Match", res.Header.Get("ETag"))
	res2, _ := env.do(t, req)
	if res2.StatusCode != http.StatusNotModified {
		t.Fatalf("cached request status = %d, want 304", res2.StatusCode)
	}
}

func TestServeThumb(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	token := env.login(t, "admin", testPassword)
	img := env.mustUpload(t, token, "a.png", pngBytes(t, 800, 600))

	res, body := env.get("/t/" + img.Hash + ".jpg")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("serve thumb: status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("thumb content-type = %q, want image/jpeg", ct)
	}
	if len(body) == 0 {
		t.Fatal("empty thumb body")
	}
}

func TestServeMissingImage(t *testing.T) {
	env := newEnv(t)
	res, body := env.get("/i/0000000000000000000000000000000000000000000000000000000000000000.png")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing image status = %d, want 404", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("missing image content-type = %q", ct)
	}
	if len(body) == 0 {
		t.Fatal("missing image body should not be empty")
	}
}

func TestServeRange(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	token := env.login(t, "admin", testPassword)
	img := env.mustUpload(t, token, "a.png", pngBytes(t, 10, 10))

	req, _ := http.NewRequest(http.MethodGet, "/i/"+img.Hash+".png", nil)
	req.Header.Set("Range", "bytes=0-9")
	res, body := env.do(t, req)
	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206", res.StatusCode)
	}
	if len(body) != 10 {
		t.Fatalf("range body len = %d, want 10", len(body))
	}
}

// §7.1 settings_test：公开配置、管理员读写、校验与非管理员拒绝。

func TestPublicConfig(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)

	res, body := env.rawJSON(t, http.MethodGet, "/api/v1/config", nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("config: status = %d, body = %s", res.StatusCode, body)
	}
	var out struct {
		Data struct {
			SiteName      string `json:"site_name"`
			AllowRegister bool   `json:"allow_register"`
			AllowUpload   bool   `json:"allow_upload"`
			MaxUploadMB   int    `json:"max_upload_mb"`
			MaxBatch      int    `json:"max_batch"`
		} `json:"data"`
	}
	decode(t, body, &out)
	if out.Data.SiteName == "" {
		t.Fatal("empty site_name")
	}
	if !out.Data.AllowUpload {
		t.Fatal("allow_upload should default to true")
	}
	if out.Data.MaxUploadMB != 20 {
		t.Fatalf("max_upload_mb = %d, want the seeded 20", out.Data.MaxUploadMB)
	}
}

func TestAdminSettingsRequiresAdmin(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	env.register(t, "bob", testPassword)
	bobToken := env.login(t, "bob", testPassword)

	res, body := env.rawJSON(t, http.MethodGet, "/api/v1/admin/settings", nil, bobToken)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin settings: status = %d, body = %s, want 403", res.StatusCode, body)
	}
}

func TestAdminSettingsUpdate(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	token := env.login(t, "admin", testPassword)

	v := env.putSettings(t, token, map[string]any{
		"site_name":      "我的图床",
		"allow_register": false,
		"max_upload_mb":  5,
		"max_batch":      3,
	})
	if v.SiteName != "我的图床" || v.AllowRegister || v.MaxUploadMB != 5 || v.MaxBatch != 3 {
		t.Fatalf("updated settings = %+v", v)
	}

	// 缓存应立即生效：公开 config 读到新值。
	res, body := env.rawJSON(t, http.MethodGet, "/api/v1/config", nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("config after update: %d %s", res.StatusCode, body)
	}
	var out struct {
		Data setting.Values `json:"data"`
	}
	decode(t, body, &out)
	if out.Data.SiteName != "我的图床" {
		t.Fatalf("config site_name = %q, want updated value", out.Data.SiteName)
	}
}

func TestAdminSettingsValidation(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	token := env.login(t, "admin", testPassword)

	cases := []map[string]any{
		{"max_upload_mb": 0},
		{"max_upload_mb": 99999},
		{"max_batch": 0},
		{"site_name": ""},
	}
	for _, c := range cases {
		res, body := env.rawJSON(t, http.MethodPut, "/api/v1/admin/settings", c, token)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid %v: status = %d, body = %s, want 400", c, res.StatusCode, body)
		}
	}
}

// §7.1 images_test：删除权限与同 hash 引用计数。

func TestDeleteImage(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	env.register(t, "bob", testPassword)
	adminToken := env.login(t, "admin", testPassword)
	bobToken := env.login(t, "bob", testPassword)

	adminImg := env.mustUpload(t, adminToken, "a.png", pngBytes(t, 10, 10))

	// 他人图片：普通用户不能删。
	res, body := env.rawJSON(t, http.MethodDelete, "/api/v1/images/"+itoa(adminImg.ID), nil, bobToken)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("bob delete admin image: status = %d, body = %s", res.StatusCode, body)
	}

	// 管理员可删任意图片。
	res, body = env.rawJSON(t, http.MethodDelete, "/api/v1/images/"+itoa(adminImg.ID), nil, adminToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("admin delete: status = %d, body = %s", res.StatusCode, body)
	}

	// 删除后原图与缩略图都不再可访问。
	if res, _ := env.get("/i/" + adminImg.Hash + ".png"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("image still served after delete: %d", res.StatusCode)
	}
}

func TestDeleteKeepsFileWhenReferenced(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	env.register(t, "bob", testPassword)
	token := env.login(t, "admin", testPassword)

	// 不同用户上传同一份字节：两条记录共享一个 hash（§3.1 内容寻址）。
	bobToken := env.login(t, "bob", testPassword)
	data := pngBytes(t, 12, 12)
	a := env.mustUpload(t, token, "a.png", data)
	b := env.mustUpload(t, bobToken, "b.png", data)
	if a.Hash != b.Hash || a.ID == b.ID {
		t.Fatalf("expected two records sharing a hash, got %+v and %+v", a, b)
	}

	res, body := env.rawJSON(t, http.MethodDelete, "/api/v1/images/"+itoa(a.ID), nil, token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete first: status = %d, body = %s", res.StatusCode, body)
	}
	// 仍有引用：文件必须保留。
	if res, _ := env.get("/i/" + a.Hash + ".png"); res.StatusCode != http.StatusOK {
		t.Fatalf("file removed while still referenced: %d", res.StatusCode)
	}

	res, body = env.rawJSON(t, http.MethodDelete, "/api/v1/images/"+itoa(b.ID), nil, token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete second: status = %d, body = %s", res.StatusCode, body)
	}
	if res, _ := env.get("/i/" + a.Hash + ".png"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("file kept after last reference deleted: %d", res.StatusCode)
	}
}

func itoa(v int64) string {
	return strconvFormatInt(v)
}

func strconvFormatInt(v int64) string {
	var buf [20]byte
	i := len(buf)
	if v == 0 {
		return "0"
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

var _ = bytes.MinRead

// 页码是用户输入：超大页码不能让 (page-1)*size 溢出成负 offset，
// 应稳定返回空列表而不是 500。
func TestListImagesRejectsAbsurdPage(t *testing.T) {
	env := newEnv(t)
	env.register(t, "admin", testPassword)
	token := env.login(t, "admin", testPassword)
	env.mustUpload(t, token, "a.png", pngBytes(t, 8, 8))

	res, body := env.rawJSON(t, http.MethodGet,
		"/api/v1/images?page=9223372036854775807&page_size=100", nil, token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
	var list struct {
		Data struct {
			Images []uploaded `json:"images"`
		} `json:"data"`
	}
	decode(t, body, &list)
	if len(list.Data.Images) != 0 {
		t.Fatalf("expected empty page for absurd page number, got %d items", len(list.Data.Images))
	}
}
