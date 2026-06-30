package gsconfig

import (
	"reflect"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestListServersFilter:showAll=false 仅留 Id>10000 且 WorldType∈{0,1,2,3};showAll=true 全显示。
func TestListServersFilter(t *testing.T) {
	st := newMemStore(t)
	cols := []Column{
		{Ordinal: 1, Name: "Id", GameType: "INT"},
		{Ordinal: 2, Name: "WorldName", GameType: "STRING", Row3Tag: "server"},
		{Ordinal: 3, Name: "WorldType", GameType: "INT", Row3Tag: "server"},
	}
	if err := st.EnsureSchema(cols); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	add := func(id, wt string) {
		if err := st.AddServer(id, map[string]string{"WorldName": "S" + id, "WorldType": wt}); err != nil {
			t.Fatalf("AddServer %s: %v", id, err)
		}
	}
	add("10001", "0")  // 留
	add("10002", "1")  // 留(大世界)
	add("10003", "2")  // 留
	add("10004", "3")  // 留
	add("10005", "-1") // 合服废弃,隐藏
	add("10006", "4")  // 中心服,隐藏
	add("9999", "0")   // Id<=10000,隐藏

	rowIDs := func(rows []ServerRow) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r["Id"]
		}
		return out
	}

	filtered, err := st.ListServers("", false, 100, 0)
	if err != nil {
		t.Fatalf("ListServers(false): %v", err)
	}
	if got, want := rowIDs(filtered), []string{"10001", "10002", "10003", "10004"}; !reflect.DeepEqual(got, want) {
		t.Errorf("过滤后 = %v, want %v", got, want)
	}

	all, err := st.ListServers("", true, 100, 0)
	if err != nil {
		t.Fatalf("ListServers(true): %v", err)
	}
	if len(all) != 7 {
		t.Errorf("显示全部 = %d, want 7", len(all))
	}
}

func TestListServersSearchesDescName(t *testing.T) {
	st := newMemStore(t)
	cols := []Column{
		{Ordinal: 1, Name: "Id", GameType: "INT"},
		{Ordinal: 2, Name: "WorldName", GameType: "STRING", Row3Tag: "server"},
		{Ordinal: 3, Name: "Desc", GameType: "STRING"},
		{Ordinal: 4, Name: "WorldType", GameType: "INT", Row3Tag: "server"},
	}
	if err := st.EnsureSchema(cols); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := st.AddServer("10001", map[string]string{
		"WorldName": "YZW1",
		"Desc":      "望海城1服(正式服)",
		"WorldType": "0",
	}); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	rows, err := st.ListServers("望海城", true, 100, 0)
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(rows) != 1 || rows[0]["Id"] != "10001" {
		t.Fatalf("按 Desc 搜索 = %+v, want 10001", rows)
	}
}

func newMemStore(t *testing.T) *Store {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	gdb.Exec("DROP TABLE IF EXISTS config_server")
	gdb.Exec("DROP TABLE IF EXISTS config_columns")
	gdb.Exec("DROP TABLE IF EXISTS config_metas")
	return NewStore(gdb)
}

func TestColumnDescriptions(t *testing.T) {
	st := newMemStore(t)
	cols := []Column{
		{Ordinal: 1, Name: "Id", GameType: "INT"},
		{Ordinal: 2, Name: "WorldName", GameType: "STRING"},
		{Ordinal: 3, Name: "WorldType", GameType: "INT"},
	}
	if err := st.EnsureSchema(cols); err != nil {
		t.Fatalf("schema: %v", err)
	}
	// 第4行:#ID + 每列中文描述(tab 分隔)
	meta := Meta{Row4Line: "#ID\t世界名称\t世界类型(0普通,2战场服)"}
	if err := st.ImportAll(cols, meta, nil); err != nil {
		t.Fatalf("import: %v", err)
	}
	descs, err := st.ColumnDescriptions()
	if err != nil {
		t.Fatalf("descs: %v", err)
	}
	if descs["Id"] != "ID" { // 去掉前导 #
		t.Errorf("Id desc=%q want ID", descs["Id"])
	}
	if descs["WorldName"] != "世界名称" {
		t.Errorf("WorldName desc=%q", descs["WorldName"])
	}
	if descs["WorldType"] != "世界类型(0普通,2战场服)" {
		t.Errorf("WorldType desc=%q", descs["WorldType"])
	}
}

func importedStore(t *testing.T) *Store {
	t.Helper()
	st := newMemStore(t)
	cols, meta, rows, err := Parse(loadSample(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := st.EnsureSchema(cols); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := st.ImportAll(cols, meta, rows); err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	return st
}

func TestStoreImportAndAllRows(t *testing.T) {
	st := importedStore(t)
	rows, err := st.AllRows()
	if err != nil {
		t.Fatalf("AllRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("服数 = %d, want 3", len(rows))
	}
	if rows[0]["Id"] != "1" || rows[0]["WorldName"] != "测试一服" {
		t.Errorf("首行错: %+v", rows[0])
	}
}

func TestStoreUpdateAndGet(t *testing.T) {
	st := importedStore(t)
	if err := st.UpdateServer("2", map[string]string{"WorldName": "改名了", "SelfPublicIp": "10.0.0.2"}); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}
	got, err := st.GetServer("2")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if got["WorldName"] != "改名了" || got["SelfPublicIp"] != "10.0.0.2" {
		t.Errorf("更新未生效: %+v", got)
	}
}

func TestStoreAddServer(t *testing.T) {
	st := importedStore(t)
	err := st.AddServer("5", map[string]string{
		"Id": "5", "WorldName": "新服", "WorldType": "0", "SelfPublicIp": "10.0.0.5", "DataBasePsw": "p5",
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	rows, _ := st.AllRows()
	if len(rows) != 4 || rows[3]["Id"] != "5" {
		t.Errorf("新增后 AllRows 错: %+v", rows)
	}
}

func TestStoreAddServersBatchOK(t *testing.T) {
	st := importedStore(t)
	err := st.AddServers([]map[string]string{
		{"Id": "5", "WorldName": "五服"},
		{"Id": "6", "WorldName": "六服"},
	})
	if err != nil {
		t.Fatalf("AddServers: %v", err)
	}
	rows, _ := st.AllRows()
	if len(rows) != 5 {
		t.Fatalf("批量新增后应 5 行, 实际 %d", len(rows))
	}
}

func TestStoreAddServersFiltersUnknownColumns(t *testing.T) {
	// 样本列只有 Id/WorldName/WorldType/SelfPublicIp/DataBasePsw;写入含未知列应被忽略而非报错。
	st := importedStore(t)
	err := st.AddServers([]map[string]string{
		{"Id": "9", "WorldName": "九服", "PortForGlobalCenter": "3353", "NoSuchCol": "x"},
	})
	if err != nil {
		t.Fatalf("未知列应被过滤而非报错: %v", err)
	}
	got, err := st.GetServer("9")
	if err != nil || got["WorldName"] != "九服" {
		t.Errorf("已知列应正常写入: %+v err=%v", got, err)
	}
}

func TestStoreDropManagedAllowsReimportWithNewColumn(t *testing.T) {
	st := importedStore(t) // 旧 schema(样本 5 列,无 BrandNewCol)
	if err := st.DropManaged(); err != nil {
		t.Fatalf("DropManaged: %v", err)
	}
	// 用带新增列的列定义重建并导入
	cols := []Column{
		{Ordinal: 1, Name: "Id", GameType: "INT"},
		{Ordinal: 2, Name: "WorldName", GameType: "STRING"},
		{Ordinal: 3, Name: "BrandNewCol", GameType: "STRING"},
	}
	if err := st.EnsureSchema(cols); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := st.ImportAll(cols, Meta{Encoding: "GBK"}, []ServerRow{
		{"Id": "1", "WorldName": "一服", "BrandNewCol": "新值"},
	}); err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	got, err := st.GetServer("1")
	if err != nil || got["BrandNewCol"] != "新值" {
		t.Errorf("重建后新增列应可用: %+v err=%v", got, err)
	}
}

func TestStoreAddServersRollback(t *testing.T) {
	st := importedStore(t)
	// 第二行 Id=1 与样本已有行冲突 -> 整批回滚,第一行也不应落库
	err := st.AddServers([]map[string]string{
		{"Id": "7", "WorldName": "七服"},
		{"Id": "1", "WorldName": "撞号"},
	})
	if err == nil {
		t.Fatal("冲突应报错")
	}
	rows, _ := st.AllRows()
	if len(rows) != 3 {
		t.Errorf("整批回滚后应仍 3 行, 实际 %d(说明半批落库了)", len(rows))
	}
}

func TestStoreColumnsAndMeta(t *testing.T) {
	st := importedStore(t)
	cols, err := st.Columns()
	if err != nil || len(cols) != 5 || cols[0].Name != "Id" {
		t.Fatalf("Columns 错: %+v err %v", cols, err)
	}
	meta, err := st.Meta()
	if err != nil || meta.MaxID != 59999 {
		t.Fatalf("Meta 错: %+v err %v", meta, err)
	}
}

func TestAllRowsEmptyWhenNoTable(t *testing.T) {
	st := newMemStore(t)
	rows, err := st.AllRows()
	if err != nil || len(rows) != 0 {
		t.Errorf("空库 AllRows 应空+无错, got %v err %v", rows, err)
	}
}

func TestEnsureRegionColumnIsIdempotentAndExcludedFromFile(t *testing.T) {
	s := importedStore(t)
	if err := s.EnsureRegionColumn(); err != nil {
		t.Fatalf("EnsureRegionColumn: %v", err)
	}
	cols, _ := s.Columns()
	var found *Column
	for i := range cols {
		if cols[i].Name == "WoRegion" {
			found = &cols[i]
		}
	}
	if found == nil {
		t.Fatalf("WoRegion 列未创建")
	}
	if found.Row3Tag == "server" || found.Row3Tag == "client" {
		t.Errorf("WoRegion Row3Tag=%q 不能是 server/client", found.Row3Tag)
	}
	if err := s.EnsureRegionColumn(); err != nil {
		t.Fatalf("二次调用应幂等: %v", err)
	}
	_ = s.AddServer("99001", map[string]string{"WorldType": "0", "WoRegion": "测试区"})
	got, _ := s.GetServer("99001")
	if got["WoRegion"] != "测试区" {
		t.Errorf("WoRegion 读回=%q, want 测试区", got["WoRegion"])
	}
}

func TestGenerateExcludesRegionColumn(t *testing.T) {
	s := importedStore(t)
	if err := s.EnsureRegionColumn(); err != nil {
		t.Fatal(err)
	}
	cols, _ := s.Columns()
	meta, _ := s.Meta()
	rows, _ := s.AllRows()
	for _, tag := range []string{"server", "client"} {
		data, err := GenerateFor(cols, meta, rows, tag)
		if err != nil {
			t.Fatalf("GenerateFor(%s): %v", tag, err)
		}
		if strings.Contains(string(data), "WoRegion") {
			t.Errorf("%s 文件不应含 WoRegion 列头", tag)
		}
	}
}

func TestStoreDeleteServer(t *testing.T) {
	st := newMemStore(t)
	cols := []Column{
		{Ordinal: 1, Name: "Id", GameType: "INT"},
		{Ordinal: 2, Name: "Desc", GameType: "STRING"},
	}
	if err := st.EnsureSchema(cols); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := st.AddServer("10002", map[string]string{"Desc": "x"}); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	if err := st.DeleteServer("10002"); err != nil {
		t.Fatalf("DeleteServer: %v", err)
	}
	if _, err := st.GetServer("10002"); err == nil {
		t.Error("删除后应查不到")
	}
	// 幂等:删不存在的行不报错
	if err := st.DeleteServer("99999"); err != nil {
		t.Errorf("删不存在应幂等, got %v", err)
	}
}

func TestUpdateServersBulk(t *testing.T) {
	st := newMemStore(t)
	cols := []Column{
		{Ordinal: 1, Name: "Id", GameType: "INT"},
		{Ordinal: 2, Name: "WorldName", GameType: "STRING", Row3Tag: "server"},
		{Ordinal: 3, Name: "PortForClient", GameType: "INT", Row3Tag: "server"},
	}
	if err := st.EnsureSchema(cols); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := st.AddServer("10001", map[string]string{"WorldName": "A", "PortForClient": "100"}); err != nil {
		t.Fatalf("add 10001: %v", err)
	}
	if err := st.AddServer("10002", map[string]string{"WorldName": "B", "PortForClient": "200"}); err != nil {
		t.Fatalf("add 10002: %v", err)
	}

	err := st.UpdateServers(map[string]map[string]string{
		"10001": {"WorldName": "A2"},
		"10002": {"PortForClient": "222", "WorldName": "B2"},
	})
	if err != nil {
		t.Fatalf("UpdateServers: %v", err)
	}

	r1, _ := st.GetServer("10001")
	if r1["WorldName"] != "A2" || r1["PortForClient"] != "100" {
		t.Errorf("10001 = %v, want WorldName=A2 PortForClient=100(未改)", r1)
	}
	r2, _ := st.GetServer("10002")
	if r2["WorldName"] != "B2" || r2["PortForClient"] != "222" {
		t.Errorf("10002 = %v, want WorldName=B2 PortForClient=222", r2)
	}
}
