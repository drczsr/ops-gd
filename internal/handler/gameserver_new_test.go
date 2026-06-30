package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestNewGamePageRoutedNotSwallowed 确认 /gsconfig/newgame 走的是新增游戏服表单页,
// 而不是被 /gsconfig/:id 通配吃成「编辑服 newgame」。这是路由顺序的关键守卫。
func TestNewGamePageRoutedNotSwallowed(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/gsconfig/newgame", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/gsconfig/newgame status=%d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/gsconfig/newgame/preview") {
		t.Errorf("应渲染新增游戏服表单(含 preview 提交地址),实际未命中")
	}
	if strings.Contains(body, "未找到该服") {
		t.Errorf("被 /gsconfig/:id 误吃了:渲染成了编辑/未找到页")
	}
}

// TestNewGamePageRendersFullForm 守卫:表单页须完整渲染到底,不能在「外网地址」处因
// .Form 为 nil 触发 index 报错而截断(此前 newGameView 未给 Form 兜底,页面只渲染到外网地址标签)。
func TestNewGamePageRendersFullForm(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/gsconfig/newgame", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	body := w.Body.String()
	// 外网地址单选 + 其后的版本包/预览按钮都须在,确保未截断
	for _, want := range []string{"添加外网域名(wss)", "用外网IP", "版本包", ">预览<"} {
		if !strings.Contains(body, want) {
			t.Errorf("表单页缺少 %q(疑似在外网地址处截断)", want)
		}
	}
}

// TestNewGamePreviewInvalidCount 数量非 4 的倍数时,预览应回表单页并显示校验错误(不落库)。
func TestNewGamePreviewInvalidCount(t *testing.T) {
	r, svc, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	form := url.Values{
		"mode": {"new"}, "region_new": {"测试区"}, "count": {"3"},
		"game_mysqlip": {"g"}, "battle_mysqlip": {"b"},
	}
	req := httptest.NewRequest("POST", "/gsconfig/newgame/preview", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "4 的倍数") {
		t.Errorf("应显示「4 的倍数」校验错误,body=%s", w.Body.String())
	}
	// 不应有任何写入:样本仍是 3 行
	rows, _ := svc.Store().AllRows()
	if len(rows) != 3 {
		t.Errorf("预览不应落库,现有 %d 行", len(rows))
	}
}
