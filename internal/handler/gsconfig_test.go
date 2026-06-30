package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"gongdan/internal/config"
	"gongdan/internal/db"
	"gongdan/internal/gsconfig"
	"gongdan/internal/gsconfig/cos"
	"gongdan/internal/mergepreview"
	"gongdan/internal/model"
	"gongdan/internal/service/auth"
	"gongdan/internal/service/member"
	"gongdan/internal/service/order"
	"gongdan/internal/settings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type fakeUploader struct{ hits int }

func (f *fakeUploader) Upload(key string, data []byte) (string, error) { f.hits++; return "", nil }
func (f *fakeUploader) Download(key string, versionID ...string) ([]byte, error) {
	return nil, cos.ErrNotFound
}
func (f *fakeUploader) HeadVersionID(key string) (string, error) {
	return "", cos.ErrNotFound
}

func setupGSServer(t *testing.T) (*gin.Engine, *gsconfig.Service, *fakeUploader) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	gdb, err := db.Init("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("db init: %v", err)
	}
	cfg := &config.Config{}
	cfg.Auth.PasswordEnabled = true
	// 测试样例服 Id 为 1~3,默认打开「显示全部服务器」以免被可见性规则滤掉
	st, _ := settings.NewStore(settings.Settings{Mode: "mock", ShowAllServers: true}, "")

	// 配置库直接复用同一个测试 sqlite(配置表与工单表同库,测试无所谓)
	gdb.Exec("DROP TABLE IF EXISTS config_server")
	gdb.Exec("DROP TABLE IF EXISTS config_columns")
	gdb.Exec("DROP TABLE IF EXISTS config_metas")
	store := gsconfig.NewStore(gdb)
	raw, err := os.ReadFile("../../internal/gsconfig/testdata/sample.txt")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	cols, meta, rows, err := gsconfig.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := store.EnsureSchema(cols); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := store.ImportAll(cols, meta, rows); err != nil {
		t.Fatalf("import: %v", err)
	}
	up := &fakeUploader{}
	gsSvc := gsconfig.NewService(store, up, "server/ServerConfigList.txt", "client/ServerConfigList.txt", "", "")

	mpStore := mergepreview.NewStore(gdb)
	if err := mpStore.EnsureSchema(); err != nil {
		t.Fatalf("mp schema: %v", err)
	}
	mp := mergepreview.NewService(mpStore)
	h := New(cfg, gdb, order.New(gdb, st, nil), member.New(gdb), st, gsSvc, mp, up)
	r := gin.New()
	h.Register(r)
	r.LoadHTMLGlob("../../templates/*.html")
	return r, gsSvc, up
}

func TestGSNeedsALB(t *testing.T) {
	if !gsNeedsALB(map[string]string{"RealSelfPublicUrl": "wss://ws-tw.example/s10001"}) {
		t.Fatal("expected true for wss public url")
	}
	if gsNeedsALB(map[string]string{"RealSelfPublicUrl": "1.1.1.1"}) {
		t.Fatal("expected false for plain ip public url")
	}
}

func TestGSConfigRequiresLogin(t *testing.T) {
	r, _, _ := setupGSServer(t)
	req := httptest.NewRequest("GET", "/gsconfig", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("未登录 status=%d, want 302", w.Code)
	}
}

func TestGSConfigAdminGenerateUploads(t *testing.T) {
	r, _, up := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("POST", "/gsconfig/_generate", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound && w.Code != http.StatusOK {
		t.Fatalf("generate status=%d", w.Code)
	}
	if up.hits != 2 {
		t.Errorf("应上传两次(server+client), hits=%d", up.hits)
	}
}

func TestGSConfigAddServerValidatesDupID(t *testing.T) {
	r, svc, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	form := url.Values{"Id": {"1"}, "WorldName": {"x"}}
	req := httptest.NewRequest("POST", "/gsconfig/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	rows, _ := svc.Store().AllRows()
	if len(rows) != 3 {
		t.Errorf("重复 Id 不应新增, 现有 %d 行", len(rows))
	}
}

func TestGSConfigEditServer(t *testing.T) {
	r, svc, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	form := url.Values{"WorldName": {"改过的名"}}
	req := httptest.NewRequest("POST", "/gsconfig/2", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	got, _ := svc.Store().GetServer("2")
	if got["WorldName"] != "改过的名" {
		t.Errorf("编辑未生效: %+v", got)
	}
}

func TestGSImportThenExport(t *testing.T) {
	r, svc, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")

	sample, err := os.ReadFile("../../internal/gsconfig/testdata/sample.txt")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	// 上传覆盖导入
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "ServerConfigList.txt")
	fw.Write(sample)
	w.Close()
	req := httptest.NewRequest("POST", "/gsconfig/import", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(ck)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("import code=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, _ := svc.Store().AllRows()
	if len(rows) == 0 {
		t.Fatalf("导入后配置库应有数据")
	}

	// 导出下载
	req2 := httptest.NewRequest("GET", "/gsconfig/export", nil)
	req2.AddCookie(ck)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("export code=%d", rec2.Code)
	}
	if cd := rec2.Header().Get("Content-Disposition"); !strings.Contains(cd, "ServerConfigList.txt") {
		t.Errorf("应有下载文件名头, got %q", cd)
	}
	if rec2.Body.Len() == 0 {
		t.Errorf("导出内容不应为空")
	}
	if utf8.Valid(rec2.Body.Bytes()) {
		t.Errorf("导出应为 GBK 字节(本用例含中文,不应为合法 UTF-8)")
	}
	cols2, _, rows2, err := gsconfig.Parse(rec2.Body.Bytes())
	if err != nil {
		t.Fatalf("导出内容应可再导入 Parse: %v", err)
	}
	if len(cols2) == 0 || len(rows2) == 0 {
		t.Fatalf("导出解析后不应为空, cols=%d rows=%d", len(cols2), len(rows2))
	}
}

func TestServerListReadsFromConfigDB(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/servers", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/servers status=%d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "测试一服") {
		t.Errorf("服务器管理页应含配置库中的服名")
	}
}

// 合服废弃服不单独成行,改在目标服行上以「已合入」徽章呈现(端到端渲染验证)。
func TestServerListShowsMergedBadgeAndHidesDiscarded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gdb, err := db.Init("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("db init: %v", err)
	}
	cfg := &config.Config{}
	cfg.Auth.PasswordEnabled = true
	st, _ := settings.NewStore(settings.Settings{Mode: "mock", ShowAllServers: true}, "")
	gdb.Exec("DROP TABLE IF EXISTS config_server")
	gdb.Exec("DROP TABLE IF EXISTS config_columns")
	gdb.Exec("DROP TABLE IF EXISTS config_metas")
	store := gsconfig.NewStore(gdb)
	// 含 RealWorldID/Desc 列的最小配置:10235 普通服 + 10240 合入 10235 的废弃服。
	sample := "Id\tWorldName\tWorldType\tRealWorldID\tDesc\tSelfPublicIp\tRealSelfPublicIp\n" +
		"INT\tSTRING\tINT\tINT\tSTRING\tSTRING\tSTRING\n" +
		"MAX_ID=59999;MAX_RECORD=60000;HttpAgent;DBAgent;GMServer;\tserver\tserver\tserver\tserver\tserver\tserver\n" +
		"#\t\t\t\t\t\t\n" +
		"10235\tW235\t0\t10235\t235世界-彭建军\t192.168.11.166\t192.168.11.166\n" +
		"10240\tWdrc\t-1\t10235\tdrc\t192.168.11.166\t192.168.11.166\n"
	cols, meta, rows, err := gsconfig.Parse([]byte(sample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := store.EnsureSchema(cols); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := store.ImportAll(cols, meta, rows); err != nil {
		t.Fatalf("import: %v", err)
	}
	up := &fakeUploader{}
	gsSvc := gsconfig.NewService(store, up, "s", "c", "", "")
	mpStore := mergepreview.NewStore(gdb)
	mpStore.EnsureSchema()
	h := New(cfg, gdb, order.New(gdb, st, nil), member.New(gdb), st, gsSvc, mergepreview.NewService(mpStore), up)
	r := gin.New()
	h.Register(r)
	r.LoadHTMLGlob("../../templates/*.html")

	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/servers", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/servers status=%d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "235世界-彭建军") {
		t.Errorf("目标服应展示")
	}
	if !strings.Contains(body, "已合入 1") {
		t.Errorf("目标服行应有「已合入 1」徽章")
	}
	if strings.Contains(body, `data-id="10240"`) {
		t.Errorf("合服废弃服不应作为独立行出现")
	}
	if !strings.Contains(body, "/servers/10240") {
		t.Errorf("徽章弹层应含原服 drc 的详情链接")
	}
}

func setupGSServerWithDB(t *testing.T) (*gin.Engine, *gorm.DB) {
	r, svc, _ := setupGSServer(t)
	return r, svc.Store().DB()
}

func TestGSGridDataReturnsColsRows(t *testing.T) {
	r, svc, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/gsconfig/grid/data", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Columns []struct {
			Field    string `json:"field"`
			Editable bool   `json:"editable"`
		} `json:"columns"`
		Rows []map[string]string `json:"rows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	cols, _ := svc.Store().Columns()
	rows, _ := svc.Store().AllRows()
	if len(resp.Columns) != len(cols) {
		t.Errorf("columns=%d want %d", len(resp.Columns), len(cols))
	}
	if len(resp.Rows) != len(rows) {
		t.Errorf("rows=%d want %d", len(resp.Rows), len(rows))
	}
	for _, c := range resp.Columns {
		if c.Field == "Id" && c.Editable {
			t.Errorf("Id 列应 editable=false")
		}
	}
}

func TestGSGridSaveApplies(t *testing.T) {
	r, svc, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	// 取一个真实存在的服号
	rows, _ := svc.Store().AllRows()
	if len(rows) == 0 {
		t.Skip("sample 无数据")
	}
	id := rows[0]["Id"]
	// 找一个 STRING 列(可编辑)
	cols, _ := svc.Store().Columns()
	strCol := ""
	for _, col := range cols {
		if col.Name != "Id" && col.GameType == "STRING" {
			strCol = col.Name
			break
		}
	}
	if strCol == "" {
		t.Skip("sample 无 STRING 列")
	}
	body := `{"changes":[{"id":"` + id + `","field":"` + strCol + `","value":"批量改名测试"}]}`
	req := httptest.NewRequest("POST", "/gsconfig/grid/save", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	got, _ := svc.Store().GetServer(id)
	if got[strCol] != "批量改名测试" {
		t.Errorf("%s=%q 未保存", strCol, got[strCol])
	}
}

func TestGSGridSaveRejectsIdAndBadInt(t *testing.T) {
	r, svc, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	rows, _ := svc.Store().AllRows()
	id := rows[0]["Id"]

	// 改 Id 被拒
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/gsconfig/grid/save", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(ck)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	w := post(`{"changes":[{"id":"` + id + `","field":"Id","value":"99999"}]}`)
	if strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("改 Id 应被拒, body=%s", w.Body.String())
	}

	// 找一个 INT 列填非整数被拒
	cols, _ := svc.Store().Columns()
	intCol := ""
	for _, col := range cols {
		if col.Name != "Id" && (col.GameType == "INT" || col.GameType == "BOOL") {
			intCol = col.Name
			break
		}
	}
	if intCol == "" {
		t.Skip("sample 无 INT 列")
	}
	w = post(`{"changes":[{"id":"` + id + `","field":"` + intCol + `","value":"abc"}]}`)
	if strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("INT 列填非整数应被拒, body=%s", w.Body.String())
	}
}

func TestGSGridBlocksNonAdmin(t *testing.T) {
	r, gdb := setupGSServerWithDB(t)
	if err := auth.UpsertAccount(gdb, "bob", "pw123", "Bob"); err != nil {
		t.Fatalf("user: %v", err)
	}
	ck := loginCookie(t, r, "bob", "pw123")
	req := httptest.NewRequest("GET", "/gsconfig/grid", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/orders" {
		t.Fatalf("非admin应重定向/orders, got %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestGSFullUpdateCreatesOrder(t *testing.T) {
	r, svc, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("POST", "/gsconfig/fullupdate", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/orders/") {
		t.Fatalf("code=%d loc=%s body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	var n int64
	svc.Store().DB().Model(&model.WorkOrder{}).Where("type = ?", model.TypeConfigPush).Count(&n)
	if n != 1 {
		t.Fatalf("configpush 工单数 = %d, want 1", n)
	}
}

func TestGSKindFromWorldType(t *testing.T) {
	cases := map[string]string{"0": "game", "2": "battle", "3": "copy", "4": "center", " 0 ": "game"}
	for in, want := range cases {
		if got := gsKindFromWorldType(in); got != want {
			t.Errorf("gsKindFromWorldType(%q)=%q want %q", in, got, want)
		}
	}
}

func TestGSDeleteBlockReason(t *testing.T) {
	rows := []map[string]string{
		{"Id": "12802", "WorldType": "2", "BattleWorldID": "12802"},
		{"Id": "10002", "WorldType": "0", "BattleWorldID": "12802", "GlobalCenterWorldID": "12500"},
		{"Id": "12500", "WorldType": "4", "GlobalCenterWorldID": "12500"},
	}
	if r := gsDeleteBlockReason(rows, 12802, "battle"); r == "" {
		t.Error("删被挂靠的战斗服应拒绝")
	}
	if r := gsDeleteBlockReason(rows, 12500, "center"); r == "" {
		t.Error("删被引用的中心服应拒绝")
	}
	if r := gsDeleteBlockReason(rows, 10002, "game"); r != "" {
		t.Errorf("删游戏服应放行, got %q", r)
	}
}
