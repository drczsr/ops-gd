package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"gongdan/internal/config"
	"gongdan/internal/db"
	"gongdan/internal/service/auth"
	"gongdan/internal/service/member"
	"gongdan/internal/service/order"
	"gongdan/internal/settings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 搭一个最小可用的真实路由(含 session 中间件),供登录+访问测试。
func setupTestServer(t *testing.T) (*gin.Engine, *settings.Store, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	gdb, err := db.Init("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("db init: %v", err)
	}
	cfg := &config.Config{}
	cfg.Auth.PasswordEnabled = true
	st, _ := settings.NewStore(settings.Settings{Mode: "mock"}, "")
	h := New(cfg, gdb, order.New(gdb, st, nil), member.New(gdb), st, nil, nil, nil)
	r := gin.New()
	h.Register(r)
	r.LoadHTMLGlob("../../templates/*.html")
	return r, st, gdb
}

// 用账号密码登录,返回携带的 session cookie。
func loginCookie(t *testing.T, r *gin.Engine, username, password string) *http.Cookie {
	t.Helper()
	form := url.Values{"username": {username}, "password": {password}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	for _, ck := range w.Result().Cookies() {
		if ck.Name == sessionName {
			return ck
		}
	}
	t.Fatalf("登录未返回 session cookie(status=%d)", w.Code)
	return nil
}

func TestAdminCanSaveSettings(t *testing.T) {
	r, st, _ := setupTestServer(t)
	ck := loginCookie(t, r, "admin", "admin123")

	form := url.Values{"mode": {"real"}, "auto_execute": {"on"}}
	req := httptest.NewRequest("POST", "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := st.Get(); got.Mode != "real" || !got.AutoExecute || got.AutoOpen {
		t.Errorf("store 未按表单更新: %+v", got)
	}
}

func TestNonAdminBlockedFromSettings(t *testing.T) {
	r, _, gdb := setupTestServer(t)
	if err := auth.UpsertAccount(gdb, "bob", "pw123", "Bob"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	ck := loginCookie(t, r, "bob", "pw123")

	req := httptest.NewRequest("GET", "/settings", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("非 admin 访问 /settings status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/orders" {
		t.Errorf("重定向到 %q, want /orders", loc)
	}
}
