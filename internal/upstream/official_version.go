// official_version.go 出站版本段的来源解析。
//
// 版本段用于组装 User-Agent 与桌面端事件上报的版本字段，取值优先级：
//   1. 配置项 upstream.client_version（显式指定）；
//   2. 本机已安装客户端的启动记录 last-launch.json（跟随实际安装版本）；
//   3. 内置默认版本。
//
// 统一来源的好处是同一实例各端点上报的版本始终一致，客户端升级后无需改代码。
package upstream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// officialLaunchFile 官方 WorkBuddy 桌面客户端写的「最近一次启动」记录。
	// 实测结构（5.8.3）：
	//   {"version":"5.8.3","build":"db0e9b51b7fa0b383f41f5b0b83dd442c8557918",
	//    "timestamp":"2026-10-20T15:19:38.846Z"}
	officialLaunchFile = "last-launch.json"
	// officialCacheTTL 探测结果缓存时长：官方客户端升级后最多滞后这么久才生效。
	officialCacheTTL = 5 * time.Minute
)

// officialVersionCache 官方客户端版本探测缓存（含失败结果，避免每请求扫盘）。
type officialVersionCache struct {
	Version string
	Build   string
	Source  string
	OK      bool
	At      time.Time
}

// officialProbePaths 返回 last-launch.json 的候选路径（按优先级）。
//
// 显式指定（extra 非空）时**只认它**，不回落到自动探测：静默改用别处读到的版本
// 会掩盖「路径配错了」这件事。宁可探测失败回落内置默认并在启动日志告警。
//
// 自动探测覆盖三种部署形态：
//   - 本机直跑：用户目录下的 .workbuddy / AppData；
//   - 容器内挂载宿主目录：容器 HOME 可能不是宿主 HOME，故额外用 APPDATA /
//     LOCALAPPDATA 环境变量兜底；
//   - 非常规安装：运维用 Client.OfficialLaunchFile 显式指定。
func officialProbePaths(extra string) []string {
	if extra != "" {
		return []string{extra}
	}
	var out []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out,
			filepath.Join(home, ".workbuddy", officialLaunchFile),
			filepath.Join(home, "AppData", "Roaming", "WorkBuddy", officialLaunchFile),
			filepath.Join(home, "AppData", "Local", "WorkBuddy", officialLaunchFile),
		)
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		out = append(out, filepath.Join(appData, "WorkBuddy", officialLaunchFile))
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		out = append(out, filepath.Join(localAppData, "WorkBuddy", officialLaunchFile))
	}
	return out
}

// readOfficialLaunch 读一个 last-launch.json，返回 (version, build, ok)。
// 文件缺失、JSON 畸形、version 为空一律 ok=false（不编造版本）。
func readOfficialLaunch(path string) (ver, build string, ok bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	var rec struct {
		Version string `json:"version"`
		Build   string `json:"build"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", "", false
	}
	ver = strings.TrimSpace(rec.Version)
	if ver == "" {
		return "", "", false
	}
	return ver, strings.TrimSpace(rec.Build), true
}

// OfficialVersion 返回本机官方客户端版本（带 TTL 缓存）。
//
// 并发模型：读路径无锁（atomic.Pointer），只有探测本身持锁并做双检 —— 因为
// clientVersion() 在每个出站请求都会调用，读路径抢锁会成为全局热点。
//
// 探测失败返回 ok=false —— 调用方回落内置默认，不阻塞任何请求路径。
// source 为命中的文件路径，供面板/日志对账（「网关拟真是从哪个文件学的」）。
func (c *Client) OfficialVersion() (ver, build, source string, ok bool) {
	if c == nil {
		return "", "", "", false
	}
	if got := c.officialVer.Load(); got != nil && time.Since(got.At) < officialCacheTTL {
		return got.Version, got.Build, got.Source, got.OK
	}

	c.officialVerMu.Lock()
	defer c.officialVerMu.Unlock()
	// 双检：等锁期间可能已有别的 goroutine 完成探测（避免一窝蜂重复读盘）。
	if got := c.officialVer.Load(); got != nil && time.Since(got.At) < officialCacheTTL {
		return got.Version, got.Build, got.Source, got.OK
	}

	for _, p := range officialProbePaths(c.OfficialLaunchFile) {
		if v, b, found := readOfficialLaunch(p); found {
			c.officialVer.Store(&officialVersionCache{
				Version: v, Build: b, Source: p, OK: true, At: time.Now(),
			})
			return v, b, p, true
		}
	}
	c.officialVer.Store(&officialVersionCache{At: time.Now()})
	return "", "", "", false
}

// effectiveClientVersion 生效的客户端版本段（UA 的 `WorkBuddy/<ver>` 与
// X-IDE-Version 共用）。优先级：
//
//  1. Client.ClientVersion —— 显式配置，需要固定版本时使用（改配置即可，不必改代码）；
//  2. FollowOfficialVersion 开启 + 探测到官方客户端 → 用官方版本（自洽于真实设备）；
//  3. 内置 defaultClientVersion —— 兜底，保证任何环境都有确定值。
func (c *Client) effectiveClientVersion() string {
	if c == nil {
		return defaultClientVersion
	}
	if c.ClientVersion != "" {
		return c.ClientVersion
	}
	if c.FollowOfficialVersion {
		if v, _, _, ok := c.OfficialVersion(); ok {
			return v
		}
	}
	return defaultClientVersion
}

// effectiveDesktopVersion 桌面端事件上报的版本段。
//
// 默认跟随 effectiveClientVersion，使同一实例各端点上报的版本一致。
// 保留 DesktopVersion 显式覆盖，供特定场景固定版本值。
func (c *Client) effectiveDesktopVersion() string {
	if c != nil && c.DesktopVersion != "" {
		return c.DesktopVersion
	}
	return c.effectiveClientVersion()
}

// desktopUAFor 桌面端出站 UA（三段式全部用同一版本段，保证自洽）。
// 实测官方形态为 `WorkBuddy/<ver> WorkBuddy/<ver> CLI/<cliVer>`。
func (c *Client) desktopUAFor() string {
	v := c.effectiveDesktopVersion()
	return "WorkBuddy/" + v + " WorkBuddy/" + v + " CLI/" + c.cliVersion()
}
