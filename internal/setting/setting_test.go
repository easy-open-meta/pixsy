package setting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pixsy/pixsy/internal/config"
	"github.com/pixsy/pixsy/internal/model"
)

func intPtr(n int) *int { return &n }

func strPtr(s string) *string { return &s }

func TestPatchFromFile(t *testing.T) {
	name, mb := "我的图床", 50
	ip, domain := "203.0.113.7", "example.com"
	p := PatchFromFile(&config.FileSettings{SiteName: &name, MaxUploadMB: &mb, HotlinkWhitelist: &ip, HotlinkReferers: &domain})
	if p.SiteName == nil || *p.SiteName != name {
		t.Fatalf("SiteName 未映射: %+v", p)
	}
	if p.MaxUploadMB == nil || *p.MaxUploadMB != mb {
		t.Fatalf("MaxUploadMB 未映射: %+v", p)
	}
	if p.HotlinkWhitelist == nil || *p.HotlinkWhitelist != ip {
		t.Fatalf("HotlinkWhitelist 未映射: %+v", p)
	}
	if p.HotlinkReferers == nil || *p.HotlinkReferers != domain {
		t.Fatalf("HotlinkReferers 未映射: %+v", p)
	}
	if p.AllowRegister != nil || p.AllowUpload != nil || p.MaxBatch != nil {
		t.Fatalf("文件里未出现的项应为 nil: %+v", p)
	}
	if got := PatchFromFile(nil); got != (Patch{}) {
		t.Fatalf("nil 应得到空 Patch, got %+v", got)
	}
}

// 配置文件覆盖项应在启动时应用到数据库，且不影响后台改过的其他项。
func TestConfigOverridesPersistedSettings(t *testing.T) {
	db, err := model.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	first, err := Load(ctx, db, 10)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := first.Get().MaxUploadMB; got != 10 {
		t.Fatalf("种子值 MaxUploadMB = %d, want 10", got)
	}

	// 管理员在后台把上限改成 30，重启后应保留（既有行为）。
	if _, err := first.Update(ctx, Patch{MaxUploadMB: intPtr(30)}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	second, err := Load(ctx, db, 10)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := second.Get().MaxUploadMB; got != 30 {
		t.Fatalf("后台设置应跨重启保留, got %d", got)
	}

	// 配置文件里的覆盖项启动时强制生效，未出现的项不受影响。
	name := "fig"
	if _, err := second.Update(ctx, PatchFromFile(&config.FileSettings{
		SiteName: &name,
		MaxBatch: intPtr(20),
	})); err != nil {
		t.Fatalf("应用文件设置: %v", err)
	}
	v := second.Get()
	if v.SiteName != "fig" || v.MaxBatch != 20 {
		t.Fatalf("文件设置未生效: %+v", v)
	}
	if v.MaxUploadMB != 30 {
		t.Fatalf("未出现的项应保持后台值 30, got %d", v.MaxUploadMB)
	}

	// 文件里的非法值必须让启动失败，而不是静默忽略。
	bad := config.MaxUploadMB + 1
	if _, err := second.Update(ctx, PatchFromFile(&config.FileSettings{MaxUploadMB: &bad})); err == nil {
		t.Fatal("超范围 max_upload_mb 应报错")
	}
}

// 外链白名单的规范化、校验与解析快照（§3.5）。
func TestHotlinkWhitelistUpdate(t *testing.T) {
	db, err := model.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	s, err := Load(ctx, db, 10)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 合法条目：逗号/换行混排、trim、丢空项、去重、域名小写化。
	v, err := s.Update(ctx, Patch{
		HotlinkWhitelist: strPtr("203.0.113.7\n 192.168.1.0/24 ,203.0.113.7"),
		HotlinkReferers:  strPtr("Example.COM\nimg.example.com,,"),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if v.HotlinkWhitelist != "203.0.113.7,192.168.1.0/24" {
		t.Fatalf("IP 表未规范化: %q", v.HotlinkWhitelist)
	}
	if v.HotlinkReferers != "example.com,img.example.com" {
		t.Fatalf("域名表未规范化: %q", v.HotlinkReferers)
	}

	rules := s.HotlinkRules()
	if len(rules.Nets) != 2 || len(rules.Domains) != 2 {
		t.Fatalf("解析快照不符: %+v", rules)
	}

	// 非法 IP 与超条目整体拒绝，另一张表不受影响。
	tooMany := make([]string, 0, maxWhitelistEntries+1)
	for i := 0; i <= maxWhitelistEntries; i++ {
		tooMany = append(tooMany, fmt.Sprintf("203.0.%d.%d", i/256, i%256))
	}
	for _, raw := range []string{
		"bad-ip",
		strings.Join(tooMany, ","),
	} {
		if _, err := s.Update(ctx, Patch{HotlinkWhitelist: strPtr(raw)}); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("IP 白名单 %q 应报 ErrInvalidSettings, got %v", raw, err)
		}
	}
	for _, raw := range []string{"https://example.com", "a..b", "example.com.", "例子.com"} {
		if _, err := s.Update(ctx, Patch{HotlinkReferers: strPtr(raw)}); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("引用域名 %q 应报 ErrInvalidSettings, got %v", raw, err)
		}
	}
	if got := s.Get().HotlinkReferers; got != "example.com,img.example.com" {
		t.Fatalf("非法更新不应改动已有域名表: %q", got)
	}

	// 公开配置视图不含任何白名单字段。
	b, err := json.Marshal(s.Get().Public())
	if err != nil {
		t.Fatalf("marshal public: %v", err)
	}
	if strings.Contains(string(b), "hotlink") {
		t.Fatalf("Public 不应包含白名单: %s", b)
	}
}

// 外链白名单应跨重启保留，并在重新加载时重建解析快照。
func TestHotlinkWhitelistPersist(t *testing.T) {
	db, err := model.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	first, err := Load(ctx, db, 10)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := first.Update(ctx, Patch{
		HotlinkWhitelist: strPtr("203.0.113.0/24"),
		HotlinkReferers:  strPtr("example.com"),
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	second, err := Load(ctx, db, 10)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	v := second.Get()
	if v.HotlinkWhitelist != "203.0.113.0/24" || v.HotlinkReferers != "example.com" {
		t.Fatalf("白名单应跨重启保留: %+v", v)
	}
	rules := second.HotlinkRules()
	if len(rules.Nets) != 1 || len(rules.Domains) != 1 {
		t.Fatalf("重启后解析快照不符: %+v", rules)
	}
}
