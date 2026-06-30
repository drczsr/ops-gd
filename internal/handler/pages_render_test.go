package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"gongdan/internal/model"
	"gongdan/internal/service/workflow"
)

// 锁住登录页/成员页能正常渲染(模板字段、函数对得上),
// 美化改版后这两页此前没有 GET 渲染覆盖。
func TestLoginPageRenders(t *testing.T) {
	r, _, _ := setupGSServer(t)
	req := httptest.NewRequest("GET", "/login", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/login status=%d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "运维工单系统") {
		t.Errorf("登录页应含系统名")
	}
	if !strings.Contains(body, `name="username"`) || !strings.Contains(body, `name="password"`) {
		t.Errorf("登录页应含用户名/密码输入框")
	}
}

func TestLoginPageDemoAutofill(t *testing.T) {
	t.Setenv("DEMO_AUTO_FILL_LOGIN", "true")
	t.Setenv("DEMO_LOGIN_USERNAME", "admin")
	t.Setenv("DEMO_LOGIN_PASSWORD", "admin123")

	r, _, _ := setupGSServer(t)
	req := httptest.NewRequest("GET", "/login", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/login status=%d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `name="username" placeholder="用户名" value="admin"`) {
		t.Fatalf("demo 模式应预填用户名, body=%s", body)
	}
	if !strings.Contains(body, `name="password" placeholder="请输入密码" value="admin123"`) {
		t.Fatalf("demo 模式应预填密码, body=%s", body)
	}
}

func TestMemberPageRenders(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/members", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/members status=%d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "成员角色管理") {
		t.Errorf("成员页应含标题")
	}
	if !strings.Contains(body, `name="user_id"`) {
		t.Errorf("成员页应含新增表单")
	}
}

func TestOrderDetailPlacesLogsAfterParams(t *testing.T) {
	r, _, _ := setupGSServer(t)
	gdb := mustOrderDB(t)
	wo := &model.WorkOrder{
		OrderNo:  "WO-DETAIL-1",
		Type:     model.TypeRelease,
		Title:    "测试详情布局",
		Status:   workflow.StatusOpened,
		CreateBy: "admin",
		Params:   `{"server_ids":[10001],"version_package":"pkg.zip","include_battle":false,"include_hot_update":false}`,
	}
	if err := gdb.Create(wo).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/orders/"+strconv.FormatUint(uint64(wo.ID), 10), nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/orders/%d status=%d body=%s", wo.ID, w.Code, w.Body.String())
	}
	body := w.Body.String()
	paramsAt := strings.Index(body, "参数详情")
	logsAt := strings.Index(body, "执行日志")
	if paramsAt < 0 || logsAt < 0 {
		t.Fatalf("详情页应同时包含参数详情和执行日志, body=%s", body)
	}
	if logsAt < paramsAt {
		t.Fatalf("执行日志应位于参数详情之后: params=%d logs=%d", paramsAt, logsAt)
	}
	if !strings.Contains(body, "wo-log-section") {
		t.Fatalf("详情页应使用下方日志布局容器 wo-log-section, body=%s", body)
	}
	if !strings.Contains(body, "wo-keyfacts") {
		t.Fatalf("详情页应使用重点信息摘要区 wo-keyfacts, body=%s", body)
	}
}
