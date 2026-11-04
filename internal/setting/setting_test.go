package setting

import (
	"context"
	"testing"

	"github.com/pixsy/pixsy/internal/config"
	"github.com/pixsy/pixsy/internal/model"
)

func intPtr(n int) *int { return &n }

func TestPatchFromFile(t *testing.T) {
	name, mb := "我的图床", 50
	p := PatchFromFile(&config.FileSettings{SiteName: &name, MaxUploadMB: &mb})
	if p.SiteName == nil || *p.SiteName != name {
		t.Fatalf("SiteName 未映射: %+v", p)
	}
	if p.MaxUploadMB == nil || *p.MaxUploadMB != mb {
		t.Fatalf("MaxUploadMB 未映射: %+v", p)
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
