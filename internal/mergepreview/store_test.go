package mergepreview

import (
	"testing"

	"gongdan/internal/db"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	gdb, err := db.Init("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("db init: %v", err)
	}
	s := NewStore(gdb)
	if err := s.EnsureSchema(); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	// 清干净(共享内存库可能残留)
	gdb.Exec("DELETE FROM merge_func_rows")
	gdb.Exec("DELETE FROM merge_func_meta")
	return s
}

func TestStoreReplaceAndAll(t *testing.T) {
	s := newTestStore(t)
	meta := Meta{NamesLine: "Id\tName", TypesLine: "INT\tSTRING", Row3Line: "x\t", Row4Line: "#\t"}
	rows := []Row{NewRow(10002, "b", 3, 13, 1, 1), NewRow(10001, "a", 3, 13, 2, 0)}
	if err := s.ReplaceAll(meta, rows); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := s.AllRows()
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	// AllRows 按 seq 升序=导入(传入)顺序:先 10002 后 10001
	if len(got) != 2 || got[0].ID != 10002 || got[1].ID != 10001 {
		t.Errorf("应按导入顺序返回2行, got %+v", got)
	}
	if got[0].PreviewOpenTime != 10 || got[0].MiaoShu != 945018 || got[0].CompensationDurationTime != 1 {
		t.Errorf("固定值/字段错误: %+v", got)
	}
	gm, err := s.GetMeta()
	if err != nil || gm.NamesLine != "Id\tName" {
		t.Errorf("meta 未存对: %+v err=%v", gm, err)
	}
}

func TestStoreReplaceIsOverwrite(t *testing.T) {
	s := newTestStore(t)
	_ = s.ReplaceAll(Meta{NamesLine: "n1"}, []Row{NewRow(1, "a", 3, 13, 1, 0)})
	_ = s.ReplaceAll(Meta{NamesLine: "n2"}, []Row{NewRow(2, "b", 3, 13, 1, 0)})
	got, _ := s.AllRows()
	if len(got) != 1 || got[0].ID != 2 {
		t.Errorf("ReplaceAll 应覆盖, got %+v", got)
	}
	gm, _ := s.GetMeta()
	if gm.NamesLine != "n2" {
		t.Errorf("meta 应被覆盖, got %q", gm.NamesLine)
	}
}

func TestStoreAppendConflict(t *testing.T) {
	s := newTestStore(t)
	_ = s.ReplaceAll(Meta{NamesLine: "n"}, []Row{NewRow(10001, "a", 3, 13, 1, 0)})
	conflicts, err := s.Append([]Row{NewRow(10001, "dup", 3, 13, 2, 1), NewRow(10002, "new", 3, 13, 1, 0)})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0] != 10001 {
		t.Errorf("应报 10001 冲突, got %v", conflicts)
	}
	got, _ := s.AllRows()
	if len(got) != 2 {
		t.Fatalf("应有2行(10001原样+10002新增), got %d", len(got))
	}
	// 10001 不被覆盖
	for _, r := range got {
		if r.ID == 10001 && r.Name != "a" {
			t.Errorf("冲突行不应被覆盖, got name=%s", r.Name)
		}
	}
}

// 新追加的行即使 Id 比已有的小,也按"最新录入"排在列表最前。
func TestStoreListNewestFirstByInsertion(t *testing.T) {
	s := newTestStore(t)
	_ = s.ReplaceAll(Meta{NamesLine: "n"}, []Row{NewRow(50000, "老服", 3, 13, 1, 0)})
	// 追加一个 Id 更小的服
	if _, err := s.Append([]Row{NewRow(10001, "新合的小Id服", 3, 13, 2, 1)}); err != nil {
		t.Fatalf("append: %v", err)
	}
	rows, _ := s.List("", 10, 0)
	if len(rows) != 2 || rows[0].ID != 10001 {
		t.Errorf("最新追加(Id=10001)应排第一,而非按 Id, got %+v", rows)
	}
}

func TestStoreListSearchPaginateDelete(t *testing.T) {
	s := newTestStore(t)
	_ = s.ReplaceAll(Meta{NamesLine: "n"}, []Row{
		NewRow(10001, "大理1服", 3, 13, 1, 0),
		NewRow(10002, "大理2服", 3, 13, 1, 0),
		NewRow(20001, "苏州1服", 3, 13, 1, 0),
	})
	// 按 Name 搜
	rows, _ := s.List("大理", 50, 0)
	if len(rows) != 2 {
		t.Errorf("搜'大理'应2行, got %d", len(rows))
	}
	// 按 Id 搜
	rows, _ = s.List("20001", 50, 0)
	if len(rows) != 1 || rows[0].ID != 20001 {
		t.Errorf("搜'20001'应1行, got %+v", rows)
	}
	// 分页 + 按录入顺序降序(导入顺序 10001<10002<20001,最后导入的 20001 在前)
	page1, _ := s.List("", 2, 0)
	if len(page1) != 2 {
		t.Errorf("limit2 应2行, got %d", len(page1))
	}
	if page1[0].ID != 20001 || page1[1].ID != 10002 {
		t.Errorf("List 应按录入顺序降序, got %d,%d", page1[0].ID, page1[1].ID)
	}
	if n, _ := s.Count(""); n != 3 {
		t.Errorf("Count 应3, got %d", n)
	}
	// 删除
	if err := s.Delete(10001); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n, _ := s.Count(""); n != 2 {
		t.Errorf("删后应2, got %d", n)
	}
}
