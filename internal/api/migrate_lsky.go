package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pixsy/pixsy/internal/auth"
	"github.com/pixsy/pixsy/internal/httpx"
	"github.com/pixsy/pixsy/internal/model"
	"github.com/pixsy/pixsy/internal/storage"
)

// Lsky Pro(开源版 lsky-org/lsky-pro)迁移：服务端按 Lsky 的 API 拉清单、下载、
// 落到本地存储并入库。任务进度只存内存，重启后重跑即可——内容寻址保证去重。
const (
	lskyTokenPath     = "/api/v1/tokens"
	lskyImagesPath    = "/api/v1/images"
	lskyMaxItems      = 10000 // 250 页 × 40 条，防止无界拉取
	lskyClientTimeout = 120 * time.Second
	lskyMaxFailures   = 20 // 状态接口里最多回显的失败条数
)

var errMigrationCancelled = errors.New("migration cancelled")

// lskyJob 是一次迁移的输入。
type lskyJob struct {
	baseURL   string // 规范化后的 Lsky 地址（无尾斜杠）
	email     string
	password  string
	userID    int64  // 图片归属：发起迁移的管理员
	pixsyBase string // 生成新链接用的站点前缀（来自发起请求的 Host）
}

// lskyMappingRow 是一张图片的迁移记录，用于导出 旧URL→新URL 的 CSV。
type lskyMappingRow struct {
	Name     string
	LskyURL  string
	PixsyURL string
	Markdown string
	Status   string // ok / fail
	Error    string
}

// lskyMigrator 持有当前（或最近一次）迁移任务的进度，单进程内同时只允许一个任务。
type lskyMigrator struct {
	mu     sync.Mutex
	status lskyStatus
	rows   []lskyMappingRow
	cancel context.CancelFunc
}

type lskyStatus struct {
	Running    bool
	Total      int
	Done       int
	Failed     int
	Current    string
	Error      string
	Failures   []string
	StartedAt  time.Time
	FinishedAt time.Time
}

// lskyStatusView 是状态接口的响应体。
type lskyStatusView struct {
	Running    bool     `json:"running"`
	Total      int      `json:"total"`
	Done       int      `json:"done"`
	Failed     int      `json:"failed"`
	Current    string   `json:"current"`
	Error      string   `json:"error"`
	Failures   []string `json:"failures"`
	StartedAt  string   `json:"started_at,omitempty"`
	FinishedAt string   `json:"finished_at,omitempty"`
	Mapping    int      `json:"mapping_count"`
}

// begin 启动任务；已有任务在跑时返回 false。
func (m *lskyMigrator) begin(run func(ctx context.Context) error) bool {
	m.mu.Lock()
	if m.status.Running {
		m.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.rows = nil
	m.status = lskyStatus{Running: true, StartedAt: time.Now()}
	m.mu.Unlock()

	go func() {
		err := run(ctx)
		m.finish(err)
	}()
	return true
}

func (m *lskyMigrator) finish(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Running = false
	m.status.Current = ""
	m.status.FinishedAt = time.Now()
	m.cancel = nil
	switch {
	case errors.Is(err, errMigrationCancelled):
		m.status.Error = "迁移已取消"
	case err != nil:
		m.status.Error = err.Error()
	}
}

func (m *lskyMigrator) setTotal(total int) {
	m.mu.Lock()
	m.status.Total = total
	m.mu.Unlock()
}

func (m *lskyMigrator) setCurrent(current string) {
	m.mu.Lock()
	m.status.Current = current
	m.mu.Unlock()
}

func (m *lskyMigrator) recordRow(row lskyMappingRow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, row)
	if row.Status == "ok" {
		m.status.Done++
		return
	}
	m.status.Failed++
	if len(m.status.Failures) < lskyMaxFailures {
		m.status.Failures = append(m.status.Failures, row.Name+": "+row.Error)
	}
}

// cancelJob 中断正在运行的任务；无可取消任务时返回 false。
func (m *lskyMigrator) cancelJob() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.status.Running || m.cancel == nil {
		return false
	}
	m.cancel()
	return true
}

func (m *lskyMigrator) snapshot() lskyStatusView {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 失败列表保持非 nil，前端拿到的是 [] 而不是 null。
	failures := make([]string, len(m.status.Failures))
	copy(failures, m.status.Failures)
	v := lskyStatusView{
		Running:  m.status.Running,
		Total:    m.status.Total,
		Done:     m.status.Done,
		Failed:   m.status.Failed,
		Current:  m.status.Current,
		Error:    m.status.Error,
		Failures: failures,
		Mapping:  len(m.rows),
	}
	if !m.status.StartedAt.IsZero() {
		v.StartedAt = m.status.StartedAt.Format(time.RFC3339)
	}
	if !m.status.FinishedAt.IsZero() {
		v.FinishedAt = m.status.FinishedAt.Format(time.RFC3339)
	}
	return v
}

func (m *lskyMigrator) rowsCopy() []lskyMappingRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]lskyMappingRow(nil), m.rows...)
}

// ---------------------------------------------------------------- 处理器

type lskyStartInput struct {
	BaseURL  string `json:"base_url"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleStartLskyMigration(w http.ResponseWriter, r *http.Request) {
	var in lskyStartInput
	if !decodeJSON(w, r, &in) {
		return
	}
	base, err := normalizeLskyBase(in.BaseURL)
	if err != nil {
		httpx.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	email := strings.TrimSpace(in.Email)
	if email == "" || in.Password == "" {
		httpx.Fail(w, http.StatusBadRequest, "请填写 Lsky 的登录邮箱和密码")
		return
	}
	u := auth.UserFrom(r.Context())
	if u == nil {
		httpx.Fail(w, http.StatusUnauthorized, "请先登录")
		return
	}

	job := lskyJob{
		baseURL:   base,
		email:     email,
		password:  in.Password,
		userID:    u.ID,
		pixsyBase: s.baseURLFor(r),
	}
	if !s.migrator.begin(func(ctx context.Context) error {
		return s.runLskyMigration(ctx, job)
	}) {
		httpx.Fail(w, http.StatusConflict, "已有迁移任务在进行中，请等待完成或先取消")
		return
	}
	httpx.Data(w, http.StatusAccepted, s.migrator.snapshot())
}

func (s *Server) handleLskyMigrationStatus(w http.ResponseWriter, _ *http.Request) {
	httpx.Data(w, http.StatusOK, s.migrator.snapshot())
}

func (s *Server) handleCancelLskyMigration(w http.ResponseWriter, _ *http.Request) {
	s.migrator.cancelJob()
	httpx.Data(w, http.StatusOK, s.migrator.snapshot())
}

func (s *Server) handleLskyMigrationCSV(w http.ResponseWriter, _ *http.Request) {
	rows := s.migrator.rowsCopy()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="lsky-pixsy-mapping.csv"`)
	_, _ = w.Write([]byte("\uFEFF")) // Excel 打开中文不乱码
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"lsky_name", "lsky_url", "pixsy_url", "pixsy_markdown", "status", "error"})
	for _, r := range rows {
		_ = cw.Write([]string{r.Name, r.LskyURL, r.PixsyURL, r.Markdown, r.Status, r.Error})
	}
	cw.Flush()
}

// ---------------------------------------------------------------- 任务执行

func normalizeLskyBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("请填写 Lsky 地址")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("Lsky 地址格式不正确，例如 https://img.example.com")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("Lsky 地址必须以 http:// 或 https:// 开头")
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/"), nil
}

func (s *Server) runLskyMigration(ctx context.Context, job lskyJob) error {
	// API 调用不跟随重定向：Laravel 在未认证且没带 JSON Accept 时会 302 到
	// /login 的 HTML 页面，跟随过去只会得到一个伪装成 200 的登录页。
	apiClient := &http.Client{
		Timeout: lskyClientTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	downloadClient := &http.Client{Timeout: lskyClientTimeout}

	token, err := lskyLogin(ctx, apiClient, job.baseURL, job.email, job.password)
	if err != nil {
		return err
	}

	items, err := lskyListImages(ctx, apiClient, job.baseURL, token)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return errors.New("Lsky 中没有可迁移的图片")
	}
	s.migrator.setTotal(len(items))

	maxBytes := int64(s.settings.Get().MaxUploadMB) << 20
	for i, item := range items {
		if ctx.Err() != nil {
			return errMigrationCancelled
		}
		name := item.displayName()
		s.migrator.setCurrent(fmt.Sprintf("%s（%d/%d）", name, i+1, len(items)))
		s.migrator.recordRow(s.migrateLskyImage(ctx, downloadClient, job, item, maxBytes))
	}
	if ctx.Err() != nil {
		return errMigrationCancelled
	}
	return nil
}

func (s *Server) migrateLskyImage(ctx context.Context, client *http.Client, job lskyJob, item lskyImage, maxBytes int64) lskyMappingRow {
	name := item.displayName()
	lskyURL := item.imageURL(job.baseURL)
	row := lskyMappingRow{Name: name, LskyURL: lskyURL, Status: "fail"}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lskyURL, nil)
	if err != nil {
		row.Error = "图片地址无效"
		return row
	}
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			row.Error = "迁移已取消"
		} else {
			row.Error = "下载失败：" + err.Error()
		}
		return row
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		row.Error = fmt.Sprintf("下载失败 HTTP %d", res.StatusCode)
		return row
	}

	saved, err := s.store.Save(ctx, res.Body, maxBytes)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrTooLarge):
			row.Error = "文件超出体积上限"
		case errors.Is(err, storage.ErrUnsupported):
			row.Error = "不支持的图片格式"
		case errors.Is(err, context.Canceled):
			row.Error = "迁移已取消"
		default:
			slog.Error("lsky migrate save failed", "url", lskyURL, "err", err)
			row.Error = "保存文件失败"
		}
		return row
	}

	img, err := s.record(ctx, job.userID, name, saved)
	if err != nil {
		slog.Error("lsky migrate record failed", "url", lskyURL, "err", err)
		row.Error = "写入图片记录失败"
		return row
	}

	// 记录旧路径别名：Lsky 的 /i/{pathname} 换域名后在新站原样可访问。
	if aliasPath := strings.Trim(strings.TrimSpace(item.Pathname), "/"); aliasPath != "" {
		if err := model.InsertAlias(ctx, s.db, aliasPath, img.Hash, img.Ext); err != nil {
			slog.Warn("lsky alias insert failed", "alias", aliasPath, "err", err)
		}
	}

	raw := job.pixsyBase + "/i/" + img.Hash + "." + img.Ext
	row.Status = "ok"
	row.PixsyURL = raw
	row.Markdown = "![" + img.Filename + "](" + raw + ")"
	return row
}

// ---------------------------------------------------------------- Lsky API

type lskyTokenResp struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		Token string `json:"token"`
	} `json:"data"`
}

func lskyLogin(ctx context.Context, client *http.Client, base, email, password string) (string, error) {
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		return "", err
	}
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+lskyTokenPath, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		// 关键：不带 JSON Accept 时 Laravel 会把异常请求 302 到 /login 的 HTML。
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("连接 Lsky 失败：%w", err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		_ = res.Body.Close()
		if readErr != nil {
			return "", fmt.Errorf("读取 Lsky 响应失败：%w", readErr)
		}

		// Lsky 的 token 接口限流 3 次/分钟，撞上就等一会儿再来。
		if res.StatusCode == http.StatusTooManyRequests && attempt <= 3 {
			if err := sleepCtx(ctx, 20*time.Second); err != nil {
				return "", err
			}
			continue
		}
		if res.StatusCode != http.StatusOK {
			return "", fmt.Errorf("Lsky 登录接口异常（HTTP %d）%s", res.StatusCode, lskyHint(res.StatusCode))
		}

		var out lskyTokenResp
		if err := json.Unmarshal(trimBOM(raw), &out); err != nil {
			return "", fmt.Errorf("Lsky 登录响应不是预期的 JSON（HTTP %d，Content-Type: %s）：%v",
				res.StatusCode, res.Header.Get("Content-Type"), err)
		}
		if !out.Status || out.Data.Token == "" {
			msg := strings.TrimSpace(out.Message)
			if msg == "" {
				msg = fmt.Sprintf("HTTP %d", res.StatusCode)
			}
			return "", fmt.Errorf("Lsky 登录失败：%s", msg)
		}
		return out.Data.Token, nil
	}
}

// lskyImage 只保留迁移用得到的字段。Lsky 的 size 是 float（模型里 cast 为 'float'，
// JSON 形如 70.0），解析成整型会失败，所以不声明未使用的数字字段；字符串字段对新版本也更宽容。
type lskyImage struct {
	Name       string `json:"name"`
	OriginName string `json:"origin_name"`
	Pathname   string `json:"pathname"`
	Extension  string `json:"extension"`
	Links      struct {
		URL string `json:"url"`
	} `json:"links"`
}

func (i lskyImage) displayName() string {
	if n := strings.TrimSpace(i.OriginName); n != "" {
		return n
	}
	if n := strings.TrimSpace(i.Name); n != "" {
		return n
	}
	return strings.TrimSpace(i.Pathname)
}

func (i lskyImage) imageURL(base string) string {
	u := strings.TrimSpace(i.Links.URL)
	if u == "" {
		u = "/" + strings.TrimLeft(i.Pathname, "/")
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = strings.TrimRight(base, "/") + "/" + strings.TrimLeft(u, "/")
	}
	return u
}

type lskyPageResp struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		Data     []lskyImage `json:"data"`
		LastPage int         `json:"last_page"`
		Total    int         `json:"total"`
	} `json:"data"`
}

func lskyListImages(ctx context.Context, client *http.Client, base, token string) ([]lskyImage, error) {
	var all []lskyImage
	lastPage := 1
	for page := 1; page <= lastPage; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("%s%s?page=%d", base, lskyImagesPath, page), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("读取 Lsky 图片清单失败：%w", err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 8<<20))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("读取 Lsky 图片清单失败：%w", readErr)
		}

		if res.StatusCode == http.StatusUnauthorized {
			return nil, errors.New("Lsky 认证失败（401），请检查账号密码，或确认该站点已启用 API")
		}
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("读取 Lsky 图片清单失败（HTTP %d）%s", res.StatusCode, lskyHint(res.StatusCode))
		}
		var out lskyPageResp
		if err := json.Unmarshal(trimBOM(raw), &out); err != nil {
			return nil, fmt.Errorf("Lsky 图片清单响应不是预期的 JSON（HTTP %d，Content-Type: %s）：%v",
				res.StatusCode, res.Header.Get("Content-Type"), err)
		}
		if !out.Status {
			msg := strings.TrimSpace(out.Message)
			if msg == "" {
				msg = "未知错误"
			}
			return nil, fmt.Errorf("读取 Lsky 图片清单失败：%s", msg)
		}
		if len(out.Data.Data) == 0 {
			break
		}
		all = append(all, out.Data.Data...)
		if len(all) > lskyMaxItems {
			return nil, fmt.Errorf("图片超过 %d 张，请先在 Lsky 端分批处理", lskyMaxItems)
		}
		lastPage = out.Data.LastPage
	}
	return all, nil
}

// trimBOM 去掉部分 PHP 部署会在 JSON 前输出的 UTF-8 BOM（json.Unmarshal 不接受）。
func trimBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
}

// lskyHint 针对常见失败状态码给出排查提示。
func lskyHint(status int) string {
	switch {
	case status >= 300 && status < 400:
		return "，接口被重定向到登录页，请确认该 Lsky 站点已启用 API"
	case status == http.StatusNotFound:
		return "，接口不存在，请确认地址和 Lsky 版本"
	}
	return ""
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
