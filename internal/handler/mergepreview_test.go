package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"gongdan/internal/config"
	"gongdan/internal/db"
	"gongdan/internal/mergepreview"
	"gongdan/internal/model"
	"gongdan/internal/service/auth"
	"gongdan/internal/service/member"
	"gongdan/internal/service/order"
	"gongdan/internal/settings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// validMergeTSV 合服预告全量样本(与 mergepreview 包内 importTSV 同格式),供建单测试播种。
const validMergeTSV = "Id\tName\tPreviewOpenTime\tPreviewDuration\tCompensationOpenTime\tCompensationDurationTime\tIsCompensationItem\tIsGrandGift\tMiaoShu\n" +
	"INT\tSTRING\tINT\tINT\tINT\tINT\tBOOL\tINT\tINT\n" +
	"MAX_ID=59999;MAX_RECORD=60000;\t\t\tserver\t\t\t\t\t\n" +
	"#\t\t\t\t\t\t\t\t\n" +
	"10054\t合服目标服\t10\t3\t13\t2\t0\t1\t945018\n" +
	"10060\t合服原服\t10\t3\t13\t1\t1\t1\t945018\n"

func setupMPServer(t *testing.T) (*gin.Engine, *mergepreview.Service, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	gdb, err := db.Init("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("db init: %v", err)
	}
	cfg := &config.Config{}
	cfg.Auth.PasswordEnabled = true
	st, _ := settings.NewStore(settings.Settings{Mode: "mock"}, "")
	store := mergepreview.NewStore(gdb)
	if err := store.EnsureSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}
	gdb.Exec("DELETE FROM merge_func_rows")
	gdb.Exec("DELETE FROM merge_func_meta")
	mp := mergepreview.NewService(store)
	h := New(cfg, gdb, order.New(gdb, st, nil), member.New(gdb), st, nil, mp, &fakeUploader{})
	r := gin.New()
	h.Register(r)
	r.LoadHTMLGlob("../../templates/*.html")
	return r, mp, gdb
}

func TestMPBlocksNonAdmin(t *testing.T) {
	r, mp, gdb := setupMPServer(t)
	if err := auth.UpsertAccount(gdb, "bob", "pw123", "Bob"); err != nil {
		t.Fatalf("user: %v", err)
	}
	ck := loginCookie(t, r, "bob", "pw123")
	form := url.Values{"row_id": {"10001"}, "row_name": {"x"}, "row_compdur": {"1"}, "row_compitem": {"0"}}
	req := httptest.NewRequest(http.MethodPost, "/mergepreview/rows", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/orders" {
		t.Fatalf("非admin应被拦重定向/orders, got %d %s", w.Code, w.Header().Get("Location"))
	}
	if n, _ := mp.Count(""); n != 0 {
		t.Errorf("非admin不应写入, count=%d", n)
	}
}

func TestMPAddRowsAsAdmin(t *testing.T) {
	r, mp, _ := setupMPServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	form := url.Values{
		"row_id":         {"10001", "10002"},
		"row_name":       {"大理1服", "大理2服"},
		"row_previewdur": {"3", "3"},
		"row_compopen":   {"13", "13"},
		"row_compdur":    {"2", "1"},
		"row_compitem":   {"0", "1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/mergepreview/rows", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("追加应回渲染页200, got %d", w.Code)
	}
	if n, _ := mp.Count(""); n != 2 {
		t.Errorf("应入库2行, got %d", n)
	}
}

func TestMPPublishCreatesOrder(t *testing.T) {
	r, mp, gdb := setupMPServer(t)
	// 播种表头+行,使 GenerateBytes 成功(cos 桩 Download/Head 返回 ErrNotFound→空基准→必建单)
	if err := mp.ImportOverwrite(validMergeTSV); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest(http.MethodPost, "/mergepreview/publish", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/orders/") {
		t.Fatalf("code=%d loc=%s body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	var n int64
	gdb.Model(&model.WorkOrder{}).Where("type = ?", model.TypeMergePublish).Count(&n)
	if n != 1 {
		t.Fatalf("mergepublish 工单数 = %d, want 1", n)
	}
}
