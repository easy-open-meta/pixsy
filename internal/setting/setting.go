// Package setting 管理系统设置（settings KV 表 + 内存缓存，§3.7）。
package setting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
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
	// 外链防盗链白名单（§3.5）：逗号/换行分隔，空串表示未启用。
	KeyHotlinkWhitelist = "hotlink_whitelist" // 访客 IP/CIDR 网段
	KeyHotlinkReferers  = "hotlink_referers"  // 允许引用的站点域名
	// 仅嵌图模式：Fetch Metadata 的 Sec-Fetch-Dest 非 image 的文档级导航
	// （新标签页/地址栏直开）不放行，只认 IP 白名单与管理员 Cookie。
	KeyHotlinkEmbedOnly = "hotlink_embed_only"
)

// DefaultMaxBatch 是单次批量上传文件数的默认上限（§3.7）。
const DefaultMaxBatch = 10

// MaxBatchLimit 限定 max_batch 的合法区间。
const MaxBatchLimit = 50

// maxWhitelistEntries / maxWhitelistBytes 限定单个白名单的规模，防止异常输入拖慢解析与存储。
const (
	maxWhitelistEntries = 64
	maxWhitelistBytes   = 4096
)

// Values 是一份完整设置快照；管理端 GET/PUT /api/v1/admin/settings 直接序列化它。
// 对外公开的 GET /api/v1/config 必须走 Public()，白名单条目不对外暴露。
type Values struct {
	SiteName         string `json:"site_name"`
	AllowRegister    bool   `json:"allow_register"`
	AllowUpload      bool   `json:"allow_upload"`
	MaxUploadMB      int    `json:"max_upload_mb"`
	MaxBatch         int    `json:"max_batch"`
	HotlinkWhitelist string `json:"hotlink_whitelist"`
	HotlinkReferers  string `json:"hotlink_referers"`
	HotlinkEmbedOnly bool   `json:"hotlink_embed_only"`
}

// Public 返回公开配置视图：GET /api/v1/config 面向未登录访客，
// 白名单属于访问控制信息，不对外暴露（§3.3）。
func (v Values) Public() any {
	return struct {
		SiteName      string `json:"site_name"`
		AllowRegister bool   `json:"allow_register"`
		AllowUpload   bool   `json:"allow_upload"`
		MaxUploadMB   int    `json:"max_upload_mb"`
		MaxBatch      int    `json:"max_batch"`
	}{
		SiteName:      v.SiteName,
		AllowRegister: v.AllowRegister,
		AllowUpload:   v.AllowUpload,
		MaxUploadMB:   v.MaxUploadMB,
		MaxBatch:      v.MaxBatch,
	}
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
	SiteName         *string `json:"site_name"`
	AllowRegister    *bool   `json:"allow_register"`
	AllowUpload      *bool   `json:"allow_upload"`
	MaxUploadMB      *int    `json:"max_upload_mb"`
	MaxBatch         *int    `json:"max_batch"`
	HotlinkWhitelist *string `json:"hotlink_whitelist"`
	HotlinkReferers  *string `json:"hotlink_referers"`
	HotlinkEmbedOnly *bool   `json:"hotlink_embed_only"`
}

// PatchFromFile 把配置文件里的 settings 段转换为 Patch（只含文件里出现的项）。
func PatchFromFile(f *config.FileSettings) Patch {
	if f == nil {
		return Patch{}
	}
	return Patch{
		SiteName:         f.SiteName,
		AllowRegister:    f.AllowRegister,
		AllowUpload:      f.AllowUpload,
		MaxUploadMB:      f.MaxUploadMB,
		MaxBatch:         f.MaxBatch,
		HotlinkWhitelist: f.HotlinkWhitelist,
		HotlinkReferers:  f.HotlinkReferers,
		HotlinkEmbedOnly: f.HotlinkEmbedOnly,
	}
}

// HotlinkRules 是外链访问控制的解析快照（§3.5）。
// EmbedOnly 独立于两个白名单生效：仅开启它时，嵌图加载不受限、文档级导航一律拦截。
type HotlinkRules struct {
	Nets      []*net.IPNet // 访客 IP/CIDR 白名单
	Domains   []string     // 允许引用的站点域名（小写）
	EmbedOnly bool         // 仅嵌图模式：Sec-Fetch-Dest 非 image 的文档级导航不放行
}

// Store 把 settings 常驻内存：读路径不加锁地取快照，写路径落库后立即刷新，
// 因此开关修改即时生效且无需重启（§3.7）。
type Store struct {
	db *sql.DB

	mu    sync.RWMutex
	cur   Values
	rules HotlinkRules // cur 里两个白名单的解析结果，随 Load/Update 一并刷新
}

// Load 播种缺失的默认值并加载全部设置。已有值不会被覆盖，运维的选择可跨重启保留。
func Load(ctx context.Context, db *sql.DB, seedMaxUploadMB int) (*Store, error) {
	d := Defaults(seedMaxUploadMB)
	s := &Store{db: db, cur: d}

	seeds := map[string]string{
		KeySiteName:         d.SiteName,
		KeyAllowRegister:    strconv.FormatBool(d.AllowRegister),
		KeyAllowUpload:      strconv.FormatBool(d.AllowUpload),
		KeyMaxUploadMB:      strconv.Itoa(d.MaxUploadMB),
		KeyMaxBatch:         strconv.Itoa(d.MaxBatch),
		KeyHotlinkWhitelist: d.HotlinkWhitelist,
		KeyHotlinkReferers:  d.HotlinkReferers,
		KeyHotlinkEmbedOnly: strconv.FormatBool(d.HotlinkEmbedOnly),
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
	s.rules = rulesFor(vals)
	return s, nil
}

// Get 返回当前设置快照；调用方拿到的是副本，可无限期持有。
func (s *Store) Get() Values {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// HotlinkRules 返回外链访问控制的解析快照，供图片直出路由逐请求复用，
// 避免每次请求重新解析 CIDR；调用方拿到的是副本，切片内容只读。
func (s *Store) HotlinkRules() HotlinkRules {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rules
}

// rulesFor 解析 Values 里的防盗链配置。写入路径已校验过，这里遇到非法条目
// （如手工改库）跳过该条目即可，不让单条脏数据拖垮图片直出。
func rulesFor(v Values) HotlinkRules {
	var rules HotlinkRules
	rules.EmbedOnly = v.HotlinkEmbedOnly
	if v.HotlinkWhitelist != "" {
		if nets, err := config.ParseCIDRs(v.HotlinkWhitelist); err == nil {
			rules.Nets = nets
		}
	}
	if v.HotlinkReferers != "" {
		for _, d := range strings.Split(v.HotlinkReferers, ",") {
			if d = strings.TrimSpace(d); d != "" {
				rules.Domains = append(rules.Domains, strings.ToLower(d))
			}
		}
	}
	return rules
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
	if p.HotlinkWhitelist != nil {
		n, err := normalizeIPList(*p.HotlinkWhitelist)
		if err != nil {
			return next, fmt.Errorf("%w: %v", ErrInvalidSettings, err)
		}
		next.HotlinkWhitelist = n
	}
	if p.HotlinkReferers != nil {
		n, err := normalizeDomainList(*p.HotlinkReferers)
		if err != nil {
			return next, fmt.Errorf("%w: %v", ErrInvalidSettings, err)
		}
		next.HotlinkReferers = n
	}
	if p.HotlinkEmbedOnly != nil {
		next.HotlinkEmbedOnly = *p.HotlinkEmbedOnly
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
		{KeyHotlinkWhitelist, next.HotlinkWhitelist},
		{KeyHotlinkReferers, next.HotlinkReferers},
		{KeyHotlinkEmbedOnly, strconv.FormatBool(next.HotlinkEmbedOnly)},
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
	s.rules = rulesFor(next)
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
	if v, ok := raw[KeyHotlinkWhitelist]; ok {
		if n, err := normalizeIPList(v); err == nil {
			out.HotlinkWhitelist = n
		}
	}
	if v, ok := raw[KeyHotlinkReferers]; ok {
		if n, err := normalizeDomainList(v); err == nil {
			out.HotlinkReferers = n
		}
	}
	if v, ok := raw[KeyHotlinkEmbedOnly]; ok {
		if b, err := strconv.ParseBool(v); err == nil {
			out.HotlinkEmbedOnly = b
		}
	}
	return out, nil
}

// normalizeIPList 规范化 IP/CIDR 白名单：逗号或换行分隔、逐项校验、去重，
// 以逗号连接返回；空串表示未启用。
func normalizeIPList(raw string) (string, error) {
	return normalizeList(raw, func(item string) (string, error) {
		// ParseCIDRs 同时接受单 IP（视为 /32、/128）与 CIDR 网段。
		if _, err := config.ParseCIDRs(item); err != nil {
			return "", fmt.Errorf("IP 白名单条目 %q 不是合法的 IP 或网段", item)
		}
		return item, nil
	})
}

// normalizeDomainList 规范化引用域名白名单：逐项小写化并校验为纯 host。
func normalizeDomainList(raw string) (string, error) {
	return normalizeList(raw, validateDomain)
}

// normalizeList 拆分（逗号/换行）、trim、丢空项、逐项 prepare、去重，
// 以逗号连接返回；任一项目标式不合法即整体报错（调用方回显给管理员）。
func normalizeList(raw string, prepare func(string) (string, error)) (string, error) {
	seen := make(map[string]bool)
	var items []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		item, err := prepare(part)
		if err != nil {
			return "", err
		}
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		items = append(items, item)
	}
	if len(items) > maxWhitelistEntries {
		return "", fmt.Errorf("条目数不能超过 %d 条", maxWhitelistEntries)
	}
	out := strings.Join(items, ",")
	if len(out) > maxWhitelistBytes {
		return "", fmt.Errorf("列表总长不能超过 %d 字节", maxWhitelistBytes)
	}
	return out, nil
}

// validateDomain 校验单个引用域名：纯 host（不带 scheme/端口/路径），
// 标签为字母数字与横线、不以横线开头结尾、无空标签；返回小写形式。
func validateDomain(item string) (string, error) {
	d := strings.ToLower(item)
	invalid := fmt.Errorf("引用域名 %q 不是合法的域名", item)
	if d == "" || len(d) > 253 {
		return "", invalid
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", invalid
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return "", invalid
			}
		}
	}
	return d, nil
}
