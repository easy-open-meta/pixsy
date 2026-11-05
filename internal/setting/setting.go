// Package setting 管理系统设置（settings KV 表 + 内存缓存，§3.7）。
package setting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/pixsy/pixsy/internal/config"
)

// settings 表的键名（§3.7）。
const (
	KeySiteName      = "site_name"
	KeyAllowRegister = "allow_register"
	KeyAllowUpload   = "allow_upload"
	KeyMaxUploadMB   = "max_upload_mb"
	KeyMaxBatch      = "max_batch"
)

// DefaultMaxBatch 是单次批量上传文件数的默认上限（§3.7）。
const DefaultMaxBatch = 10

// MaxBatchLimit 限定 max_batch 的合法区间。
const MaxBatchLimit = 50

// Values 是一份完整设置快照；GET /api/v1/config 直接序列化它。
type Values struct {
	SiteName      string `json:"site_name"`
	AllowRegister bool   `json:"allow_register"`
	AllowUpload   bool   `json:"allow_upload"`
	MaxUploadMB   int    `json:"max_upload_mb"`
	MaxBatch      int    `json:"max_batch"`
}

// Defaults 返回 §3.7 的默认值；seedMaxUploadMB 来自 PIXSY_MAX_UPLOAD_MB。
func Defaults(seedMaxUploadMB int) Values {
	if seedMaxUploadMB < config.MinUploadMB || seedMaxUploadMB > config.MaxUploadMB {
		seedMaxUploadMB = 20
	}
	return Values{
		SiteName:      "pixsy",
		AllowRegister: true,
		AllowUpload:   true,
		MaxUploadMB:   seedMaxUploadMB,
		MaxBatch:      DefaultMaxBatch,
	}
}

// Patch 是 PUT /api/v1/admin/settings 的请求体：只有出现的字段才会被修改。
type Patch struct {
	SiteName      *string `json:"site_name"`
	AllowRegister *bool   `json:"allow_register"`
	AllowUpload   *bool   `json:"allow_upload"`
	MaxUploadMB   *int    `json:"max_upload_mb"`
	MaxBatch      *int    `json:"max_batch"`
}

// PatchFromFile 把配置文件里的 settings 段转换为 Patch（只含文件里出现的项）。
func PatchFromFile(f *config.FileSettings) Patch {
	if f == nil {
		return Patch{}
	}
	return Patch{
		SiteName:      f.SiteName,
		AllowRegister: f.AllowRegister,
		AllowUpload:   f.AllowUpload,
		MaxUploadMB:   f.MaxUploadMB,
		MaxBatch:      f.MaxBatch,
	}
}

// Store 把 settings 常驻内存：读路径不加锁地取快照，写路径落库后立即刷新，
// 因此开关修改即时生效且无需重启（§3.7）。
type Store struct {
	db *sql.DB

	mu  sync.RWMutex
	cur Values
}

// Load 播种缺失的默认值并加载全部设置。已有值不会被覆盖，运维的选择可跨重启保留。
func Load(ctx context.Context, db *sql.DB, seedMaxUploadMB int) (*Store, error) {
	d := Defaults(seedMaxUploadMB)
	s := &Store{db: db, cur: d}

	seeds := map[string]string{
		KeySiteName:      d.SiteName,
		KeyAllowRegister: strconv.FormatBool(d.AllowRegister),
		KeyAllowUpload:   strconv.FormatBool(d.AllowUpload),
		KeyMaxUploadMB:   strconv.Itoa(d.MaxUploadMB),
		KeyMaxBatch:      strconv.Itoa(d.MaxBatch),
	}
	for k, v := range seeds {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING`, k, v); err != nil {
			return nil, fmt.Errorf("seed setting %s: %w", k, err)
		}
	}

	vals, err := readAll(ctx, db, d)
	if err != nil {
		return nil, err
	}
	s.cur = vals
	return s, nil
}

// Get 返回当前设置快照；调用方拿到的是副本，可无限期持有。
func (s *Store) Get() Values {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// ErrInvalidSettings 包装参数校验类错误：调用方（API）可以据此把校验
// 文案回显给用户，而 DB/驱动内部错误只返回通用文案、详情进日志。
var ErrInvalidSettings = errors.New("invalid settings")

// Update 应用 patch，校验通过后写库并刷新缓存。
func (s *Store) Update(ctx context.Context, p Patch) (Values, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.cur
	if p.SiteName != nil {
		if len([]rune(*p.SiteName)) == 0 || len([]rune(*p.SiteName)) > 64 {
			return next, fmt.Errorf("%w: 站点名称需为 1~64 个字符", ErrInvalidSettings)
		}
		next.SiteName = *p.SiteName
	}
	if p.AllowRegister != nil {
		next.AllowRegister = *p.AllowRegister
	}
	if p.AllowUpload != nil {
		next.AllowUpload = *p.AllowUpload
	}
	if p.MaxUploadMB != nil {
		if *p.MaxUploadMB < config.MinUploadMB || *p.MaxUploadMB > config.MaxUploadMB {
			return next, fmt.Errorf("%w: 单文件上限需在 %d~%d MB 之间", ErrInvalidSettings, config.MinUploadMB, config.MaxUploadMB)
		}
		next.MaxUploadMB = *p.MaxUploadMB
	}
	if p.MaxBatch != nil {
		if *p.MaxBatch < 1 || *p.MaxBatch > MaxBatchLimit {
			return next, fmt.Errorf("%w: 批量上限需在 1~%d 之间", ErrInvalidSettings, MaxBatchLimit)
		}
		next.MaxBatch = *p.MaxBatch
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return s.cur, fmt.Errorf("begin settings tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmts := []struct{ key, value string }{
		{KeySiteName, next.SiteName},
		{KeyAllowRegister, strconv.FormatBool(next.AllowRegister)},
		{KeyAllowUpload, strconv.FormatBool(next.AllowUpload)},
		{KeyMaxUploadMB, strconv.Itoa(next.MaxUploadMB)},
		{KeyMaxBatch, strconv.Itoa(next.MaxBatch)},
	}
	for _, it := range stmts {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings(key, value) VALUES(?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, it.key, it.value); err != nil {
			return s.cur, fmt.Errorf("persist setting %s: %w", it.key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return s.cur, fmt.Errorf("commit settings: %w", err)
	}

	s.cur = next
	return next, nil
}

// readAll 读取整张表，缺失或损坏的值回退到默认值。
func readAll(ctx context.Context, db *sql.DB, d Values) (Values, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return d, fmt.Errorf("load settings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	raw := make(map[string]string, 8)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return d, fmt.Errorf("scan setting: %w", err)
		}
		raw[k] = v
	}
	if err := rows.Err(); err != nil {
		return d, fmt.Errorf("iterate settings: %w", err)
	}

	out := d
	if v, ok := raw[KeySiteName]; ok && v != "" {
		out.SiteName = v
	}
	if v, ok := raw[KeyAllowRegister]; ok {
		if b, err := strconv.ParseBool(v); err == nil {
			out.AllowRegister = b
		}
	}
	if v, ok := raw[KeyAllowUpload]; ok {
		if b, err := strconv.ParseBool(v); err == nil {
			out.AllowUpload = b
		}
	}
	if v, ok := raw[KeyMaxUploadMB]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= config.MinUploadMB && n <= config.MaxUploadMB {
			out.MaxUploadMB = n
		}
	}
	if v, ok := raw[KeyMaxBatch]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= MaxBatchLimit {
			out.MaxBatch = n
		}
	}
	return out, nil
}
