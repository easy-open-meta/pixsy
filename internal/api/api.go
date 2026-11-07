// Package api 实现 /api/v1 下的 JSON 接口（§3.3、§3.6、§3.7）。
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/pixsy/pixsy/internal/auth"
	"github.com/pixsy/pixsy/internal/httpx"
	"github.com/pixsy/pixsy/internal/model"
	"github.com/pixsy/pixsy/internal/setting"
	"github.com/pixsy/pixsy/internal/storage"
)

const (
	defaultPageSize = 40
	maxPageSize     = 100
	maxPage         = 100000 // 远超实际数据量，只为给 offset 计算兜底
	maxJSONBody     = 1 << 20
)

// Options 组装 API 处理器所需的依赖。
type Options struct {
	DB             *sql.DB
	Settings       *setting.Store
	Store          *storage.Store
	Auth           *auth.Manager
	BaseURL        string
	TrustedProxies []*net.IPNet // 可信代理网段；空表示直连部署，忽略 X-Forwarded-*
	// AllowPrivateHosts 允许 Lsky 迁移指向内网地址（默认拒绝，防 SSRF）。
	// 仅用于测试环境，或用户明确要迁移局域网内自建 Lsky 的场景。
	AllowPrivateHosts bool
}

// Server 持有 API 处理器共享的依赖。
type Server struct {
	db                *sql.DB
	settings          *setting.Store
	store             *storage.Store
	auth              *auth.Manager
	baseURL           string
	trustedProxies    []*net.IPNet
	allowPrivateHosts bool
	rateLimit         *auth.Limiter
	migrator          *lskyMigrator
}

// New 创建 API 处理器。
func New(opts Options) *Server {
	return &Server{
		db:                opts.DB,
		settings:          opts.Settings,
		store:             opts.Store,
		auth:              opts.Auth,
		baseURL:           strings.TrimRight(opts.BaseURL, "/"),
		trustedProxies:    opts.TrustedProxies,
		allowPrivateHosts: opts.AllowPrivateHosts,
		rateLimit:         auth.NewLimiter(5, time.Minute),
		migrator:          &lskyMigrator{},
	}
}

// Handler 返回注册了全部 /api/v1 路由的处理器。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/config", s.handleConfig)
	mux.HandleFunc("POST /api/v1/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)

	require := auth.Require(s.auth, s.db)
	mux.Handle("GET /api/v1/auth/me", require(http.HandlerFunc(s.handleMe)))
	mux.Handle("POST /api/v1/images", require(http.HandlerFunc(s.handleUpload)))
	mux.Handle("GET /api/v1/images", require(http.HandlerFunc(s.handleList)))
	mux.Handle("DELETE /api/v1/images/{id}", require(http.HandlerFunc(s.handleDelete)))
	mux.Handle("GET /api/v1/admin/settings", require(auth.RequireAdmin(http.HandlerFunc(s.handleGetSettings))))
	mux.Handle("PUT /api/v1/admin/settings", require(auth.RequireAdmin(http.HandlerFunc(s.handlePutSettings))))
	mux.Handle("POST /api/v1/admin/migrate/lsky", require(auth.RequireAdmin(http.HandlerFunc(s.handleStartLskyMigration))))
	mux.Handle("GET /api/v1/admin/migrate/lsky", require(auth.RequireAdmin(http.HandlerFunc(s.handleLskyMigrationStatus))))
	mux.Handle("POST /api/v1/admin/migrate/lsky/cancel", require(auth.RequireAdmin(http.HandlerFunc(s.handleCancelLskyMigration))))
	mux.Handle("GET /api/v1/admin/migrate/lsky/csv", require(auth.RequireAdmin(http.HandlerFunc(s.handleLskyMigrationCSV))))

	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, http.StatusNotFound, "接口不存在")
	})
	return mux
}

// ---------------------------------------------------------------- 公开配置

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	// 公开接口：白名单属于访问控制信息，走 Public() 视图剔除后再序列化（§3.3）。
	httpx.Data(w, http.StatusOK, s.settings.Get().Public())
}

// ---------------------------------------------------------------- 认证

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.rateLimit.Allow(auth.ClientIP(r, s.trustedProxies)) {
		httpx.Fail(w, http.StatusTooManyRequests, "操作过于频繁，请稍后再试")
		return
	}
	var in credentials
	if !decodeJSON(w, r, &in) {
		return
	}
	name := auth.NormalizeUsername(in.Username)
	if err := auth.ValidateUsername(name); err != nil {
		httpx.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.ValidatePassword(in.Password); err != nil {
		httpx.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	// 注册开关检查放在 bcrypt 之前：关闭注册时被拒绝的请求
	// 不应白白消耗一次 cost 10 的哈希 CPU。
	if err := s.checkRegisterOpen(r.Context()); err != nil {
		httpx.Fail(w, http.StatusForbidden, err.Error())
		return
	}
	pwHash, err := auth.HashPassword(in.Password)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "密码处理失败")
		return
	}

	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "数据库不可用")
		return
	}
	defer tx.Rollback()

	count, err := model.CountUsersTx(ctx, tx)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "数据库不可用")
		return
	}
	role := model.RoleUser
	if count == 0 {
		// 首个注册用户成为超级管理员，且不受 allow_register 限制（§3.2、§3.7）。
		role = model.RoleAdmin
	} else if !s.settings.Get().AllowRegister {
		httpx.Fail(w, http.StatusForbidden, "注册已关闭")
		return
	}

	u, err := model.CreateUserTx(ctx, tx, name, pwHash, role)
	if err != nil {
		if errors.Is(err, model.ErrUsernameTaken) || model.IsUniqueViolation(err) {
			httpx.Fail(w, http.StatusConflict, "用户名已被占用")
			return
		}
		httpx.Fail(w, http.StatusInternalServerError, "注册失败")
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "注册失败")
		return
	}
	httpx.Data(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) checkRegisterOpen(ctx context.Context) error {
	if s.settings.Get().AllowRegister {
		return nil
	}
	// 无任何用户时首个注册即管理员，不受 allow_register 限制（§3.2、§3.7）。
	count, err := model.CountUsers(ctx, s.db)
	if err != nil {
		return errors.New("注册已关闭")
	}
	if count == 0 {
		return nil
	}
	return errors.New("注册已关闭")
}

// dummyHash 与线上 bcrypt cost 相同，登录时对不存在的用户也跑一次比较，
// 抹平「用户不存在」与「密码错误」的时延差，防用户名枚举时序预言机。
var dummyHash = func() string {
	h, _ := bcrypt.GenerateFromPassword([]byte("pixsy-dummy-password"), 10)
	return string(h)
}()

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.rateLimit.Allow(auth.ClientIP(r, s.trustedProxies)) {
		httpx.Fail(w, http.StatusTooManyRequests, "操作过于频繁，请稍后再试")
		return
	}
	var in credentials
	if !decodeJSON(w, r, &in) {
		return
	}
	u, err := model.GetUserByName(r.Context(), s.db, auth.NormalizeUsername(in.Username))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// security: 与命中用户的路径做等量 bcrypt 运算，消除用户枚举时序侧信道。
			auth.CheckPassword(dummyHash, in.Password)
			httpx.Fail(w, http.StatusUnauthorized, "用户名或密码错误")
			return
		}
		httpx.Fail(w, http.StatusInternalServerError, "数据库不可用")
		return
	}
	if !auth.CheckPassword(u.PasswordHash, in.Password) {
		httpx.Fail(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	token, err := s.auth.Issue(u.ID, u.Role)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "签发令牌失败")
		return
	}
	s.setSessionCookie(w, r, token)
	httpx.Data(w, http.StatusOK, map[string]any{"token": token, "user": u})
}

// setSessionCookie 登录成功时下发会话 Cookie（值即 JWT，任何角色，§3.5）：
// 图片直出路由（/i/、/t/）靠它识别登录用户并放行外链防盗链——<img> 加载与
// 「打开原图」的 target=_blank 导航都带不上 Authorization 头。
// HttpOnly + SameSite=Lax：JS 不可读；第三方站点嵌图时浏览器不携带该
// Cookie，登录凭证不会混入外链请求。无服务端注销接口，Cookie 随
// token 同期（TokenTTL）自然过期。
func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	// security: Secure 只在确定走 HTTPS 时设置（直连 TLS，或可信代理声明的
	// X-Forwarded-Proto）——直连 HTTP 部署误设会让浏览器丢弃 Cookie，
	// 登录用户的外链访问静默失效。
	secure := r.TLS != nil ||
		(auth.RemoteAddrInNets(r, s.trustedProxies) && r.Header.Get("X-Forwarded-Proto") == "https")
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(auth.TokenTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	httpx.Data(w, http.StatusOK, map[string]any{"user": auth.UserFrom(r.Context())})
}

// ---------------------------------------------------------------- 图片

// imageOut 是图片的响应视图：记录字段 + 常用引用格式（§3.3）。
type imageOut struct {
	ID        int64     `json:"id"`
	Filename  string    `json:"filename"`
	URL       string    `json:"url"`
	ThumbURL  string    `json:"thumb_url"`
	Markdown  string    `json:"markdown"`
	HTML      string    `json:"html"`
	BBCode    string    `json:"bbcode"`
	Size      int64     `json:"size"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Hash      string    `json:"hash"`
	Ext       string    `json:"ext"`
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// baseURLFor 返回生成图片外链用的站点前缀。显式配置的 base_url 优先；
// 否则从请求推断。X-Forwarded-* 仅当连接来自可信代理时才采信，
// 且 scheme/host 都做格式校验——伪造头无法注入引号或脚本进生成的链接。
func (s *Server) baseURLFor(r *http.Request) string {
	if s.baseURL != "" {
		return s.baseURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if auth.RemoteAddrInNets(r, s.trustedProxies) {
		if p := r.Header.Get("X-Forwarded-Proto"); p == "http" || p == "https" {
			scheme = p
		}
		if h := r.Header.Get("X-Forwarded-Host"); validHost(h) {
			host = h
		}
	}
	// security: Host/转发头不可信时只接受合法主机名字符，防止拼接出
	// javascript: 等异常 scheme 或带引号注入 HTML 属性。
	if !validHost(host) {
		host = ""
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// validHost 校验主机名/IP[:port] 只含 URL host 的合法字符。
func validHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, c := range host {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '-' || c == ':' || c == '[' || c == ']':
		default:
			return false
		}
	}
	return true
}

func (s *Server) present(r *http.Request, img *model.Image) imageOut {
	base := s.baseURLFor(r)
	raw := base + "/i/" + img.Hash + "." + img.Ext
	thumb := base + "/t/" + img.Hash + ".jpg"
	// security: html 片段必须转义——文件名与 host 虽经 sanitize/校验，
	// 这里仍按不可信数据处理，杜绝未来改动引入的注入面。
	htmlAttr := html.EscapeString(raw)
	alt := html.EscapeString(img.Filename)
	return imageOut{
		ID:        img.ID,
		Filename:  img.Filename,
		URL:       raw,
		ThumbURL:  thumb,
		Markdown:  "![" + img.Filename + "](" + raw + ")",
		HTML:      `<img src="` + htmlAttr + `" alt="` + alt + `" />`,
		BBCode:    "[img]" + raw + "[/img]",
		Size:      img.Size,
		Width:     img.Width,
		Height:    img.Height,
		Hash:      img.Hash,
		Ext:       img.Ext,
		UserID:    img.UserID,
		CreatedAt: img.CreatedAt,
	}
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		httpx.Fail(w, http.StatusUnauthorized, "请先登录")
		return
	}
	v := s.settings.Get()
	if !v.AllowUpload && u.Role != model.RoleAdmin {
		httpx.Fail(w, http.StatusForbidden, "上传已关闭")
		return
	}

	maxBytes := int64(v.MaxUploadMB) << 20
	maxBatch := v.MaxBatch
	if maxBatch <= 0 {
		maxBatch = setting.DefaultMaxBatch
	}
	// 整体请求体上限：多文件总和 + multipart 开销。
	// security: 无条件包 MaxBytesReader——ContentLength 预检在
	// Transfer-Encoding: chunked 时为 -1，可被绕过，不能只靠它。
	totalLimit := maxBytes*int64(maxBatch) + (1 << 20)
	r.Body = http.MaxBytesReader(w, r.Body, totalLimit)
	if r.ContentLength > 0 && r.ContentLength > totalLimit {
		httpx.Fail(w, http.StatusRequestEntityTooLarge, "文件超出体积上限")
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		httpx.Fail(w, http.StatusBadRequest, "需要 multipart/form-data 请求")
		return
	}

	ctx := r.Context()
	out := make([]imageOut, 0, 1)
	seen := 0
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			httpx.Fail(w, http.StatusBadRequest, "读取上传数据失败")
			return
		}
		if part.FormName() != "file" {
			part.Close()
			continue
		}
		seen++
		if seen > maxBatch {
			part.Close()
			httpx.Fail(w, http.StatusBadRequest, "单次上传文件数超出上限")
			return
		}

		res, saveErr := s.store.Save(ctx, part, maxBytes)
		part.Close()
		if saveErr != nil {
			switch {
			case errors.Is(saveErr, storage.ErrTooLarge):
				httpx.Fail(w, http.StatusRequestEntityTooLarge, "文件超出体积上限")
			case errors.Is(saveErr, storage.ErrUnsupported):
				httpx.Fail(w, http.StatusBadRequest, "不支持的图片格式")
			case errors.Is(saveErr, context.Canceled):
				httpx.Fail(w, http.StatusBadRequest, "上传已取消")
			default:
				// 连接中断、磁盘错误等都归到这里；记录原始错误便于排查。
				slog.Error("save upload failed", "err", saveErr)
				httpx.Fail(w, http.StatusInternalServerError, "保存文件失败")
			}
			return
		}

		img, err := s.record(ctx, u.ID, part.FileName(), res)
		if err != nil {
			httpx.Fail(w, http.StatusInternalServerError, "写入图片记录失败")
			return
		}
		out = append(out, s.present(r, img))
	}
	if len(out) == 0 {
		httpx.Fail(w, http.StatusBadRequest, "未收到文件")
		return
	}
	httpx.Data(w, http.StatusOK, map[string]any{"images": out})
}

// record 入库：同用户同 hash 幂等，返回已有记录（§3.4 step 6）。
func (s *Server) record(ctx context.Context, userID int64, filename string, res *storage.Result) (*model.Image, error) {
	existing, err := model.FindImageByUserHash(ctx, s.db, userID, res.Hash)
	if err == nil && existing != nil {
		return existing, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	img := &model.Image{
		Hash:     res.Hash,
		Ext:      res.Ext,
		Filename: sanitizeFilename(filename),
		Mime:     res.Mime,
		Size:     res.Size,
		Width:    res.Width,
		Height:   res.Height,
		UserID:   userID,
	}
	if err := model.InsertImage(ctx, s.db, img); err != nil {
		// 并发上传同文件时可能撞唯一约束，回读既有记录。
		if again, e := model.FindImageByUserHash(ctx, s.db, userID, res.Hash); e == nil && again != nil {
			return again, nil
		}
		return nil, err
	}
	return img, nil
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		httpx.Fail(w, http.StatusUnauthorized, "请先登录")
		return
	}
	q := r.URL.Query()
	page := atoiDefault(q.Get("page"), 1)
	if page < 1 {
		page = 1
	}
	// 页码上限防止 (page-1)*size 在 int 上溢出成负数 offset。
	if page > maxPage {
		page = maxPage
	}
	size := atoiDefault(q.Get("page_size"), defaultPageSize)
	if size < 1 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	all := q.Get("scope") == "all" && u.Role == model.RoleAdmin

	imgs, total, err := model.ListImages(r.Context(), s.db, u.ID, all, size, (page-1)*size)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "读取图片列表失败")
		return
	}
	items := make([]imageOut, 0, len(imgs))
	for _, img := range imgs {
		items = append(items, s.present(r, img))
	}
	httpx.Data(w, http.StatusOK, map[string]any{
		"images":    items,
		"total":     total,
		"page":      page,
		"page_size": size,
	})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		httpx.Fail(w, http.StatusUnauthorized, "请先登录")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(w, http.StatusBadRequest, "无效的图片 ID")
		return
	}
	ctx := r.Context()
	img, err := model.GetImage(ctx, s.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.Fail(w, http.StatusNotFound, "图片不存在")
			return
		}
		httpx.Fail(w, http.StatusInternalServerError, "读取图片失败")
		return
	}
	if img.UserID != u.ID && u.Role != model.RoleAdmin {
		httpx.Fail(w, http.StatusForbidden, "无权删除该图片")
		return
	}
	if err := model.DeleteImage(ctx, s.db, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.Fail(w, http.StatusNotFound, "图片不存在")
			return
		}
		httpx.Fail(w, http.StatusInternalServerError, "删除失败")
		return
	}
	// 内容寻址：仅当没有其它记录引用同一 hash 时才删除磁盘文件（§3.1），
	// 同时清理指向它的旧路径别名。
	refs, err := model.CountImagesByHash(ctx, s.db, img.Hash)
	if err != nil {
		// 引用数未知时宁可不删文件，避免误删其它记录仍在使用的原图。
		slog.Warn("count image refs failed", "hash", img.Hash, "err", err)
	} else if refs == 0 {
		_ = s.store.Remove(img.Hash, img.Ext)
		_ = model.DeleteAliasesByHash(ctx, s.db, img.Hash)
	}
	httpx.Data(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

// ---------------------------------------------------------------- 管理设置

func (s *Server) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	httpx.Data(w, http.StatusOK, s.settings.Get())
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var p setting.Patch
	if !decodeJSON(w, r, &p) {
		return
	}
	v, err := s.settings.Update(r.Context(), p)
	if err != nil {
		// security: 校验错误回显文案；DB/驱动内部错误只回通用文案，
		// 原始错误进日志，避免向客户端暴露存储层细节。
		if errors.Is(err, setting.ErrInvalidSettings) {
			httpx.Fail(w, http.StatusBadRequest, err.Error())
			return
		}
		slog.Error("update settings failed", "err", err)
		httpx.Fail(w, http.StatusInternalServerError, "保存设置失败")
		return
	}
	httpx.Data(w, http.StatusOK, v)
}

// ---------------------------------------------------------------- 辅助

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxJSONBody)).Decode(dst); err != nil {
		httpx.Fail(w, http.StatusBadRequest, "请求体不是合法的 JSON")
		return false
	}
	return true
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// sanitizeFilename 只保留展示用的文件名，去掉路径与控制字符。
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '<' || r == '>' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "image"
	}
	if len(name) > 200 {
		// 按字节上限截断时退到 rune 边界，避免把多字节字符切成半个。
		cut := 200
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut]
	}
	return name
}
