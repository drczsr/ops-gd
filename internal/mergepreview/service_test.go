package mergepreview

import "testing"

func TestServiceAppendRows(t *testing.T) {
	st := newTestStore(t)
	s := NewService(st)
	_ = s.ImportOverwrite(importTSV) // 含 10054/10060
	conflicts, err := s.AppendRows([]Row{NewRow(10054, "dup", 3, 13, 1, 0), NewRow(30001, "新服", 3, 13, 2, 1)})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0] != 10054 {
		t.Errorf("应报10054冲突, got %v", conflicts)
	}
	if n, _ := s.Count(""); n != 3 {
		t.Errorf("应3行, got %d", n)
	}
}

func TestServiceGenerateBytes(t *testing.T) {
	st := newTestStore(t)
	s := NewService(st)
	// 空库(无表头)应报错
	if _, err := s.GenerateBytes(); err == nil {
		t.Error("空库生成应报错")
	}
	// 导入后可生成非空全量文件
	if err := s.ImportOverwrite(importTSV); err != nil {
		t.Fatalf("导入: %v", err)
	}
	data, err := s.GenerateBytes()
	if err != nil {
		t.Fatalf("生成: %v", err)
	}
	if len(data) == 0 {
		t.Error("生成内容不应为空")
	}
}
