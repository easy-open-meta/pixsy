package config

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PIXSY_ADDR", "PIXSY_DATA", "PIXSY_BASE_URL", "PIXSY_MAX_UPLOAD_MB",
		"PIXSY_JWT_SECRET", "PIXSY_STATIC_DIR", "PIXSY_CONFIG", "PIXSY_OPEN_BROWSER",
	} {
		t.Setenv(k, "")
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "pixsy.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	chdir(t, t.TempDir())

	cfg, err := Load(nil, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.DataDir != "data" {
		t.Fatalf("DataDir = %q, want data", cfg.DataDir)
	}
	if cfg.MaxUploadMB != defaultMaxUploadMB {
		t.Fatalf("MaxUploadMB = %d, want %d", cfg.MaxUploadMB, defaultMaxUploadMB)
	}
	if cfg.ConfigFile != "" || cfg.Settings != nil {
		t.Fatalf("没有配置文件时应留空: file=%q settings=%v", cfg.ConfigFile, cfg.Settings)
	}
	if want := runtime.GOOS == "windows"; cfg.OpenBrowser != want {
		t.Fatalf("OpenBrowser 默认值 = %v, want %v（%s）", cfg.OpenBrowser, want, runtime.GOOS)
	}
}

func TestOpenBrowserPrecedence(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	writeConfig(t, dir, `{"open_browser": false}`)

	cfg, err := Load(nil, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenBrowser {
		t.Fatal("配置文件 open_browser=false 应生效")
	}

	t.Setenv("PIXSY_OPEN_BROWSER", "true")
	cfg, err = Load(nil, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.OpenBrowser {
		t.Fatal("环境变量应覆盖配置文件")
	}

	cfg, err = Load([]string{"--open-browser=false"}, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenBrowser {
		t.Fatal("显式 flag 应覆盖环境变量")
	}
}

func TestConfigFileValues(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	writeConfig(t, dir, `{
		"port": 9090,
		"data": "D:/pixsy-data",
		"base_url": "https://img.example.com/",
		"settings": {"site_name": "我的图床", "allow_register": false, "max_batch": 5}
	}`)

	cfg, err := Load(nil, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Fatalf("port 应转换为监听地址, got %q", cfg.Addr)
	}
	if cfg.DataDir != filepath.Clean("D:/pixsy-data") {
		t.Fatalf("DataDir = %q", cfg.DataDir)
	}
	if cfg.BaseURL != "https://img.example.com" {
		t.Fatalf("BaseURL 应去掉末尾斜杠, got %q", cfg.BaseURL)
	}
	if cfg.ConfigFile != "pixsy.json" {
		t.Fatalf("ConfigFile = %q, want pixsy.json", cfg.ConfigFile)
	}
	s := cfg.Settings
	if !s.HasValues() || s.SiteName == nil || *s.SiteName != "我的图床" ||
		s.AllowRegister == nil || *s.AllowRegister ||
		s.MaxBatch == nil || *s.MaxBatch != 5 {
		t.Fatalf("settings 解析结果不对: %+v", s)
	}
	if s.AllowUpload != nil || s.MaxUploadMB != nil {
		t.Fatalf("文件里未出现的设置项应保持 nil: %+v", s)
	}
}

func TestPrecedence(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	writeConfig(t, dir, `{"addr": ":9000"}`)

	cfg, err := Load(nil, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9000" {
		t.Fatalf("配置文件应生效, got %q", cfg.Addr)
	}

	t.Setenv("PIXSY_ADDR", ":9100")
	cfg, err = Load(nil, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9100" {
		t.Fatalf("环境变量应覆盖配置文件, got %q", cfg.Addr)
	}

	cfg, err = Load([]string{"--addr", ":9200"}, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9200" {
		t.Fatalf("flag 应覆盖环境变量, got %q", cfg.Addr)
	}
}

func TestExplicitConfigFileMissing(t *testing.T) {
	clearEnv(t)
	chdir(t, t.TempDir())

	_, err := Load([]string{"--config", "nope.json"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "读取配置文件") {
		t.Fatalf("显式指定的配置文件不存在时应报错, got %v", err)
	}
}

func TestConfigFromEnvPath(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	path := filepath.Join(dir, "custom.json")
	if err := os.WriteFile(path, []byte(`{"addr": ":9300"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIXSY_CONFIG", path)

	cfg, err := Load(nil, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9300" || cfg.ConfigFile != path {
		t.Fatalf("PIXSY_CONFIG 未生效: addr=%q file=%q", cfg.Addr, cfg.ConfigFile)
	}
}

func TestConfigFileErrors(t *testing.T) {
	cases := []struct {
		name, content, want string
	}{
		{"未知键", `{"prot": 9090}`, "unknown field"},
		{"addr 与 port 冲突", `{"addr": ":8080", "port": 9090}`, "只能填一个"},
		{"端口越界", `{"port": 70000}`, "1~65535"},
		{"非法 JSON", `{`, "解析配置文件"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			dir := t.TempDir()
			chdir(t, dir)
			writeConfig(t, dir, tc.content)

			_, err := Load(nil, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want contains %q", err, tc.want)
			}
		})
	}
}

// 显式传空的 --data/PIXSY_DATA 应触发 Validate 报错，
// 而不是被 filepath.Clean("") 静默变成 "."（会把数据写进当前目录）。
func TestLoadRejectsEmptyDataFlag(t *testing.T) {
	clearEnv(t)
	chdir(t, t.TempDir())

	if _, err := Load([]string{"--data", ""}, io.Discard); err == nil {
		t.Fatal("Load(--data \"\") succeeded, want validation error")
	}
	if _, err := Load([]string{}, io.Discard); err != nil {
		t.Fatalf("Load(default) failed: %v", err)
	}
}

// ParseCIDRs 同时接受单 IP（视为 /32、/128）与 CIDR 网段；trusted-proxies
// 与 setting 包的外链 IP 白名单共用此解析。
func TestParseCIDRs(t *testing.T) {
	nets, err := ParseCIDRs("10.0.0.1, 192.168.1.0/24")
	if err != nil {
		t.Fatalf("ParseCIDRs: %v", err)
	}
	if len(nets) != 2 {
		t.Fatalf("应解析出 2 个网段, got %d", len(nets))
	}
	if !nets[0].Contains(net.ParseIP("10.0.0.1")) || nets[0].Contains(net.ParseIP("10.0.0.2")) {
		t.Fatalf("单 IP 应视为 /32: %v", nets[0])
	}
	if !nets[1].Contains(net.ParseIP("192.168.1.77")) || nets[1].Contains(net.ParseIP("192.168.2.1")) {
		t.Fatalf("CIDR 未生效: %v", nets[1])
	}
	if _, err := ParseCIDRs("bad-ip"); err == nil {
		t.Fatal("非法条目应报错")
	}
	if nets, err := ParseCIDRs(""); err != nil || nets != nil {
		t.Fatalf("空串应返回 nil: %v %v", nets, err)
	}
}
