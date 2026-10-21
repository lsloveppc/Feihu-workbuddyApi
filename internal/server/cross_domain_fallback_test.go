// cross_domain_fallback_test.go 裸模型名跨域回退（issue #135）端到端测试：
// 开关默认关闭 = 零回归；开启后「首选域选不到号」与「首选域 11102」两条路径都要
// 回退到另一域；显式 cn:/global: 前缀永不回退。
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/feihu/feihu/internal/auth"
	"github.com/feihu/feihu/internal/pool"
)

// newRealmPool 构造 cn + global 各一个账号的池子。
// global 号按 domain 后缀归一化（www.workbuddy.ai → global）；cn 号积分更高，
// 配合 testPoolWith 的确定性随机源，断言「先试 cn、失败后才回退 global」。
func newRealmPool(t *testing.T) *pool.Pool {
	t.Helper()
	p := testPoolWith(
		&auth.Auth{UID: "cn1", AccessToken: "at-cn", ExpiresAt: 9999999999},
		&auth.Auth{UID: "gl1", AccessToken: "at-gl", Domain: "www.workbuddy.ai", ExpiresAt: 9999999999},
	)
	p.SetCredits("cn1", 2000, 0)
	p.SetCredits("gl1", 1000, 0)
	return p
}

// 11102「该后端无此模型」是 issue #135 的核心场景：模型只存在于 global 目录，
// 裸名请求先被送到 cn 号 → 11102；开启回退后应改用 global 号拿到 200。
func TestBareModelCrossDomainFallbackOnModelBlocked(t *testing.T) {
	calls := map[string]int{}
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		calls[authz]++
		if authz == "Bearer at-cn" {
			return 400, `{"code":11102,"msg":"model [deepseek-v4.1-flash-sg] service info not found"}`, false
		}
		return 200, sseOK, true
	})
	p := newRealmPool(t)
	p.SetCrossDomainFallback(true)
	h := NewHandler(Config{Pool: p, Upstream: up})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-v4.1-flash-sg","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s (want 200: 裸名在 cn 11102 后必须回退 global)", rec.Code, rec.Body)
	}
	if calls["Bearer at-cn"] != 1 || calls["Bearer at-gl"] != 1 {
		t.Errorf("calls=%v want cn 1 次（11102）→ global 1 次（回退成功）", calls)
	}
}

// 开关缺省关闭：行为与引入前完全一致——11102 后不跨域，回 400 model_unavailable。
func TestBareModelCrossDomainFallbackDisabledByDefault(t *testing.T) {
	calls := map[string]int{}
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		calls[authz]++
		if authz == "Bearer at-cn" {
			return 400, `{"code":11102,"msg":"model [deepseek-v4.1-flash-sg] service info not found"}`, false
		}
		return 200, sseOK, true
	})
	p := newRealmPool(t)
	if p.CrossDomainFallback() {
		t.Fatal("cross_domain_fallback 缺省必须为 false（零回归）")
	}
	h := NewHandler(Config{Pool: p, Upstream: up})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-v4.1-flash-sg","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "model_unavailable") {
		t.Fatalf("code=%d body=%s (want 400 model_unavailable)", rec.Code, rec.Body)
	}
	if calls["Bearer at-gl"] != 0 {
		t.Errorf("calls=%v want global 0 次（开关关闭不得跨域）", calls)
	}
}

// 显式 realm 前缀是确定性语义：开关开启也不回退（写 cn: 就只用 cn 号）。
func TestExplicitRealmNeverFallsBack(t *testing.T) {
	calls := map[string]int{}
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		calls[authz]++
		if authz == "Bearer at-cn" {
			return 400, `{"code":11102,"msg":"model [deepseek-v4.1-flash-sg] service info not found"}`, false
		}
		return 200, sseOK, true
	})
	p := newRealmPool(t)
	p.SetCrossDomainFallback(true)
	h := NewHandler(Config{Pool: p, Upstream: up})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"cn:deepseek-v4.1-flash-sg","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "model_unavailable") {
		t.Fatalf("code=%d body=%s (want 400: 显式 cn: 前缀不得回退)", rec.Code, rec.Body)
	}
	if calls["Bearer at-gl"] != 0 {
		t.Errorf("calls=%v want global 0 次（显式前缀零回退）", calls)
	}
}

// 第二条触发路径：首选域整域不可用（账号级冷却导致选号返回 nil），同样要回退。
func TestBareModelCrossDomainFallbackOnNoHealthyAccount(t *testing.T) {
	calls := map[string]int{}
	up := newFakeUpstream(t, func(authz string) (int, string, bool) {
		calls[authz]++
		return 200, sseOK, true
	})
	p := newRealmPool(t)
	p.SetCrossDomainFallback(true)
	p.Cooldown("cn1", pool.CoolSoft, time.Hour, "test: account-level cooldown")
	h := NewHandler(Config{Pool: p, Upstream: up})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s (want 200: cn 整域冷却时裸名应回退 global)", rec.Code, rec.Body)
	}
	if calls["Bearer at-cn"] != 0 || calls["Bearer at-gl"] != 1 {
		t.Errorf("calls=%v want cn 0 次、global 1 次", calls)
	}
}
