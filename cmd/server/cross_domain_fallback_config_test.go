// cross_domain_fallback_config_test.go pool.cross_domain_fallback 配置测试：
// 默认 false（零回归）/ 文件覆盖为 true。
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCrossDomainFallbackDefault 键缺席 → 默认 false（裸名仍是 cn 单域，行为与引入前一致）。
func TestCrossDomainFallbackDefault(t *testing.T) {
	c := Default()
	if err := c.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if c.Pool.CrossDomainFallback {
		t.Error("cross_domain_fallback 缺省必须为 false")
	}
}

// TestCrossDomainFallbackParsedFromFile 显式配置覆盖默认。
func TestCrossDomainFallbackParsedFromFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "c.json")
	if err := os.WriteFile(fp, []byte(`{"pool":{"cross_domain_fallback":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(fp)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Pool.CrossDomainFallback {
		t.Error("cross_domain_fallback=true 未生效")
	}
}
