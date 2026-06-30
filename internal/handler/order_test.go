package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gongdan/internal/config"
	"gongdan/internal/db"
	"gongdan/internal/gsconfig/cos"
	"gongdan/internal/model"
	"gongdan/internal/service/member"
	"gongdan/internal/service/order"
	"gongdan/internal/service/workflow"
	"gongdan/internal/settings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func mustParams(t *testing.T, p model.ReleaseParams) string {
	t.Helper()
	s, err := model.MarshalParams(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRetryFailedIDs(t *testing.T) {
	// 发版 + 执行失败 + 有失败集 → 返回失败集
	wo := &model.WorkOrder{
		Type:   model.TypeRelease,
		Status: workflow.StatusExecFailed,
		Params: mustParams(t, model.ReleaseParams{
			ServerIDs: []int{10001, 10002}, VersionPackage: "v.zip", LastFailedIDs: []int{10002},
		}),
	}
	got := retryFailedIDs(wo)
	if len(got) != 1 || got[0] != 10002 {
		t.Errorf("got %v, want [10002]", got)
	}

	// 非失败状态 → nil
	wo.Status = workflow.StatusExecuting
	if retryFailedIDs(wo) != nil {
		t.Error("非 exec_failed 应返回 nil")
	}

	// 非发版类型 → nil
	if retryFailedIDs(&model.WorkOrder{Type: model.TypeMerge, Status: workflow.StatusExecFailed}) != nil {
		t.Error("非发版类型应返回 nil")
	}

	// 失败集为空(如设维护中止)→ nil
	wo2 := &model.WorkOrder{
		Type:   model.TypeRelease,
		Status: workflow.StatusExecFailed,
		Params: mustParams(t, model.ReleaseParams{ServerIDs: []int{10001}, VersionPackage: "v.zip"}),
	}
	if retryFailedIDs(wo2) != nil {
		t.Error("无失败集应返回 nil")
	}
}

func TestReleaseParamsHotFieldNames(t *testing.T) {
	p := model.ReleaseParams{IncludeHotUpdate: true, HotPackage: "c.zip", HotFiles: "a.ini"}
	if !p.IncludeHotUpdate || p.HotPackage != "c.zip" || p.HotFiles != "a.ini" {
		t.Error("字段不可用")
	}
}

func TestRetryFailedIDsHotupdate(t *testing.T) {
	params, _ := model.MarshalParams(model.HotupdateParams{
		ServerIDs:     []int{210, 211},
		ConfigPackage: "cfg.zip",
		HotFiles:      "a.ini",
		LastFailedIDs: []int{211},
	})
	wo := &model.WorkOrder{
		Type:   model.TypeHotupdate,
		Status: workflow.StatusExecFailed,
		Params: params,
	}
	got := retryFailedIDs(wo)
	if len(got) != 1 || got[0] != 211 {
		t.Errorf("热更失败工单 retryFailedIDs=%v, want [211]", got)
	}
}

// findRow 在 rows 中找到指定标签的行;找不到返回 nil。
func findRow(rows []ParamRow, label string) *ParamRow {
	for i := range rows {
		if rows[i].Label == label {
			return &rows[i]
		}
	}
	return nil
}

func TestParamRowsRelease(t *testing.T) {
	// 发版:不连带、不热更 → 目标服/版本包/连带=否/发版后热更=否,无热更包行
	wo := &model.WorkOrder{Type: model.TypeRelease, Params: mustParams(t, model.ReleaseParams{
		ServerIDs: []int{235}, VersionPackage: "ProjectT_x.zip",
	})}
	rows := paramRows(wo)

	if r := findRow(rows, "目标服"); r == nil || r.Value != "1 个：235" || r.Fold != "" {
		t.Errorf("目标服行错误: %+v", r)
	}
	if r := findRow(rows, "版本包"); r == nil || r.Value != "ProjectT_x.zip" {
		t.Errorf("版本包行错误: %+v", r)
	}
	if r := findRow(rows, "连带战斗/副本服"); r == nil || r.Value != "否" || r.Highlight != "no" {
		t.Errorf("连带行错误: %+v", r)
	}
	if r := findRow(rows, "发版后热更"); r == nil || r.Value != "否" || r.Highlight != "no" {
		t.Errorf("热更行错误: %+v", r)
	}
	if findRow(rows, "热更配置包") != nil {
		t.Error("不热更时不应有热更配置包行")
	}
}

func TestParamRowsReleaseWithBattleAndHot(t *testing.T) {
	wo := &model.WorkOrder{Type: model.TypeRelease, Params: mustParams(t, model.ReleaseParams{
		ServerIDs: []int{235}, VersionPackage: "v.zip",
		IncludeBattle: true, IncludeHotUpdate: true, HotPackage: "cfg.zip", HotFiles: "a.ini",
	})}
	rows := paramRows(wo)

	if r := findRow(rows, "连带战斗/副本服"); r == nil || r.Value != "是" || r.Highlight != "yes" {
		t.Errorf("连带行错误: %+v", r)
	}
	if r := findRow(rows, "发版后热更"); r == nil || r.Value != "是" || r.Highlight != "yes" {
		t.Errorf("热更行错误: %+v", r)
	}
	if r := findRow(rows, "热更配置包"); r == nil || r.Value != "cfg.zip" {
		t.Errorf("热更配置包行错误: %+v", r)
	}
	if r := findRow(rows, "热更文件"); r == nil || r.Value != "a.ini" {
		t.Errorf("热更文件行错误: %+v", r)
	}
}

func TestParamRowsTargetFold(t *testing.T) {
	// 超过 12 个服 → 目标服只显示数量,完整列表进 Fold
	ids := make([]int, 0, 13)
	for i := 0; i < 13; i++ {
		ids = append(ids, 1000+i)
	}
	wo := &model.WorkOrder{Type: model.TypeRelease, Params: mustParams(t, model.ReleaseParams{
		ServerIDs: ids, VersionPackage: "v.zip",
	})}
	r := findRow(paramRows(wo), "目标服")
	if r == nil || r.Value != "13 个" || r.Fold == "" {
		t.Errorf("多服应折叠: %+v", r)
	}
}

func TestParamRowsHotupdate(t *testing.T) {
	params, _ := model.MarshalParams(model.HotupdateParams{
		ServerIDs: []int{210, 211}, ConfigPackage: "cfg.zip", HotFiles: "a.ini", IncludeBattle: true,
	})
	rows := paramRows(&model.WorkOrder{Type: model.TypeHotupdate, Params: params})

	if r := findRow(rows, "目标服"); r == nil || r.Value != "2 个：210,211" {
		t.Errorf("目标服行错误: %+v", r)
	}
	if r := findRow(rows, "配置包"); r == nil || r.Value != "cfg.zip" {
		t.Errorf("配置包行错误: %+v", r)
	}
	if r := findRow(rows, "连带战斗/副本服"); r == nil || r.Value != "是" || r.Highlight != "yes" {
		t.Errorf("连带行错误: %+v", r)
	}
}

func TestParamRowsMerge(t *testing.T) {
	params, _ := model.MarshalParams(model.MergeParams{
		IncludeMerge: true,
		Pairs:        []model.MergePair{{Target: 1, Source: 2}, {Target: 3, Source: 4}},
		ToolPackage:  "tool.zip",
	})
	rows := paramRows(&model.WorkOrder{Type: model.TypeMerge, Params: params})
	if r := findRow(rows, "合服组数"); r == nil || r.Value != "2 组" {
		t.Errorf("合服组数行错误: %+v", r)
	}
	if r := findRow(rows, "合服工具包"); r == nil || r.Value != "tool.zip" {
		t.Errorf("工具包行错误: %+v", r)
	}
}

func TestParamRowsNewServer(t *testing.T) {
	params, _ := model.MarshalParams(model.NewServerCreateParams{
		RegionName: "洛阳", VersionPackage: "v9.zip",
		Rows: []model.NewServerRow{
			{Kind: "game", ID: 10002, SelfPublicIp: "10.0.0.1"},
			{Kind: "battle", ID: 12802, SelfPublicIp: "10.1.0.2"},
		},
	})
	rows := paramRows(&model.WorkOrder{Type: model.TypeNewServer, Params: params})
	if r := findRow(rows, "版本包"); r == nil || r.Value != "v9.zip" {
		t.Errorf("版本包行错误: %+v", r)
	}
	if r := findRow(rows, "区服名"); r == nil || r.Value != "洛阳" {
		t.Errorf("区服名行错误: %+v", r)
	}
	if r := findRow(rows, "新建游戏服"); r == nil || r.Value != "10002" {
		t.Errorf("新建游戏服行错误: %+v", r)
	}
	if r := findRow(rows, "全部服"); r == nil || !strings.Contains(r.Value, "战斗服 12802") {
		t.Errorf("全部服行错误: %+v", r)
	}
}

func TestNormalizeMergeDate(t *testing.T) {
	got, err := normalizeMergeDate("2026-06-05")
	if err != nil || got != "20260605" {
		t.Errorf("got %q err %v", got, err)
	}
	if _, err := normalizeMergeDate("bad"); err == nil {
		t.Error("非法日期应报错")
	}
}

func TestParamRowsMergeCombined(t *testing.T) {
	p := model.MergeParams{
		IncludeMerge: true, Pairs: []model.MergePair{{Target: 10001, Source: 10004}}, ToolPackage: "t.zip",
		IncludePreMerge: true, PreMergeIDs: []int{20001}, PreMergeDate: "20260605",
	}
	params, _ := model.MarshalParams(p)
	rows := paramRows(&model.WorkOrder{Type: model.TypeMerge, Params: params})
	joined := ""
	for _, r := range rows {
		joined += r.Label + "=" + r.Value + ";"
	}
	if !strings.Contains(joined, "预合服时间=2026-06-05") {
		t.Errorf("详情应含格式化预合服时间: %s", joined)
	}
	if !strings.Contains(joined, "合服工具包=t.zip") {
		t.Errorf("详情应含合服工具包: %s", joined)
	}
}

func TestParamRowsInvalid(t *testing.T) {
	if paramRows(&model.WorkOrder{Type: model.TypeRelease, Params: "{not json"}) != nil {
		t.Error("非法 JSON 应返回 nil")
	}
	if paramRows(&model.WorkOrder{Type: "unknown", Params: "{}"}) != nil {
		t.Error("未知类型应返回 nil")
	}
}

func TestListPackages(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"b.zip", "a.zip", "note.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got := listPackages(dir)
	if len(got) != 2 || got[0] != "a.zip" || got[1] != "b.zip" {
		t.Errorf("got %v, want [a.zip b.zip](排序)", got)
	}
	if listPackages(filepath.Join(dir, "nope")) != nil {
		t.Error("不存在目录应返回 nil")
	}
}

func TestParseScheduledTime(t *testing.T) {
	if got := parseScheduledTime(""); got != nil {
		t.Error("空串应为 nil(立即)")
	}
	got := parseScheduledTime("2026-06-03T03:00")
	if got == nil || got.Hour() != 3 || got.Day() != 3 {
		t.Errorf("应解析出 06-03 03:00, 实际 %v", got)
	}
}

// cfgViewCOS 是一个可配置的 COS stub,供审批表格测试用。
type cfgViewCOS struct {
	data []byte
	ver  string
}

func (c cfgViewCOS) Upload(string, []byte) (string, error)      { return c.ver, nil }
func (c cfgViewCOS) Download(string, ...string) ([]byte, error) { return c.data, nil }
func (c cfgViewCOS) HeadVersionID(string) (string, error)       { return c.ver, nil }

// rollbackCOS 是回滚测试专用 COS stub:
// 带版本参数时返回历史快照,不带版本(当前线上基准)返回 ErrNotFound → 视为空基准 → diff 非空 → 建新单。
type rollbackCOS struct{ data []byte }

func (c rollbackCOS) Upload(string, []byte) (string, error) { return "v-new", nil }
func (c rollbackCOS) Download(key string, ver ...string) ([]byte, error) {
	if len(ver) > 0 {
		return c.data, nil // 历史版本
	}
	return nil, cos.ErrNotFound // 当前线上(空基准)
}
func (c rollbackCOS) HeadVersionID(string) (string, error) { return "", cos.ErrNotFound }

func TestOrderRollbackCreatesNewOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gdb, err := db.Init("file:rollback_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	sample, err := os.ReadFile("../../internal/gsconfig/testdata/sample.txt")
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	cfg := &config.Config{}
	cfg.Auth.PasswordEnabled = true
	st, _ := settings.NewStore(settings.Settings{Mode: "mock"}, "")
	orders := order.New(gdb, st, nil)
	h := New(cfg, gdb, orders, member.New(gdb), st, nil, nil, rollbackCOS{data: sample})
	r := gin.New()
	h.Register(r)
	r.LoadHTMLGlob("../../templates/*.html")

	wo, err := orders.CreateWithArtifact(model.TypeConfigPush, "全服更新", "全服",
		"server/ServerConfigList.txt",
		map[string][]byte{"server/ServerConfigList.txt": sample}, "", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := orders.SetDeployedVersion(wo.ID, "v9"); err != nil {
		t.Fatalf("setdep: %v", err)
	}

	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("POST", "/orders/"+strconv.FormatUint(uint64(wo.ID), 10)+"/rollback", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var n int64
	gdb.Model(&model.WorkOrder{}).Where("type = ?", model.TypeConfigPush).Count(&n)
	if n != 2 {
		t.Fatalf("应有原单+回滚单共2张, got %d", n)
	}
}

// 新建工单页应内联提交前的「关键信息确认窗」逻辑(buildSummary + 走 uiConfirm)。
func TestOrderNewPageHasSubmitConfirm(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gdb, err := db.Init("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	cfg := &config.Config{}
	cfg.Auth.PasswordEnabled = true
	st, _ := settings.NewStore(settings.Settings{Mode: "mock"}, "")
	h := New(cfg, gdb, order.New(gdb, st, nil), member.New(gdb), st, nil, nil, nil)
	r := gin.New()
	h.Register(r)
	r.LoadHTMLGlob("../../templates/*.html")

	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/orders/new", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/orders/new code=%d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"buildSummary", "确认提交工单", "uiConfirm"} {
		if !strings.Contains(body, want) {
			t.Errorf("新建工单页缺少确认窗逻辑标记 %q", want)
		}
	}
}

// 合服服号必须真实存在于配置库:不存在的服号应被拒,存在的放行。
func TestOrderCreateMergeValidatesServerExistence(t *testing.T) {
	r, _, _ := setupGSServer(t) // 样例库含服号 1/2/3
	ck := loginCookie(t, r, "admin", "admin123")

	post := func(pairs string) *httptest.ResponseRecorder {
		form := url.Values{"type": {"merge"}, "title": {"t"},
			"include_merge": {"on"}, "merge_pairs": {pairs}, "tool_package": {"x.zip"}}
		req := httptest.NewRequest("POST", "/orders", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(ck)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// 99999 不存在 -> 400 且提示不存在
	if w := post("1,99999"); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "不存在") {
		t.Fatalf("不存在的服号应被拒, code=%d body=%s", w.Code, w.Body.String())
	}
	// 1,2 均存在 -> 放行(创建成功跳转 302)
	if w := post("1,2"); w.Code == http.StatusBadRequest {
		t.Fatalf("存在的服号不应被拒, body=%s", w.Body.String())
	}
}

func TestOrderConfigDataReturnsDiff(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gdb, err := db.Init("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	sample, err := os.ReadFile("../../internal/gsconfig/testdata/sample.txt")
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	cfg := &config.Config{}
	cfg.Auth.PasswordEnabled = true
	st, _ := settings.NewStore(settings.Settings{Mode: "mock"}, "")
	orders := order.New(gdb, st, nil)
	cosStub := cfgViewCOS{data: sample, ver: "v-live"} // 线上版本 v-live;工件 baseline "" → stale
	h := New(cfg, gdb, orders, member.New(gdb), st, nil, nil, cosStub)
	r := gin.New()
	h.Register(r)
	r.LoadHTMLGlob("../../templates/*.html")

	wo, err := orders.CreateWithArtifact(model.TypeConfigPush, "全服更新", "全服",
		"server/ServerConfigList.txt",
		map[string][]byte{"server/ServerConfigList.txt": sample}, "", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/orders/"+strconv.FormatUint(uint64(wo.ID), 10)+"/config", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Columns       []string            `json:"columns"`
		Rows          []map[string]string `json:"rows"`
		BaselineStale bool                `json:"baselineStale"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(resp.Columns) == 0 || len(resp.Rows) == 0 {
		t.Fatalf("空表 cols=%d rows=%d", len(resp.Columns), len(resp.Rows))
	}
	if !resp.BaselineStale {
		t.Fatal("线上版本 v-live ≠ 工件 baseline \"\",应 stale")
	}
}


func getBody(t *testing.T, r *gin.Engine, ck *http.Cookie, path string) string {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d, want 200 (body=%s)", path, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestOrderSubPagesRender(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	for _, tc := range []struct {
		path, want string
	}{
		{"/orders/mine", "我的工单"},
		{"/orders/pending", "待审批"},
		{"/orders/executions", "执行记录"},
		{"/orders/auditlog", "操作日志"},
	} {
		if body := getBody(t, r, ck, tc.path); !strings.Contains(body, tc.want) {
			t.Errorf("%s 页应含标题 %q", tc.path, tc.want)
		}
	}
}

func TestOrderExecutionsUsesUnifiedListShell(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	body := getBody(t, r, ck, "/orders/executions")
	for _, want := range []string{`class="ol-card"`, `id="ol-q"`, `class="ol-pager"`} {
		if !strings.Contains(body, want) {
			t.Errorf("执行记录页应使用统一列表骨架，缺少 %q", want)
		}
	}
}

func TestOrderAuditLogUsesUnifiedListShell(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	body := getBody(t, r, ck, "/orders/auditlog")
	for _, want := range []string{`class="ol-card"`, `id="ol-q"`, `class="ol-pager"`} {
		if !strings.Contains(body, want) {
			t.Errorf("操作日志页应使用统一列表骨架，缺少 %q", want)
		}
	}
}

func TestOrderMineFiltersByCreator(t *testing.T) {
	r, _, _ := setupGSServer(t)
	// 直接落两张不同提交人的工单到测试库
	gdb := mustOrderDB(t)
	mkOrder := func(no, by string) {
		if err := gdb.Create(&model.WorkOrder{
			OrderNo: no, Type: model.TypeRelease, Title: "t", Status: workflow.StatusPendingApprove,
			CreateBy: by,
		}).Error; err != nil {
			t.Fatalf("create %s: %v", no, err)
		}
	}
	mkOrder("WO-MINE-1", "admin")
	mkOrder("WO-OTHER-1", "bob")

	ck := loginCookie(t, r, "admin", "admin123")
	body := getBody(t, r, ck, "/orders/mine")
	if !strings.Contains(body, "WO-MINE-1") {
		t.Error("我的工单应含 admin 提交的单")
	}
	if strings.Contains(body, "WO-OTHER-1") {
		t.Error("我的工单不应含他人提交的单")
	}
}

func TestOrderExecutionsOnlyExecuted(t *testing.T) {
	r, _, _ := setupGSServer(t)
	gdb := mustOrderDB(t)
	now := time.Now()
	end := now.Add(90 * time.Second)
	if err := gdb.Create(&model.WorkOrder{
		OrderNo: "WO-EXEC-1", Type: model.TypeRelease, Title: "t", Status: workflow.StatusPendingTest,
		CreateBy: "admin", ExecuteBy: "admin", ExecuteTime: &now, ExecuteEndTime: &end,
	}).Error; err != nil {
		t.Fatalf("create exec: %v", err)
	}
	if err := gdb.Create(&model.WorkOrder{
		OrderNo: "WO-NOEXEC-1", Type: model.TypeRelease, Title: "t", Status: workflow.StatusPendingApprove,
		CreateBy: "admin",
	}).Error; err != nil {
		t.Fatalf("create noexec: %v", err)
	}

	ck := loginCookie(t, r, "admin", "admin123")
	body := getBody(t, r, ck, "/orders/executions")
	if !strings.Contains(body, "WO-EXEC-1") {
		t.Error("执行记录应含已执行工单")
	}
	if strings.Contains(body, "WO-NOEXEC-1") {
		t.Error("执行记录不应含未执行工单")
	}
	if !strings.Contains(body, "1分30秒") {
		t.Errorf("执行记录应展示耗时 1分30秒, body=%s", body)
	}
}

func TestOrderAuditLogShowsActions(t *testing.T) {
	r, _, _ := setupGSServer(t)
	gdb := mustOrderDB(t)
	if err := gdb.Create(&model.WorkOrder{ID: 9001, OrderNo: "WO-AUD-1", Type: model.TypeRelease,
		Title: "t", Status: workflow.StatusPendingApprove, CreateBy: "admin"}).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}
	// action 日志应出现,exec 逐行日志不应出现
	gdb.Create(&model.WorkOrderLog{OrderID: 9001, LogType: model.LogAction,
		Stage: workflow.StageSubmit, Operator: "admin", Content: "提交工单", CreateTime: time.Now()})
	gdb.Create(&model.WorkOrderLog{OrderID: 9001, LogType: model.LogExec,
		Stage: workflow.StageExecute, Operator: "system", Content: "脚本输出一行", CreateTime: time.Now()})

	ck := loginCookie(t, r, "admin", "admin123")
	body := getBody(t, r, ck, "/orders/auditlog")
	if !strings.Contains(body, "提交工单") || !strings.Contains(body, "WO-AUD-1") {
		t.Error("操作日志应含动作日志与工单号")
	}
	if strings.Contains(body, "脚本输出一行") {
		t.Error("操作日志不应混入逐行执行输出")
	}
}

func TestOrderLogsAfterReturnsIncrementalLines(t *testing.T) {
	r, _, _ := setupGSServer(t)
	gdb := mustOrderDB(t)
	gdb.Exec("DELETE FROM work_order_logs")
	gdb.Exec("DELETE FROM work_orders")

	wo := model.WorkOrder{
		OrderNo:    "WO-LOG-INC-1",
		Type:       model.TypeRelease,
		Title:      "logs",
		Status:     workflow.StatusExecuting,
		CreateBy:   "admin",
		CreateTime: time.Now(),
		UpdateTime: time.Now(),
	}
	if err := gdb.Create(&wo).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	logs := []model.WorkOrderLog{
		{OrderID: wo.ID, LogType: model.LogExec, Stage: workflow.StageExecute, Operator: "system", Content: "line-1", CreateTime: time.Now()},
		{OrderID: wo.ID, LogType: model.LogExec, Stage: workflow.StageExecute, Operator: "system", Content: "line-2", CreateTime: time.Now()},
		{OrderID: wo.ID, LogType: model.LogExec, Stage: workflow.StageExecute, Operator: "system", Content: "line-3", CreateTime: time.Now()},
	}
	for i := range logs {
		if err := gdb.Create(&logs[i]).Error; err != nil {
			t.Fatalf("create log %d: %v", i, err)
		}
	}

	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET",
		"/orders/"+strconv.FormatUint(uint64(wo.ID), 10)+"/logs?poll=1&after="+strconv.FormatUint(uint64(logs[0].ID), 10), nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "line-1") {
		t.Fatalf("after cursor should exclude old line, body=%s", body)
	}
	if !strings.Contains(body, "line-2") || !strings.Contains(body, "line-3") {
		t.Fatalf("should contain incremental lines, body=%s", body)
	}
	if got := w.Header().Get("X-Log-Last-ID"); got != strconv.FormatUint(uint64(logs[2].ID), 10) {
		t.Fatalf("X-Log-Last-ID=%q want %d", got, logs[2].ID)
	}
}

func mustOrderDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := db.Init("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	return gdb
}

func TestOrderCreateRejectsSystemOnlyTypes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gdb, err := db.Init("file:rejecttypes?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	cfg := &config.Config{}
	cfg.Auth.PasswordEnabled = true
	st, _ := settings.NewStore(settings.Settings{Mode: "mock"}, "")
	h := New(cfg, gdb, order.New(gdb, st, nil), member.New(gdb), st, nil, nil, nil)
	r := gin.New()
	h.Register(r)
	r.LoadHTMLGlob("../../templates/*.html")
	ck := loginCookie(t, r, "admin", "admin123")
	for _, ty := range []string{"configpush", "mergepublish"} {
		form := url.Values{"type": {ty}, "title": {"x"}}
		req := httptest.NewRequest("POST", "/orders", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(ck)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("type=%s code=%d, want 400 (系统专属类型不可手动建单)", ty, w.Code)
		}
	}
}

func TestOrderRepackageUpdatesPackage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gdb, err := db.Init("file:repackage_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	cfg := &config.Config{}
	cfg.Auth.PasswordEnabled = true
	st, _ := settings.NewStore(settings.Settings{Mode: "mock"}, "")
	orders := order.New(gdb, st, nil)
	h := New(cfg, gdb, orders, member.New(gdb), st, nil, nil, nil)
	r := gin.New()
	h.Register(r)
	r.LoadHTMLGlob("../../templates/*.html")

	p := model.NewServerCreateParams{RegionName: "洛阳", VersionPackage: "old.zip",
		Rows: []model.NewServerRow{{Kind: "game", ID: 10002, Fields: map[string]string{"Id": "10002"}}}}
	params, _ := model.MarshalParams(p)
	wo, err := orders.Create(model.TypeNewServer, "创建游戏服", "洛阳", params, "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wo.Status = workflow.StatusExecFailed
	gdb.Save(wo)

	ck := loginCookie(t, r, "admin", "admin123")
	form := url.Values{"version_package": {"new.zip"}}
	req := httptest.NewRequest("POST", "/orders/"+strconv.FormatUint(uint64(wo.ID), 10)+"/repackage",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	got, _ := orders.Get(wo.ID)
	gp, _ := model.UnmarshalNewServerCreateParams(got.Params)
	if gp.VersionPackage != "new.zip" {
		t.Errorf("版本包未更新: %q", gp.VersionPackage)
	}
}
