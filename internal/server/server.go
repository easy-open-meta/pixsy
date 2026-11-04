// Package server 组装 HTTP 服务：API、图片直出与前端静态资源（§3.5、§3.8）。
package server

import (
	"database/sql"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pixsy/pixsy/internal/model"
	"github.com/pixsy/pixsy/internal/storage"
)

// Options 是构建根处理器所需的依赖。
type Options struct {
	API       http.Handler // /api/v1 处理器
	Store     *storage.Store
	DB        *sql.DB // 旧路径别名查询（可为 nil）
	Static    fs.FS   // 内嵌前端产物（以 dist 为根）
	StaticDir string  // 非空时从磁盘读取前端产物（开发热更新）
	Logger    *slog.Logger
}

// Server 持有路由所需的依赖。
type Server struct {
	api       http.Handler
	store     *storage.Store
	db        *sql.DB
	static    fs.FS
	staticDir string
	logger    *slog.Logger
	mux       *http.ServeMux
}

// New 创建服务；Static 可为 nil（此时只提供 API 与图片）。
func New(opts Options) *Server {
	s := &Server{
		api:       opts.API,
		store:     opts.Store,
		db:        opts.DB,
		static:    opts.Static,
		staticDir: opts.StaticDir,
		logger:    opts.Logger,
	}
	if s.logger == nil {
		s.logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", s.api)
	// 路径通配：既支持 /i/<hash>.<ext>，也支持 Lsky 风格的 /i/2025/06/20/x.png 旧链接。
	mux.HandleFunc("GET /i/{path...}", s.serveOriginal)
	mux.HandleFunc("GET /t/{name}", s.serveThumb)
	mux.HandleFunc("/", s.serveStatic)
	s.mux = mux
	return s
}

// Handler 返回带请求日志的根处理器。
func (s *Server) Handler() http.Handler { return s.logRequests(s.mux) }

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
