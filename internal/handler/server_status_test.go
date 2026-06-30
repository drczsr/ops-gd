package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"gongdan/internal/gameserver"
)

// mock 模式下状态接口不连网,全部服返回在线(7)。
func TestServerStatusMockJSON(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/servers/status", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/servers/status status=%d, want 200", w.Code)
	}
	var resp struct {
		Mock     bool `json:"mock"`
		Statuses map[string]struct {
			Mask int    `json:"mask"`
			Err  string `json:"err"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应应为 JSON: %v\n%s", err, w.Body.String())
	}
	if !resp.Mock {
		t.Errorf("mock 模式响应应标记 mock=true")
	}
	if len(resp.Statuses) == 0 {
		t.Fatalf("应包含全部服的状态")
	}
	for id, st := range resp.Statuses {
		if st.Mask != 7 {
			t.Errorf("mock 模式服%s应在线(7),got %+v", id, st)
		}
	}
}

func TestServerStatusRequiresLogin(t *testing.T) {
	r, _, _ := setupGSServer(t)
	req := httptest.NewRequest("GET", "/servers/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("未登录 status=%d, want 302", w.Code)
	}
}

// 服务器管理页与状态探测应受「显示全部服务器」开关约束:
// 关闭后样例服(Id 1~3,≤10000)应被隐藏,状态接口也不再探测它们。
func TestServerListRespectsShowAllServers(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")

	// 通过系统设置页在线关闭「显示全部服务器」(表单不带 show_all_servers 即为关)
	form := url.Values{"mode": {"mock"}}
	req := httptest.NewRequest("POST", "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusFound {
		t.Fatalf("关闭开关失败 status=%d", w.Code)
	}

	req = httptest.NewRequest("GET", "/servers", nil)
	req.AddCookie(ck)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/servers status=%d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "没有服务器数据") {
		t.Errorf("开关关闭后 Id≤10000 的样例服应被隐藏")
	}

	req = httptest.NewRequest("GET", "/servers/status", nil)
	req.AddCookie(ck)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp struct {
		Statuses map[string]struct {
			Mask int `json:"mask"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应应为 JSON: %v", err)
	}
	if len(resp.Statuses) != 0 {
		t.Errorf("开关关闭后不应探测隐藏的服,got %d 条", len(resp.Statuses))
	}
}

// 合服废弃服(WorldType==-1)永不作为独立行展示——即便「显示全部服务器」开启。
func TestVisibleServersAlwaysHidesMergedAway(t *testing.T) {
	servers := []gameserver.Server{
		{ID: 10235, WorldType: 0, RealWorldID: 10235},
		{ID: 10240, WorldType: -1, RealWorldID: 10235}, // 合入 10235 的废弃服
		{ID: 12801, WorldType: 4, RealWorldID: 12801},  // 中心服:仅 showAll 显示
	}
	on := visibleServers(servers, true)
	for _, s := range on {
		if s.WorldType == -1 {
			t.Errorf("showAll 开启也不应出现合服废弃服: %+v", s)
		}
	}
	if len(on) != 2 { // 普通服 + 中心服;废弃服被剔除
		t.Errorf("showAll 应显示 2 台(剔除废弃服), got %d: %+v", len(on), on)
	}
	off := visibleServers(servers, false)
	if len(off) != 1 || off[0].ID != 10235 {
		t.Errorf("关闭开关应只剩普通服 10235, got %+v", off)
	}
}

func TestServerRegionGroups(t *testing.T) {
	servers := []gameserver.Server{
		{ID: 10001, Desc: "洛阳1服"},
		{ID: 10002, Desc: "洛阳2服(正式)"},
		{ID: 10003, Desc: "大理1服"},
		{ID: 10004, Desc: "战斗服"},     // 无 WoRegion → GroupName(Desc)="战斗"
		{ID: 10005, Desc: "随便写的名"}, // WoRegion 覆盖
	}
	woRegion := map[int]string{10005: "苏州"} // 仅 10005 有库内大区,覆盖 Desc 前缀
	groups, regionOf := serverRegionGroups(servers, woRegion)

	// 按首次出现顺序:洛阳、大理、战斗、苏州
	wantOrder := []struct {
		region string
		count  int
	}{{"洛阳", 2}, {"大理", 1}, {"战斗", 1}, {"苏州", 1}}
	if len(groups) != len(wantOrder) {
		t.Fatalf("分组数 = %d, want %d: %+v", len(groups), len(wantOrder), groups)
	}
	for i, w := range wantOrder {
		if groups[i].Region != w.region || groups[i].Count != w.count {
			t.Errorf("分组[%d] = {%q,%d}, want {%q,%d}", i, groups[i].Region, groups[i].Count, w.region, w.count)
		}
	}
	if regionOf[10002] != "洛阳" || regionOf[10004] != "战斗" || regionOf[10005] != "苏州" {
		t.Errorf("regionOf 映射不符: %+v", regionOf)
	}
}

func TestMergedSourcesGroup(t *testing.T) {
	// 无合入源服 → 不生成分组
	if _, ok := mergedSourcesGroup(nil); ok {
		t.Errorf("无源服时不应生成「已合入的服」分组")
	}
	g, ok := mergedSourcesGroup([]gameserver.MergedSource{
		{ID: 10000, Desc: "drc"},
		{ID: 10001, Desc: ""}, // 缺区服名 → 回退「服10001」
	})
	if !ok {
		t.Fatal("有源服时应生成分组")
	}
	if g.Title != "已合入的服" {
		t.Errorf("分组标题 = %q", g.Title)
	}
	if len(g.Fields) != 2 {
		t.Fatalf("应每个源服一行, got %d", len(g.Fields))
	}
	if g.Fields[0].Value != "drc(ID 10000)" {
		t.Errorf("源服值 = %q, want drc(ID 10000)", g.Fields[0].Value)
	}
	if g.Fields[1].Value != "服10001(ID 10001)" {
		t.Errorf("缺名回退 = %q", g.Fields[1].Value)
	}
}

func TestServerBatchLoginLimit(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")

	// 合法:对样例服 1、2 设登录限制=3(mock 模式直接成功)
	form := url.Values{"ids": {"1,2"}, "limit": {"3"}}
	req := httptest.NewRequest("POST", "/servers/batch/loginlimit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		OK      bool `json:"ok"`
		Results []struct {
			ID int  `json:"id"`
			OK bool `json:"ok"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应应为 JSON: %v", err)
	}
	if !resp.OK || len(resp.Results) != 2 {
		t.Fatalf("应返回 2 条结果, got %+v", resp)
	}
	for _, x := range resp.Results {
		if !x.OK {
			t.Errorf("mock 模式应成功: %+v", x)
		}
	}

	// 非法 limit(7 不在允许集)应 400
	form2 := url.Values{"ids": {"1"}, "limit": {"7"}}
	req2 := httptest.NewRequest("POST", "/servers/batch/loginlimit", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.AddCookie(ck)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("非法 limit 应 400, got %d", w2.Code)
	}
}

// 服务器管理页(云控制台风格)应展示实例名称/ID、状态、类型、IP(公网/私网)等列。
func TestServerListShowsOutIPAndStatusColumns(t *testing.T) {
	r, _, _ := setupGSServer(t)
	ck := loginCookie(t, r, "admin", "admin123")
	req := httptest.NewRequest("GET", "/servers", nil)
	req.AddCookie(ck)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/servers status=%d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, col := range []string{"ID", "实例名称", "状态", "类型", "版本", "所属战场服", "主IPv4地址", "公网", "私网"} {
		if !strings.Contains(body, col) {
			t.Errorf("应有「%s」列/字段", col)
		}
	}
}
