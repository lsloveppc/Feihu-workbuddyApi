package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nestedIdentityDoc = `{
  "auth": {"accessToken": "at", "refreshToken": "rt", "expiresAt": 1999999999,
           "domain": "copilot.tencent.com", "realm": "cn"},
  "account": {"uid": "u-identity", "nickname": "号"},
  "device_token": "dt-1",
  "qimei36": "de98bc5f1653cde0854ba1c030001851a717",
  "machine_id": "b6a3f986-07d8-4f21-9123-2359df157687"
}`

// TestParseDeviceIdentityNested 嵌套形（插件 OAuth 输出）解析真机标识。
//
// 为什么要有这一条：改造前 auth 文件只认 accessToken/uid/device_token，真机标识
// 无处可放，网关只能用 uid 哈希伪造 machineId/qimei36 —— 一个真机登录过的账号
// 报出另一台「虚拟设备」，是上游通道校验最容易抓的形态。
func TestParseDeviceIdentityNested(t *testing.T) {
	a, err := Parse([]byte(nestedIdentityDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if a.QIMEI36 != "de98bc5f1653cde0854ba1c030001851a717" {
		t.Errorf("QIMEI36=%q", a.QIMEI36)
	}
	if a.MachineID != "b6a3f986-07d8-4f21-9123-2359df157687" {
		t.Errorf("MachineID=%q", a.MachineID)
	}
	if a.DeviceToken != "dt-1" {
		t.Errorf("DeviceToken=%q", a.DeviceToken)
	}
}

// TestParseDeviceIdentityFlat 扁平形（手写 auth 文件）同样解析真机标识。
func TestParseDeviceIdentityFlat(t *testing.T) {
	doc := `{"accessToken":"at","uid":"u-flat","qimei36":"q36-flat","machine_id":"mid-flat"}`
	a, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if a.QIMEI36 != "q36-flat" || a.MachineID != "mid-flat" {
		t.Errorf("扁平形解析失败：qimei36=%q machine_id=%q", a.QIMEI36, a.MachineID)
	}
}

// TestSaveAtomicRoundTripsDeviceIdentity token 刷新后的原子写回不能丢真机标识。
//
// 这是最容易回归的一条：refresh 路径会用 Parse 出的 *Auth 重新 SaveAtomic，
// 若写回时漏掉 qimei36/machine_id，它们的真值会在第一次 refresh 后静默消失，
// 网关随即退回伪造派生的设备标识——而这次退化不会有任何报错。
func TestSaveAtomicRoundTripsDeviceIdentity(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "workbuddy-u-identity.json")
	if err := os.WriteFile(p, []byte(nestedIdentityDoc), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a, err := Parse([]byte(nestedIdentityDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	a.FilePath = p
	if err := a.SaveAtomic(); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	b, err := Parse(raw)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if b.QIMEI36 != a.QIMEI36 {
		t.Errorf("qimei36 写回丢失：got=%q want=%q", b.QIMEI36, a.QIMEI36)
	}
	if b.MachineID != a.MachineID {
		t.Errorf("machine_id 写回丢失：got=%q want=%q", b.MachineID, a.MachineID)
	}
	if b.AccessToken != "at" {
		t.Errorf("accessToken 应保持：got=%q", b.AccessToken)
	}
}

// TestSaveAtomicOmitsEmptyDeviceIdentity 没有真机标识时不引入空键。
// 保持旧 auth 文件的既有形状（插件读取忽略未知键，但空值会让运维误以为已配置）。
func TestSaveAtomicOmitsEmptyDeviceIdentity(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "workbuddy-u-plain.json")
	a := &Auth{AccessToken: "at", UID: "u-plain", FilePath: p}
	if err := a.SaveAtomic(); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, `"qimei36"`) {
		t.Errorf("空 qimei36 不应写回：%s", text)
	}
	if strings.Contains(text, `"machine_id"`) {
		t.Errorf("空 machine_id 不应写回：%s", text)
	}
}
