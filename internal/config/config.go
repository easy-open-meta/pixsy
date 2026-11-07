// Package config 加载 pixsy 的运行期配置：命令行 flag > PIXSY_* 环境变量 > 配置文件 > 默认值。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	defaultAddr        = ":8080"
	defaultDataDir     = "./data"
	defaultMaxUploadMB = 20
	defaultConfigFile  = "pixsy.json"

	// MinUploadMB / MaxUploadMB 限定 max_upload_mb 的合法区间（§3.7）。
	MinUploadMB = 1
	MaxUploadMB = 1024
)

// FileSettings 是配置文件 settings 段，对应后台页面可改的设置。
// 用指针区分「文件里没写」和「写了零值」：只有出现的项会在启动时覆盖后台设置。
type FileSettings struct {
	SiteName         *string `json:"site_name"`
	AllowRegister    *bool   `json:"allow_register"`
	AllowUpload      *bool   `json:"allow_upload"`
	MaxUploadMB      *int    `json:"max_upload_mb"`
	MaxBatch         *int    `json:"max_batch"`
	HotlinkWhitelist *string `json:"hotlink_whitelist"`
	HotlinkReferers  *string `json:"hotlink_referers"`
	HotlinkEmbedOnly *bool   `json:"hotlink_embed_only"`
}

// HasValues 报告是否至少有一项覆盖。
func (f *FileSettings) HasValues() bool {
	return f != nil && (f.SiteName != nil || f.AllowRegister != nil ||
		f.AllowUpload != nil || f.MaxUploadMB != nil || f.MaxBatch != nil ||
		f.HotlinkWhitelist != nil || f.HotlinkReferers != nil || f.HotlinkEmbedOnly != nil)
}

// fileConfig 是配置文件的反序列化结构；未出现的键保持 nil。
type fileConfig struct {
	Addr           *string       `json:"addr"`
	Port           *int          `json:"port"`
	Data           *string       `json:"data"`
	BaseURL        *string       `json:"base_url"`
	JWTSecret      *string       `json:"jwt_secret"`
	StaticDir      *string       `json:"static_dir"`
	OpenBrowser    *bool         `json:"open_browser"`
	TrustedProxies *string       `json:"trusted_proxies"`
	Settings       *FileSettings `json:"settings"`
}

// Config 是一次进程启动所需的全部配置。
type Config struct {
	Addr        string        // HTTP 监听地址
	DataDir     string        // 数据目录：SQLite + 原图 + 缩略图
	BaseURL     string        // 图片外链前缀（如 CDN 域名）；空则按请求 Host 生成
	MaxUploadMB int           // 首次启动写入 settings 的种子值
	JWTSecret   string        // 空则自动生成并落盘到 DataDir/jwt.secret
	StaticDir   string        // 前端静态资源目录；空则使用内嵌产物（开发热更新用）
	OpenBrowser bool          // 启动后自动用默认浏览器打开页面；默认仅 Windows 开启
	ConfigFile  string        // 实际加载的配置文件路径；空表示没有配置文件
	Settings    *FileSettings // 配置文件里的后台设置覆盖项；nil 表示不覆盖

	// TrustedProxiesRaw 是可信代理网段（CIDR 或单 IP，逗号分隔）；空表示直连部署、
	// 完全不信任 X-Forwarded-*。ParsedTrustedProxies 在 Validate 后可用。
	TrustedProxiesRaw    string
	parsedTrustedProxies []*net.IPNet
}

// ParsedTrustedProxies 返回解析后的可信代理网段；仅 Validate 成功后可用。
func (c *Config) ParsedTrustedProxies() []*net.IPNet { return c.parsedTrustedProxies }

// DBPath 返回 SQLite 文件路径。
func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "pixsy.db") }

// SecretPath 返回自动生成的 JWT 密钥文件路径。
func (c *Config) SecretPath() string { return filepath.Join(c.DataDir, "jwt.secret") }

// Load 解析 args（不含程序名）并返回校验后的配置。
func Load(args []string, errOut io.Writer) (*Config, error) {
	flags := flag.NewFlagSet("pixsy", flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.Usage = func() {
		fmt.Fprintln(errOut, "pixsy — 自托管轻量图库")
		fmt.Fprintln(errOut, "配置文件：--config 或 PIXSY_CONFIG；都不指定时自动加载当前目录的 pixsy.json")
		flags.PrintDefaults()
	}

	addr := flags.String("addr", "", "HTTP 监听地址（默认 :8080）")
	data := flags.String("data", "", "数据目录（默认 ./data）")
	baseURL := flags.String("base-url", "", "图片外链前缀，可填 CDN 域名")
	maxMB := flags.Int("max-upload-mb", 0, "首次启动写入 settings 的单文件上限 MB（默认 20）")
	jwtSecret := flags.String("jwt-secret", "", "JWT 签名密钥；留空则自动生成并存入数据目录")
	staticDir := flags.String("static-dir", "", "前端静态资源目录；留空则使用内嵌产物")
	openBrowser := flags.Bool("open-browser", false, "启动后自动打开浏览器（Windows 默认开启；可用 --open-browser=false 关闭）")
	trustedProxies := flags.String("trusted-proxies", "", "可信反向代理网段（CIDR，逗号分隔）；留空表示直连部署，忽略 X-Forwarded-* 头")
	cfgPath := flags.String("config", "", "配置文件路径（JSON）；留空自动加载当前目录的 pixsy.json")

	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	// flag.Visit 只访问显式传入的 flag，用于区分「用户没传」和「传了空串」。
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })

	path := strings.TrimSpace(*cfgPath)
	explicitFile := set["config"] || strings.TrimSpace(os.Getenv("PIXSY_CONFIG")) != ""
	if path == "" {
		path = strings.TrimSpace(os.Getenv("PIXSY_CONFIG"))
	}
	if path == "" {
		path = defaultConfigFile
	}
	fc, loaded, err := loadFile(path, explicitFile)
	if err != nil {
		return nil, err
	}

	// 优先级：显式 flag > 环境变量 > 配置文件 > 默认值。
	pick := func(name, flagVal, envKey string, fileVal *string, def string) string {
		if set[name] {
			return strings.TrimSpace(flagVal)
		}
		if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
			return v
		}
		if fileVal != nil {
			return strings.TrimSpace(*fileVal)
		}
		return def
	}
	pickInt := func(name string, flagVal int, envKey string, fileVal *int, def int) int {
		if set[name] {
			return flagVal
		}
		if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
		if fileVal != nil {
			return *fileVal
		}
		return def
	}
	pickBool := func(name string, flagVal bool, envKey string, fileVal *bool, def bool) bool {
		if set[name] {
			return flagVal
		}
		if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				return b
			}
		}
		if fileVal != nil {
			return *fileVal
		}
		return def
	}

	// 显式传空的 --data/PIXSY_DATA 要留给 Validate 报错；filepath.Clean("") 会变成 "."。
	dataDir := pick("data", *data, "PIXSY_DATA", fc.Data, defaultDataDir)
	if dataDir != "" {
		dataDir = filepath.Clean(dataDir)
	}

	cfg := &Config{
		Addr:              pick("addr", *addr, "PIXSY_ADDR", fc.Addr, defaultAddr),
		DataDir:           dataDir,
		BaseURL:           strings.TrimRight(pick("base-url", *baseURL, "PIXSY_BASE_URL", fc.BaseURL, ""), "/"),
		MaxUploadMB:       pickInt("max-upload-mb", *maxMB, "PIXSY_MAX_UPLOAD_MB", nil, defaultMaxUploadMB),
		JWTSecret:         pick("jwt-secret", *jwtSecret, "PIXSY_JWT_SECRET", fc.JWTSecret, ""),
		StaticDir:         pick("static-dir", *staticDir, "PIXSY_STATIC_DIR", fc.StaticDir, ""),
		OpenBrowser:       pickBool("open-browser", *openBrowser, "PIXSY_OPEN_BROWSER", fc.OpenBrowser, runtime.GOOS == "windows"),
		ConfigFile:        loaded,
		TrustedProxiesRaw: pick("trusted-proxies", *trustedProxies, "PIXSY_TRUSTED_PROXIES", fc.TrustedProxies, ""),
		Settings:          fc.Settings,
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadFile 读取配置文件；required=false 时文件不存在视为「没有配置文件」。
// 返回的第二个值是要记录的配置文件路径（未加载则为空）。
func loadFile(path string, required bool) (*fileConfig, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !required {
			return &fileConfig{}, "", nil
		}
		return nil, "", fmt.Errorf("读取配置文件 %s: %w", path, err)
	}

	var fc fileConfig
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields() // 拼错的键直接报错，避免配置静默失效
	if err := dec.Decode(&fc); err != nil {
		return nil, "", fmt.Errorf("解析配置文件 %s: %w", path, err)
	}

	if fc.Addr != nil && fc.Port != nil {
		return nil, "", fmt.Errorf("配置文件 %s 里 addr 和 port 只能填一个", path)
	}
	if fc.Port != nil {
		if *fc.Port < 1 || *fc.Port > 65535 {
			return nil, "", fmt.Errorf("配置文件 %s 的 port 必须在 1~65535 之间", path)
		}
		addr := ":" + strconv.Itoa(*fc.Port)
		fc.Addr = &addr
	}
	return &fc, path, nil
}

// Validate 检查配置是否可用。
func (c *Config) Validate() error {
	if c.Addr == "" {
		return errors.New("监听地址（--addr / PIXSY_ADDR）不能为空")
	}
	if c.DataDir == "" {
		return errors.New("数据目录（--data / PIXSY_DATA）不能为空")
	}
	if c.MaxUploadMB < MinUploadMB || c.MaxUploadMB > MaxUploadMB {
		return fmt.Errorf("max-upload-mb 必须在 %d~%d 之间", MinUploadMB, MaxUploadMB)
	}
	if c.BaseURL != "" && !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
		return errors.New("base-url 必须以 http:// 或 https:// 开头")
	}
	nets, err := ParseCIDRs(c.TrustedProxiesRaw)
	if err != nil {
		return fmt.Errorf("trusted-proxies: %w", err)
	}
	c.parsedTrustedProxies = nets
	return nil
}

// ParseCIDRs 解析逗号分隔的 CIDR/单 IP 列表；单 IP 视为 /32 或 /128。
// 供本包与 setting 包（外链 IP 白名单）复用。
func ParseCIDRs(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var nets []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			ip := net.ParseIP(part)
			if ip == nil {
				return nil, fmt.Errorf("无效的网段或 IP：%q", part)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			part = fmt.Sprintf("%s/%d", ip.String(), bits)
		}
		_, ipNet, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("无效的网段：%q", part)
		}
		nets = append(nets, ipNet)
	}
	return nets, nil
}
