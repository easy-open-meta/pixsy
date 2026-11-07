package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pixsy/pixsy/internal/api"
	"github.com/pixsy/pixsy/internal/auth"
	"github.com/pixsy/pixsy/internal/config"
	"github.com/pixsy/pixsy/internal/model"
	"github.com/pixsy/pixsy/internal/server"
	"github.com/pixsy/pixsy/internal/setting"
	"github.com/pixsy/pixsy/internal/storage"
)

const testPassword = "password123"

// hotlinkEnv 搭出与生产一致的完整链路（API + 图片直出），专测外链防盗链 guard（§3.5）。
type hotlinkEnv struct {
	t        *testing.T
	handler  http.Handler
	settings *setting.Store
	store    *storage.Store
}

func newHotlinkEnv(t *testing.T, trustedProxies string) *hotlinkEnv {
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
	settings, err := setting.Load(context.Background(), db, 20)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}

	var trusted []*net.IPNet
	if trustedProxies != "" {
		if trusted, err = config.ParseCIDRs(trustedProxies); err != nil {
			t.Fatalf("parse trusted proxies: %v", err)
		}
	}
	apiSrv := api.New(api.Options{DB: db, Settings: settings, Store: store, Auth: mgr, TrustedProxies: trusted})
	srv := server.New(server.Options{
		API:            apiSrv.Handler(),
		Store:          store,
		DB:             db,
		Settings:       settings,
		Auth:           mgr,
		TrustedProxies: trusted,
	})
	return &hotlinkEnv{t: t, handler: srv.Handler(), settings: settings, store: store}
}

// seedImage 直接写一张小图进存储（Save 自动生成缩略图），返回原图与缩略图外链路径。
func (e *hotlinkEnv) seedImage(t *testing.T) (orig, thumb string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	res, err := e.store.Save(context.Background(), &buf, int64(buf.Len()))
	if err != nil {
		t.Fatalf("save image: %v", err)
	}
	return "/i/" + res.Hash + "." + res.Ext, "/t/" + res.Hash + ".jpg"
}

// setWhitelist 同时设置两张白名单（空串表示不启用对应机制）。
func (e *hotlinkEnv) setWhitelist(t *testing.T, ips, domains string) {
	t.Helper()
	if _, err := e.settings.Update(context.Background(), setting.Patch{
		HotlinkWhitelist: &ips,
		HotlinkReferers:  &domains,
	}); err != nil {
		t.Fatalf("update whitelist: %v", err)
	}
}

type mod func(*http.Request)

func fromIP(addr string) mod { return func(r *http.Request) { r.RemoteAddr = addr } }

func withReferer(v string) mod {
	return func(r *http.Request) { r.Header.Set("Referer", v) }
}

func withXFF(v string) mod {
	return func(r *http.Request) { r.Header.Set("X-Forwarded-For", v) }
}

func withCookie(c *http.Cookie) mod { return func(r *http.Request) { r.AddCookie(c) } }

func withDest(v string) mod {
	return func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", v) }
}

// setEmbedOnly 切换仅嵌图模式（hotlink_embed_only）。
func (e *hotlinkEnv) setEmbedOnly(t *testing.T, on bool) {
	t.Helper()
	if _, err := e.settings.Update(context.Background(), setting.Patch{
		HotlinkEmbedOnly: &on,
	}); err != nil {
		t.Fatalf("update embed only: %v", err)
	}
}

// get 发送图片直出请求，返回状态码、响应体与 Cache-Control。
func (e *hotlinkEnv) get(t *testing.T, path string, mods ...mod) (int, []byte, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, m := range mods {
		m(req)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	res := rec.Result()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	res.Body.Close()
	return res.StatusCode, body, res.Header.Get("Cache-Control")
}

func (e *hotlinkEnv) postJSON(t *testing.T, path string, payload any) (*http.Response, []byte) {
	t.Helper()
	buf, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
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

// loginCookie 注册并登录，返回登录响应下发的会话 Cookie（任何角色）。
func (e *hotlinkEnv) loginCookie(t *testing.T, username string) *http.Cookie {
	t.Helper()
	if res, body := e.postJSON(t, "/api/v1/auth/register", map[string]any{
		"username": username, "password": testPassword,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("register %s: %d %s", username, res.StatusCode, body)
	}
	res, body := e.postJSON(t, "/api/v1/auth/login", map[string]any{
		"username": username, "password": testPassword,
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login %s: %d %s", username, res.StatusCode, body)
	}
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	t.Fatalf("login 未下发 %s Cookie: %v", auth.SessionCookieName, res.Cookies())
	return nil
}

// 两个白名单都为空：任何 IP 直连都放行，行为与未加 guard 一致（§3.5）。
func TestHotlinkDisabledWhenBothEmpty(t *testing.T) {
	e := newHotlinkEnv(t, "")
	orig, thumb := e.seedImage(t)
	e.setWhitelist(t, "", "")

	for _, p := range []string{orig, thumb} {
		code, body, cc := e.get(t, p, fromIP("192.0.2.1:1"))
		if code != http.StatusOK || len(body) == 0 {
			t.Fatalf("%s: status=%d bytes=%d", p, code, len(body))
		}
		if cc != "public, max-age=31536000, immutable" {
			t.Fatalf("%s: Cache-Control = %q", p, cc)
		}
	}
}

// 仅启用 IP 白名单：命中放行、未命中空响应；空 Referer 与伪造 XFF 都不影响拦截。
func TestHotlinkIPWhitelistOnly(t *testing.T) {
	e := newHotlinkEnv(t, "")
	orig, thumb := e.seedImage(t)
	e.setWhitelist(t, "203.0.113.7,198.51.100.0/24", "")

	allowed := fromIP("203.0.113.7:1234")
	blocked := fromIP("192.0.2.1:999")

	code, body, cc := e.get(t, orig, allowed)
	if code != http.StatusOK || len(body) == 0 || cc != "public, max-age=31536000, immutable" {
		t.Fatalf("白名单内应正常出图: status=%d bytes=%d cc=%q", code, len(body), cc)
	}
	// CIDR 网段命中
	if code, body, _ = e.get(t, orig, fromIP("198.51.100.77:1")); code != http.StatusOK || len(body) == 0 {
		t.Fatalf("CIDR 内 IP 应放行: status=%d bytes=%d", code, len(body))
	}

	// 白名单外：200 空响应体 + no-store
	if code, body, cc = e.get(t, orig, blocked); code != http.StatusOK || len(body) != 0 || cc != "no-store" {
		t.Fatalf("白名单外应返回空响应: status=%d bytes=%d cc=%q", code, len(body), cc)
	}
	// 空 Referer 策略只作用于域名机制，不能放行 IP 拦截
	if _, body, _ = e.get(t, orig, blocked, withReferer("https://example.com/")); len(body) != 0 {
		t.Fatalf("域名表未启用时 Referer 不应放行: bytes=%d", len(body))
	}
	// 无可信代理时伪造 XFF 头不影响判定
	if _, body, _ = e.get(t, orig, blocked, withXFF("203.0.113.7")); len(body) != 0 {
		t.Fatalf("不可信 XFF 不应放行: bytes=%d", len(body))
	}
	// 缩略图同样受控
	if code, body, _ = e.get(t, thumb, blocked); code != http.StatusOK || len(body) != 0 {
		t.Fatalf("/t/ 应受控: status=%d bytes=%d", code, len(body))
	}
	if _, body, _ = e.get(t, thumb, allowed); len(body) == 0 {
		t.Fatal("/t/ 白名单内应放行")
	}
}

// 仅启用域名白名单：来源页面语义生效，空/异常 Referer 按策略放行。
func TestHotlinkRefererWhitelistOnly(t *testing.T) {
	e := newHotlinkEnv(t, "")
	orig, _ := e.seedImage(t)
	e.setWhitelist(t, "", "example.com")

	cases := []struct {
		name    string
		referer string
		allowed bool
	}{
		{"精确域名", "https://example.com/post/1", true},
		{"子域名", "https://img.example.com/x", true},
		{"后缀伪装不误命中", "https://evil-example.com/", false},
		{"其他域名", "https://other.com/", false},
		{"空 Referer 放行", "", true},
		{"非 http(s) scheme 放行", "chrome-extension://abcd/img", true},
		{"解析失败放行", "://bad", true},
	}
	for _, tc := range cases {
		var mods []mod
		mods = append(mods, fromIP("192.0.2.1:1"))
		if tc.referer != "" {
			mods = append(mods, withReferer(tc.referer))
		}
		code, body, _ := e.get(t, orig, mods...)
		if code != http.StatusOK {
			t.Fatalf("%s: status=%d", tc.name, code)
		}
		if (len(body) > 0) != tc.allowed {
			t.Fatalf("%s: bytes=%d, allowed=%v", tc.name, len(body), tc.allowed)
		}
	}
}

// 两表同启用：任一命中放行，全不命中拦截；登录会话 Cookie（含普通用户）兜底，伪造 Cookie 不放行。
func TestHotlinkBothListsAndSessionBypass(t *testing.T) {
	e := newHotlinkEnv(t, "")
	orig, _ := e.seedImage(t)
	e.setWhitelist(t, "203.0.113.7", "example.com")

	admin := e.loginCookie(t, "rootadmin") // 首个注册用户即管理员
	user := e.loginCookie(t, "plainuser")  // 第二个用户为普通用户
	fake := &http.Cookie{Name: auth.SessionCookieName, Value: "forged-token"}

	cases := []struct {
		name    string
		mods    []mod
		blocked bool
	}{
		{"IP 命中 Referer 不命中放行", []mod{fromIP("203.0.113.7:2"), withReferer("https://other.com/")}, false},
		{"IP 不命中 Referer 命中放行", []mod{fromIP("192.0.2.1:2"), withReferer("https://example.com/")}, false},
		{"全不命中拦截", []mod{fromIP("192.0.2.1:2"), withReferer("https://other.com/")}, true},
		{"管理员 Cookie 放行", []mod{fromIP("192.0.2.1:2"), withReferer("https://other.com/"), withCookie(admin)}, false},
		{"普通用户 Cookie 放行", []mod{fromIP("192.0.2.1:2"), withReferer("https://other.com/"), withCookie(user)}, false},
		{"伪造 Cookie 不放行", []mod{fromIP("192.0.2.1:2"), withReferer("https://other.com/"), withCookie(fake)}, true},
	}
	for _, tc := range cases {
		code, body, _ := e.get(t, orig, tc.mods...)
		if code != http.StatusOK {
			t.Fatalf("%s: status=%d", tc.name, code)
		}
		if (len(body) == 0) != tc.blocked {
			t.Fatalf("%s: bytes=%d, blocked=%v", tc.name, len(body), tc.blocked)
		}
	}
}

// 仅嵌图模式（无白名单）：文档级导航一律拦截，嵌图加载不受影响；
// 不发 Sec-Fetch-Dest 的老客户端/阅读器按原逻辑放行。
func TestHotlinkEmbedOnlyWithoutWhitelists(t *testing.T) {
	e := newHotlinkEnv(t, "")
	orig, _ := e.seedImage(t)
	e.setWhitelist(t, "", "")
	e.setEmbedOnly(t, true)

	cases := []struct {
		name    string
		mods    []mod
		blocked bool
	}{
		{"嵌图加载放行", []mod{withDest("image")}, false},
		{"无 Sec-Fetch 头放行", nil, false},
		{"新标签页文档导航拦截", []mod{withDest("document")}, true},
		{"iframe 导航拦截", []mod{withDest("iframe")}, true},
	}
	for _, tc := range cases {
		code, body, cc := e.get(t, orig, tc.mods...)
		if code != http.StatusOK {
			t.Fatalf("%s: status=%d", tc.name, code)
		}
		if (len(body) == 0) != tc.blocked {
			t.Fatalf("%s: bytes=%d, blocked=%v", tc.name, len(body), tc.blocked)
		}
		if tc.blocked && cc != "no-store" {
			t.Fatalf("%s: 拦截响应应 no-store, got %q", tc.name, cc)
		}
	}
}

// 仅嵌图模式与白名单组合：Referer 命中不能授权文档级导航（否则从白名单站点
// 「在新标签页打开图片」即可绕过）；IP 白名单与登录会话 Cookie 可以放行文档导航。
func TestHotlinkEmbedOnlyWithWhitelists(t *testing.T) {
	e := newHotlinkEnv(t, "")
	orig, _ := e.seedImage(t)
	e.setWhitelist(t, "203.0.113.7", "example.com")
	e.setEmbedOnly(t, true)

	admin := e.loginCookie(t, "rootadmin")
	user := e.loginCookie(t, "plainuser") // 普通用户：图库「打开原图」依赖此放行

	cases := []struct {
		name    string
		mods    []mod
		blocked bool
	}{
		{"嵌图+白名单 Referer 放行", []mod{withDest("image"), withReferer("https://example.com/")}, false},
		{"嵌图+空 Referer 放行", []mod{withDest("image")}, false},
		{"嵌图+其他站 Referer 拦截", []mod{withDest("image"), withReferer("https://other.com/")}, true},
		{"文档导航+白名单 Referer 仍拦截", []mod{withDest("document"), withReferer("https://example.com/")}, true},
		{"文档导航+空 Referer 拦截", []mod{withDest("document")}, true},
		{"文档导航+IP 白名单放行", []mod{withDest("document"), fromIP("203.0.113.7:2"), withReferer("https://other.com/")}, false},
		{"文档导航+管理员 Cookie 放行", []mod{withDest("document"), withReferer("https://other.com/"), withCookie(admin)}, false},
		{"文档导航+普通用户 Cookie 放行", []mod{withDest("document"), withReferer("https://other.com/"), withCookie(user)}, false},
	}
	for _, tc := range cases {
		code, body, _ := e.get(t, orig, tc.mods...)
		if code != http.StatusOK {
			t.Fatalf("%s: status=%d", tc.name, code)
		}
		if (len(body) == 0) != tc.blocked {
			t.Fatalf("%s: bytes=%d, blocked=%v", tc.name, len(body), tc.blocked)
		}
	}
}

// 仅嵌图关闭时行为与之前完全一致：文档导航按 Referer/空 Referer 语义判定。
func TestHotlinkEmbedOnlyOffKeepsLegacyBehavior(t *testing.T) {
	e := newHotlinkEnv(t, "")
	orig, _ := e.seedImage(t)
	e.setWhitelist(t, "", "example.com")
	e.setEmbedOnly(t, false)

	if _, body, _ := e.get(t, orig, withDest("document"), withReferer("https://example.com/")); len(body) == 0 {
		t.Fatal("关闭仅嵌图时白名单 Referer 应放行文档导航（原行为）")
	}
	if _, body, _ := e.get(t, orig, withDest("document")); len(body) == 0 {
		t.Fatal("关闭仅嵌图时空 Referer 应放行（原行为）")
	}
	if _, body, _ := e.get(t, orig, withDest("document"), withReferer("https://other.com/")); len(body) != 0 {
		t.Fatal("关闭仅嵌图时非白名单 Referer 应拦截（原行为）")
	}
}

// 连接来自可信代理时按 X-Forwarded-For 判定访客 IP。
func TestHotlinkXFFWithTrustedProxy(t *testing.T) {
	e := newHotlinkEnv(t, "10.0.0.0/8")
	orig, _ := e.seedImage(t)
	e.setWhitelist(t, "203.0.113.7", "")

	if _, body, _ := e.get(t, orig, fromIP("10.0.0.9:1"), withXFF("203.0.113.7, 10.0.0.9")); len(body) == 0 {
		t.Fatal("可信代理后的白名单 XFF 应放行")
	}
	if _, body, _ := e.get(t, orig, fromIP("10.0.0.9:1"), withXFF("198.51.100.5")); len(body) != 0 {
		t.Fatal("可信代理后的非白名单 XFF 应拦截")
	}
}

// 登录响应下发的会话 Cookie 属性：HttpOnly、SameSite=Lax、Max-Age=token TTL。
func TestLoginSetsSessionCookieAttributes(t *testing.T) {
	e := newHotlinkEnv(t, "")
	e.postJSON(t, "/api/v1/auth/register", map[string]any{
		"username": "adminattr", "password": testPassword,
	})
	res, _ := e.postJSON(t, "/api/v1/auth/login", map[string]any{
		"username": "adminattr", "password": testPassword,
	})
	var ck *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookieName {
			ck = c
		}
	}
	if ck == nil {
		t.Fatal("登录应下发会话 Cookie")
	}
	if !ck.HttpOnly {
		t.Fatal("Cookie 应为 HttpOnly")
	}
	if ck.SameSite != http.SameSiteLaxMode {
		t.Fatalf("Cookie 应为 SameSite=Lax, got %v", ck.SameSite)
	}
	if ck.MaxAge != int(auth.TokenTTL.Seconds()) {
		t.Fatalf("MaxAge=%d, want %d", ck.MaxAge, int(auth.TokenTTL.Seconds()))
	}
	if ck.Path != "/" {
		t.Fatalf("Path=%q", ck.Path)
	}
}

// 放行路径的条件请求语义不变：ETag 命中返回 304。
func TestHotlinkAllowedKeepsConditionalRequests(t *testing.T) {
	e := newHotlinkEnv(t, "")
	orig, _ := e.seedImage(t)
	e.setWhitelist(t, "203.0.113.7", "")
	hash := strings.TrimSuffix(strings.TrimPrefix(orig, "/i/"), ".png")

	req := httptest.NewRequest(http.MethodGet, orig, nil)
	req.RemoteAddr = "203.0.113.7:2"
	req.Header.Set("If-None-Match", `"`+hash+`"`)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("放行路径应保留 304 语义: status=%d", rec.Code)
	}
}

// 公开配置接口不应泄露白名单字段。
func TestPublicConfigHidesWhitelist(t *testing.T) {
	e := newHotlinkEnv(t, "")
	e.setWhitelist(t, "203.0.113.7", "example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, "203.0.113.7") || strings.Contains(body, "example.com") || strings.Contains(body, "hotlink") {
		t.Fatalf("公开配置泄露白名单: %s", body)
	}
}
