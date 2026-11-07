// Package server 组装 HTTP 服务：API、图片直出与前端静态资源（§3.5、§3.8）。
package server

import (
	"database/sql"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pixsy/pixsy/internal/auth"
	"github.com/pixsy/pixsy/internal/model"
	"github.com/pixsy/pixsy/internal/setting"
	"github.com/pixsy/pixsy/internal/storage"
)

// Options 是构建根处理器所需的依赖。
type Options struct {
	API            http.Handler // /api/v1 处理器
	Store          *storage.Store
	DB             *sql.DB // 旧路径别名查询（可为 nil）
	Static         fs.FS   // 内嵌前端产物（以 dist 为根）
	StaticDir      string  // 非空时从磁盘读取前端产物（开发热更新）
	Logger         *slog.Logger
	Settings       *setting.Store // 外链白名单设置（nil 视为功能未启用）
	Auth           *auth.Manager  // 管理员 Cookie 校验（nil 时跳过管理员放行分支）
	TrustedProxies []*net.IPNet   // 可信代理网段；空表示直连部署，忽略 X-Forwarded-*
}

// Server 持有路由所需的依赖。
type Server struct {
	api            http.Handler
	store          *storage.Store
	db             *sql.DB
	static         fs.FS
	staticDir      string
	logger         *slog.Logger
	mux            *http.ServeMux
	settings       *setting.Store
	auth           *auth.Manager
	trustedProxies []*net.IPNet
}

// New 创建服务；Static 可为 nil（此时只提供 API 与图片）。
func New(opts Options) *Server {
	s := &Server{
		api:            opts.API,
		store:          opts.Store,
		db:             opts.DB,
		static:         opts.Static,
		staticDir:      opts.StaticDir,
		logger:         opts.Logger,
		settings:       opts.Settings,
		auth:           opts.Auth,
		trustedProxies: opts.TrustedProxies,
	}
	if s.logger == nil {
		s.logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", s.api)
	// 路径通配：既支持 /i/<hash>.<ext>，也支持 Lsky 风格的 /i/2025/06/20/x.png 旧链接。
	// 两条图片直出路由包一层外链防盗链 guard（§3.5）。
	mux.Handle("GET /i/{path...}", s.hotlinkGuard(http.HandlerFunc(s.serveOriginal)))
	mux.Handle("GET /t/{name}", s.hotlinkGuard(http.HandlerFunc(s.serveThumb)))
	mux.HandleFunc("/", s.serveStatic)
	s.mux = mux
	return s
}

// Handler 返回带安全响应头与请求日志的根处理器。
func (s *Server) Handler() http.Handler { return s.securityHeaders(s.logRequests(s.mux)) }

// ---------------------------------------------------------------- 外链访问控制

// hotlinkGuard 实现外链防盗链（§3.5）：未启用任何机制（两个白名单都为空
// 且未开仅嵌图）时不做任何检测，行为与未加 guard 完全一致。启用后按序判定：
//
//	仅嵌图模式下，Sec-Fetch-Dest 存在且非 image（新标签页/地址栏等文档级导航）
//	→ 只认 IP 白名单与管理员 Cookie，Referer/空 Referer 放行不适用；
//	嵌图加载（dest=image 或缺失）→ ClientIP 在 IP 白名单内放行；域名白名单启用
//	且无有效 Referer（空 Referer 策略）或 Referer 域名命中放行；管理员 Cookie
//	（登录时下发的 JWT）有效放行，管理员无需把自己的 IP 入白名单；
//	其余 → 200 空响应体，不暴露拦截行为。
func (s *Server) hotlinkGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hotlinkAllowed(r) {
			// security: no-store 必不可少——若空响应被 CDN/浏览器按缓存规则
			// 存储，后续放行的正常请求会命中被污染的缓存条目。
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hotlinkAllowed 按放行公式判定单个请求，任何一步命中即放行。
func (s *Server) hotlinkAllowed(r *http.Request) bool {
	if s.settings == nil {
		return true
	}
	rules := s.settings.HotlinkRules()
	hasWhitelist := len(rules.Nets) > 0 || len(rules.Domains) > 0
	if !hasWhitelist && !rules.EmbedOnly {
		return true // 防盗链完全未启用
	}
	if rules.EmbedOnly && !isEmbeddedImageLoad(r) {
		// 仅嵌图：文档级导航（新标签页/地址栏/iframe 等）不套用 Referer 与
		// 空 Referer 放行——否则从白名单站点"在新标签页打开图片"即可绕过。
		// 只认 IP 白名单与管理员 Cookie。
		if len(rules.Nets) > 0 && ipInNets(auth.ClientIP(r, s.trustedProxies), rules.Nets) {
			return true
		}
		return s.sessionAllowed(r)
	}
	if !hasWhitelist {
		return true // 仅启用仅嵌图模式：嵌图加载不受白名单限制
	}
	if len(rules.Nets) > 0 && ipInNets(auth.ClientIP(r, s.trustedProxies), rules.Nets) {
		return true
	}
	if len(rules.Domains) > 0 {
		host, ok := refererHost(r)
		// 无有效来源（空 Referer/解析失败/非 http(s) scheme）按空 Referer 策略放行。
		if !ok || domainAllowed(host, rules.Domains) {
			return true
		}
	}
	return s.sessionAllowed(r)
}

// sessionAllowed 校验登录会话 Cookie（登录时下发的 JWT）：站内登录用户视为
// 可信——图库的「打开原图」是新标签页导航（dest=document），不能被仅嵌图
// 模式误伤；开放注册时注册用户因此可绕过白名单取图，属已接受的取舍（§3.5）。
func (s *Server) sessionAllowed(r *http.Request) bool {
	if s.auth == nil {
		return false
	}
	c, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		return false
	}
	_, err = s.auth.Parse(c.Value)
	return err == nil
}

// isEmbeddedImageLoad 报告请求是否为网页图片元素加载：Fetch Metadata 的
// Sec-Fetch-Dest 为 image，或缺失（老浏览器/非浏览器客户端按原逻辑放行）。
// 该头由浏览器强制附加，JS 不可伪造或移除；文档级导航（新标签页/地址栏）为 document。
func isEmbeddedImageLoad(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "", "image":
		return true
	default:
		return false
	}
}

// ipInNets 报告点分 IP 字符串是否落在任一网段内（auth.ipInNets 的本地形态）。
func ipInNets(ip string, nets []*net.IPNet) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// refererHost 提取 Referer 的来源主机名（小写、去端口）。仅采信 http/https
// scheme；空、解析失败、app 扩展等其他 scheme 返回 ok=false，视为无有效来源。
func refererHost(r *http.Request) (host string, ok bool) {
	ref := r.Referer()
	if ref == "" {
		return "", false
	}
	u, err := url.Parse(ref)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	host = strings.ToLower(u.Hostname())
	return host, host != ""
}

// domainAllowed 按域名白名单匹配来源 host：精确相等或为其子域
// （img.example.com 命中 example.com，evil-example.com 不会误命中）。
func domainAllowed(host string, domains []string) bool {
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// securityHeaders 为所有响应补齐基础安全头（§3.5）。
// security: 前端 SPA 无内联脚本，可上严格 CSP；frame-ancestors 'none'
// 阻止后台页被点击劫持；HSTS 只在 TLS（含反代终结）时下发。
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src * data:; script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'none'; "+
				"frame-ancestors 'none'")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- 图片直出

// serveOriginal 直出原图：/i/{hash}.{ext}（§3.5：零重定向、永久缓存、CDN 友好）。
func (s *Server) serveOriginal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("path")
	hash, ext, ok := splitName(name)
	if !ok && s.db != nil {
		// 旧站路径别名：/i/2025/06/20/x.png → 内容哈希，保证历史文章链接直接可用。
		if a, err := model.FindAlias(r.Context(), s.db, name); err == nil {
			hash, ext = a.Hash, a.Ext
			ok = validHash(hash)
		}
	}
	if !ok {
		notFound(w)
		return
	}
	mime, ok := storage.MimeForExt(ext)
	if !ok {
		notFound(w)
		return
	}
	f, fi, err := s.store.OpenFile(hash, ext)
	if err != nil {
		notFound(w)
		return
	}
	defer f.Close()
	serveImage(w, r, f, fi, hash, mime)
}

// serveThumb 直出缩略图：/t/{hash}.jpg。
func (s *Server) serveThumb(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	hash, ok := strings.CutSuffix(name, ".jpg")
	if !ok || !validHash(hash) {
		notFound(w)
		return
	}
	f, fi, err := s.store.OpenThumb(hash)
	if err != nil {
		notFound(w)
		return
	}
	defer f.Close()
	serveImage(w, r, f, fi, hash, "image/jpeg")
}

// serveImage 输出图片响应：immutable 缓存头 + ETag + Range/304 交给 ServeContent。
func serveImage(w http.ResponseWriter, r *http.Request, f *os.File, fi os.FileInfo, hash, mime string) {
	h := w.Header()
	h.Set("Content-Type", mime)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("X-Content-Type-Options", "nosniff")
	// 内容寻址：hash 即可作为强 ETag，ServeContent 据此处理 If-None-Match/If-Range。
	h.Set("ETag", `"`+hash+`"`)
	http.ServeContent(w, r, hash, fi.ModTime(), f)
}

func splitName(name string) (hash, ext string, ok bool) {
	hash, ext, ok = strings.Cut(name, ".")
	if !ok || !validHash(hash) || ext == "" {
		return "", "", false
	}
	return hash, ext, true
}

func validHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// notFound 返回纯文本 404（§3.5）。
func notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, "404 not found\n")
}

// ---------------------------------------------------------------- 静态资源

// staticFS 返回前端产物文件系统：PIXSY_STATIC_DIR 优先，其次内嵌产物。
func (s *Server) staticFS() (fs.FS, bool) {
	if s.staticDir != "" {
		return os.DirFS(s.staticDir), true
	}
	if s.static != nil {
		return s.static, true
	}
	return nil, false
}

// serveStatic 托管前端 SPA：命中文件即直出，否则回退 index.html。
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	fsys, ok := s.staticFS()
	if !ok {
		notFound(w)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if f, err := fsys.Open(name); err == nil {
		defer f.Close()
		if st, err := f.Stat(); err == nil && !st.IsDir() {
			if rs, ok := f.(io.ReadSeeker); ok {
				w.Header().Set("Cache-Control", cacheControlFor(name))
				http.ServeContent(w, r, name, st.ModTime(), rs)
				return
			}
		}
	}

	index, err := fsys.Open("index.html")
	if err != nil {
		notFound(w)
		return
	}
	defer index.Close()
	if rs, ok := index.(io.ReadSeeker); ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", time.Time{}, rs)
		return
	}
	notFound(w)
}

// cacheControlFor：Vite 产物带内容指纹，可长期缓存；入口 HTML 必须每次校验。
func cacheControlFor(name string) string {
	if strings.HasPrefix(name, "assets/") {
		return "public, max-age=31536000, immutable"
	}
	if strings.HasSuffix(name, ".html") {
		return "no-cache"
	}
	return "public, max-age=3600"
}

// ---------------------------------------------------------------- 请求日志

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// ReadFrom 保留底层 sendfile 快路径，避免包装 ResponseWriter 拖慢图片直出。
func (r *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	n, err := io.Copy(r.ResponseWriter, src)
	r.bytes += n
	return n, err
}

// Unwrap 让 http.ResponseController 能取到底层连接。
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
