package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gongdan/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// mkUser 建一个可登录账号 + 角色行(canExecute 控制是否有「执行」角色)。
func mkUser(t *testing.T, gdb *gorm.DB, username, password string, canExecute bool) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if err := gdb.Create(&model.User{Username: username, Password: string(hash), DisplayName: username}).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := gdb.Create(&model.WorkOrderMember{UserID: username, DisplayName: username,
		CanSubmit: true, CanExecute: canExecute}).Error; err != nil {
		t.Fatalf("create member: %v", err)
	}
}

func TestServerDetailRenders(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/servers/1", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/servers/1 status=%d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "测试一服") {
		t.Error("详情页应含服名")
	}
	if !strings.Contains(body, "启动") || !strings.Contains(body, "停止") {
		t.Error("admin 有执行角色,详情页应有启动/停止按钮")
	}
	if !strings.Contains(body, "运行中") { // mock 模式探测恒为运行中
		t.Error("详情页应展示当前状态")
	}
	for _, sec := range []string{"基本信息", "网络信息", "拓扑关系", "全部字段", "数据库"} {
		if !strings.Contains(body, sec) {
			t.Errorf("详情页应含分组「%s」", sec)
		}
	}
}

func TestServerDetailNotFound(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/servers/99999", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在的服应 404, got %d", w.Code)
	}
}

func TestServerStartStopMockOK(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	for _, op := range []string{"start", "stop"} {
		req := httptest.NewRequest("POST", "/servers/1/"+op, nil)
		req.AddCookie(ck)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d, want 200 (body=%s)", op, w.Code, w.Body.String())
		}
		var resp struct {
			OK     bool   `json:"ok"`
			Output string `json:"output"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s json: %v", op, err)
		}
		if !resp.OK {
			t.Errorf("mock 模式 %s 应成功, resp=%+v", op, resp)
		}
		if !strings.Contains(resp.Output, "mock") {
			t.Errorf("%s 输出应含 mock 标记, got %q", op, resp.Output)
		}
	}
}

func TestServerTerminalPageAdminOnly(t *testing.T) {
	r, db := setupGSServerWithDB(t)
	// 管理员可进,页面应加载 xterm
	ck := loginCookie(t, r, "admin", "admin123")
	body := getBody(t, r, ck, "/servers/1/terminal")
	if !strings.Contains(body, "xterm") || !strings.Contains(body, "/terminal/ws") {
		t.Error("终端页应加载 xterm 并连接 ws")
	}
	// 非管理员(即便有执行角色)也不能用,403
	mkUser(t, db, "opsguy", "pass123", true)
	ck2 := loginCookie(t, r, "opsguy", "pass123")
	for _, path := range []string{"/servers/1/terminal", "/servers/1/terminal/ws"} {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(ck2)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("非管理员 %s 应 403, got %d", path, w.Code)
		}
	}
}

func TestServerStopForceUsesKill(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("POST", "/servers/1/stop?mode=force", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("force stop status=%d", w.Code)
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !resp.OK || !strings.Contains(resp.Output, "强制") {
		t.Errorf("mode=force 应走强制停止(mock 输出含「强制」), resp=%+v", resp)
	}
}

func TestServerStartBlockedAfterForceKill(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	post := func(path string) map[string]any {
		req := httptest.NewRequest("POST", path, nil)
		req.AddCookie(ck)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	// 强制停止 → 标记强杀
	if m := post("/servers/1/stop?mode=force"); m["ok"] != true {
		t.Fatalf("强制停止应成功, got %+v", m)
	}
	// 普通启动被拦截
	if m := post("/servers/1/start"); m["ok"] != false || m["needRepair"] != true {
		t.Fatalf("强杀后普通启动应被拦截(needRepair), got %+v", m)
	}
	// 修复启动成功并解除标记
	if m := post("/servers/1/start?mode=repair"); m["ok"] != true {
		t.Fatalf("修复启动应成功, got %+v", m)
	}
	// 解除后普通启动放行
	if m := post("/servers/1/start"); m["ok"] != true {
		t.Fatalf("解除强杀后普通启动应放行, got %+v", m)
	}
}

func TestServerBatchMockOK(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("POST", "/servers/batch",
		strings.NewReader("op=stop&ids=1,2,3,99999"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("batch status=%d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp struct {
		OK      bool `json:"ok"`
		Results []struct {
			ID    int    `json:"id"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !resp.OK || len(resp.Results) != 4 {
		t.Fatalf("应返回 4 条结果, resp=%+v", resp)
	}
	for _, x := range resp.Results {
		if x.ID == 99999 {
			if x.OK || x.Error == "" {
				t.Errorf("不存在的服应失败带原因, got %+v", x)
			}
		} else if !x.OK { // 1/2/3 在样例库中,mock 模式应成功
			t.Errorf("服 %d mock 停服应成功, got %+v", x.ID, x)
		}
	}
}

func TestServerBatchRequiresExecuteRole(t *testing.T) {
	r, db := setupGSServerWithDB(t)
	mkUser(t, db, "viewer2", "pass123", false)
	ck := loginCookie(t, r, "viewer2", "pass123")
	req := httptest.NewRequest("POST", "/servers/batch",
		strings.NewReader("op=start&ids=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("无执行角色批量操作应 403, got %d", w.Code)
	}
}

func TestServerActionRequiresExecuteRole(t *testing.T) {
	r, db := setupGSServerWithDB(t)
	// 建一个只有提交角色、无执行角色的账号
	mkUser(t, db, "viewer", "pass123", false)
	ck := loginCookie(t, r, "viewer", "pass123")
	req := httptest.NewRequest("POST", "/servers/1/start", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("无执行角色应 403, got %d (body=%s)", w.Code, w.Body.String())
	}
}
