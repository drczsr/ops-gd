package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gongdan/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// mkMember 建可登录账号 + 指定标志的角色行。
func mkMember(t *testing.T, gdb *gorm.DB, m model.WorkOrderMember, password string) {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err := gdb.Create(&model.User{Username: m.UserID, Password: string(hash), DisplayName: m.UserID}).Error; err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := gdb.Create(&m).Error; err != nil {
		t.Fatalf("member: %v", err)
	}
}

// 菜单门禁:按标志放行/拦截,admin override 全通。
func TestMenuPermissionGating(t *testing.T) {
	r, db := setupGSServerWithDB(t)
	mkMember(t, db, model.WorkOrderMember{UserID: "mpOnlyUser", CanMergePreview: true}, "pass123")
	mkMember(t, db, model.WorkOrderMember{UserID: "srvOnlyUser", CanServers: true}, "pass123")
	mkMember(t, db, model.WorkOrderMember{UserID: "noPermUser", CanSubmit: true}, "pass123")

	// 每个用户登录一次,复用 cookie 检查多个页面。
	cookies := map[string]*http.Cookie{
		"mpOnlyUser":  loginCookie(t, r, "mpOnlyUser", "pass123"),
		"srvOnlyUser": loginCookie(t, r, "srvOnlyUser", "pass123"),
		"noPermUser":  loginCookie(t, r, "noPermUser", "pass123"),
		"admin":       loginCookie(t, r, "admin", "admin123"),
	}
	code := func(user, path string) int {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(cookies[user])
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	ok, deny := http.StatusOK, http.StatusFound // 无权限统一 302 重定向 /orders

	// planner:仅合服预告
	if code("mpOnlyUser", "/mergepreview") != ok {
		t.Error("planner 应能进合服预告")
	}
	for _, p := range []string{"/servers", "/gsconfig", "/members", "/settings"} {
		if code("mpOnlyUser", p) != deny {
			t.Errorf("planner 不应进 %s", p)
		}
	}
	// opsguy:仅服务器管理
	if code("srvOnlyUser", "/servers") != ok {
		t.Error("opsguy 应能进服务器管理")
	}
	if code("srvOnlyUser", "/mergepreview") != deny {
		t.Error("opsguy 不应进合服预告")
	}
	// nobody:啥配置/管理页都进不了
	for _, p := range []string{"/servers", "/gsconfig", "/mergepreview", "/members", "/settings"} {
		if code("noPermUser", p) != deny {
			t.Errorf("nobody 不应进 %s", p)
		}
	}
	// admin:全通(override)
	for _, p := range []string{"/servers", "/gsconfig", "/mergepreview", "/members", "/settings"} {
		if code("admin", p) != ok {
			t.Errorf("admin 应能进 %s", p)
		}
	}
}
