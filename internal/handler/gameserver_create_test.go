package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGSCreatePageRenders(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/gsconfig/create", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/gsconfig/create status=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "创建游戏服") {
		t.Errorf("页面应含标题")
	}
}

func TestGSCreatePreviewMissingSlotReturnsError(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	// 只填名,缺落点 -> PlanCreateGameServer 报错 -> 回表单带错(200),不应 500
	form := url.Values{"server_name": {"测试1服"}, "world_name": {"T1"}}
	req := httptest.NewRequest("POST", "/gsconfig/create/preview", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusInternalServerError {
		t.Fatalf("preview 不应 500: %s", w.Body.String())
	}
}

func TestGSCreatePreviewNewCenter(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	form := url.Values{
		"server_name":    {"测试2服"},
		"world_name":     {"T2"},
		"slot":           {"10.0.0.1|1.1.1.1|3400"},
		"game_mysqlip":   {"game-rds"},
		"battle_enabled": {"1"},
		"battle_mode":    {"new"},
		"battle_machine": {"10.1.0.2|9.9.9.2"},
		"battle_mysqlip": {"battle-rds"},
		"center_enabled": {"1"},
		"center_mode":    {"new"},
		"center_machine": {"192.168.11.161|8.8.8.2"},
		"center_mysqlip": {"center-rds"},
	}
	req := httptest.NewRequest("POST", "/gsconfig/create/preview", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusInternalServerError {
		t.Fatalf("新建中心服预览不应 500: %s", w.Body.String())
	}
}

// 确认方案页应渲染「查看完整字段」折叠块,且端口段(list+index)正常展开。
// 用纯游戏服(不挂战斗/中心,样例库无 WT2/3/4 模板),确保走到 preview 分支。
func TestGSCreatePreviewShowsFullFields(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	form := url.Values{
		"server_name":  {"完整字段测试服"},
		"world_name":   {"FF1"},
		"slot":         {"10.0.0.1|1.1.1.1|3400"},
		"game_mysqlip": {"game-rds"},
	}
	req := httptest.NewRequest("POST", "/gsconfig/create/preview", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "确认方案") {
		t.Fatalf("应渲染确认方案页, got: %s", body)
	}
	for _, want := range []string{"查看完整字段", "PortForClient", "PortForGlobalCenter"} {
		if !strings.Contains(body, want) {
			t.Errorf("确认页缺少完整字段块内容 %q", want)
		}
	}
}

func TestGSCreateManualNewserverOrderRejected(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	form := url.Values{"type": {"newserver"}, "title": {"x"}}
	req := httptest.NewRequest("POST", "/orders", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("手动建 newserver 应 400, got %d", w.Code)
	}
}
