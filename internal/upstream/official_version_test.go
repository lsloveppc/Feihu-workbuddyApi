package upstream

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEffectiveClientVersionPrecedence 钉住版本段的优先级链：
// 显式配置 > 跟随本机官方客户端 > 内置默认。
//
// 这条链决定出站版本段的来源：显式配置优先，其次是本机安装的客户端版本，
// 最后回落内置默认值。
func TestEffectiveClientVersionPrecedence(t *testing.T) {
	dir := t.TempDir()
	launch := filepath.Join(dir, "last-launch.json")
	if err := os.WriteFile(launch, []byte(`{"version":"5.8.3","build":"db0e9b51"}`), 0o644); err != nil {
		t.Fatalf("write launch: %v", err)
	}

	// 1) 显式钉死：即使 follow 打开、文件里有新版本，也以配置为准。
	c := &Client{ClientVersion: "9.9.9", FollowOfficialVersion: true, OfficialLaunchFile: launch}
	if got := c.effectiveClientVersion(); got != "9.9.9" {
		t.Errorf("显式钉死失效：got=%q want 9.9.9", got)
	}

	// 2) follow 打开 + 探测到官方客户端 → 用文件里的真实版本。
	c2 := &Client{FollowOfficialVersion: true, OfficialLaunchFile: launch}
	if got := c2.effectiveClientVersion(); got != "5.8.3" {
		t.Errorf("跟随官方客户端失效：got=%q want 5.8.3", got)
	}

	// 3) follow 关闭 → 内置默认（旧行为，运维可显式回落）。
	c3 := &Client{OfficialLaunchFile: launch}
	if got := c3.effectiveClientVersion(); got != defaultClientVersion {
		t.Errorf("follow 关闭时应回落默认：got=%q want %q", got, defaultClientVersion)
	}

	// 4) follow 打开但文件不存在 → 回落到默认，不产生空版本号。
	c4 := &Client{FollowOfficialVersion: true, OfficialLaunchFile: filepath.Join(dir, "missing.json")}
	if got := c4.effectiveClientVersion(); got != defaultClientVersion {
		t.Errorf("探测失败应回落默认：got=%q want %q", got, defaultClientVersion)
	}
}

// TestOfficialVersionProbeRejectsMalformed 畸形/空版本号一律视为探测失败，
// 不允许把垃圾值当成版本写进出站 UA。
func TestOfficialVersionProbeRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"空 version":   `{"version":""}`,
		"缺 version":   `{"build":"abc"}`,
		"畸形 JSON":    `{"version":`,
		"version 空白": `{"version":"   "}`,
	}
	for name, body := range cases {
		dir := t.TempDir()
		p := filepath.Join(dir, "last-launch.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("%s: write: %v", name, err)
		}
		c := &Client{FollowOfficialVersion: true, OfficialLaunchFile: p}
		if v, _, _, ok := c.OfficialVersion(); ok {
			t.Errorf("%s: 应判探测失败，实际 ver=%q ok=true", name, v)
		}
	}
}

// TestDesktopVersionSelfConsistency 桌面端事件版本默认跟随 UA 版本段，
// 保证同一实例各端点上报的版本一致。
func TestDesktopVersionSelfConsistency(t *testing.T) {
	c := &Client{ClientVersion: "5.8.3", CliVersion: "2.137.1"}

	if got := c.effectiveDesktopVersion(); got != "5.8.3" {
		t.Errorf("桌面版本应跟随 ClientVersion：got=%q want 5.8.3", got)
	}
	want := "WorkBuddy/5.8.3 WorkBuddy/5.8.3 CLI/2.137.1"
	if got := c.desktopUAFor(); got != want {
		t.Errorf("desktop UA=%q want %q", got, want)
	}

	// 显式钉死桌面版本（桌面任务点亮依赖实测版本时用）。
	c2 := &Client{ClientVersion: "5.8.3", DesktopVersion: "5.5.6"}
	if got := c2.effectiveDesktopVersion(); got != "5.5.6" {
		t.Errorf("DesktopVersion 显式覆盖失效：got=%q want 5.5.6", got)
	}
	if got := c2.desktopUAFor(); got != "WorkBuddy/5.5.6 WorkBuddy/5.5.6 CLI/2.137.1" {
		t.Errorf("覆盖后 UA 仍应自洽：got=%q", got)
	}
}

// TestOfficialVersionCachedTTL 探测结果带 TTL 缓存：同一实例重复调用不重复读盘。
// 用一个在首次调用后被删除的文件验证——第二次仍能返回缓存值。
func TestOfficialVersionCachedTTL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "last-launch.json")
	if err := os.WriteFile(p, []byte(`{"version":"5.8.3"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := &Client{FollowOfficialVersion: true, OfficialLaunchFile: p}
	if v, _, _, ok := c.OfficialVersion(); !ok || v != "5.8.3" {
		t.Fatalf("首次探测失败: v=%q ok=%v", v, ok)
	}
	if err := os.Remove(p); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if v, _, _, ok := c.OfficialVersion(); !ok || v != "5.8.3" {
		t.Errorf("TTL 内应命中缓存：v=%q ok=%v", v, ok)
	}
}
