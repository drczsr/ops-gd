package mergepreview

import "testing"

const importTSV = "Id\tName\tPreviewOpenTime\tPreviewDuration\tCompensationOpenTime\tCompensationDurationTime\tIsCompensationItem\tIsGrandGift\tMiaoShu\n" +
	"INT\tSTRING\tINT\tINT\tINT\tINT\tBOOL\tINT\tINT\n" +
	"MAX_ID=59999;MAX_RECORD=60000;\t\t\tserver\t\t\t\t\t\n" +
	"#\t\t\t\t\t\t\t\t\n" +
	"10054\t合服目标服\t10\t3\t13\t2\t0\t1\t945018\n" +
	"10060\t合服原服\t10\t3\t13\t1\t1\t1\t945018\n"

func TestParseImport(t *testing.T) {
	meta, rows, err := ParseImport(importTSV)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if meta.NamesLine != "Id\tName\tPreviewOpenTime\tPreviewDuration\tCompensationOpenTime\tCompensationDurationTime\tIsCompensationItem\tIsGrandGift\tMiaoShu" {
		t.Errorf("NamesLine 未原样保留: %q", meta.NamesLine)
	}
	if meta.Row4Line != "#\t\t\t\t\t\t\t\t" {
		t.Errorf("Row4Line 错: %q", meta.Row4Line)
	}
	if len(rows) != 2 {
		t.Fatalf("应解析2行, got %d", len(rows))
	}
	if rows[0].ID != 10054 || rows[0].Name != "合服目标服" || rows[0].CompensationDurationTime != 2 || rows[0].IsCompensationItem != 0 || rows[0].MiaoShu != 945018 {
		t.Errorf("第1行映射错: %+v", rows[0])
	}
	if rows[1].ID != 10060 || rows[1].CompensationDurationTime != 1 || rows[1].IsCompensationItem != 1 {
		t.Errorf("第2行映射错: %+v", rows[1])
	}
}

func TestParseImportRejects(t *testing.T) {
	// 缺 MiaoShu 列
	bad := "Id\tName\tPreviewOpenTime\tPreviewDuration\tCompensationOpenTime\tCompensationDurationTime\tIsCompensationItem\tIsGrandGift\n" +
		"INT\tSTRING\tINT\tINT\tINT\tINT\tBOOL\tINT\n" +
		"x\t\t\t\t\t\t\t\n" +
		"#\t\t\t\t\t\t\t\n" +
		"10054\t合服目标服\t10\t3\t13\t2\t0\t1\n"
	if _, _, err := ParseImport(bad); err == nil {
		t.Error("缺列应报错")
	}
	// 非整数
	bad2 := "Id\tName\tPreviewOpenTime\tPreviewDuration\tCompensationOpenTime\tCompensationDurationTime\tIsCompensationItem\tIsGrandGift\tMiaoShu\n" +
		"INT\tSTRING\tINT\tINT\tINT\tINT\tBOOL\tINT\tINT\n" +
		"x\t\t\t\tserver\t\t\t\t\n" +
		"#\t\t\t\t\t\t\t\t\n" +
		"abc\t合服目标服\t10\t3\t13\t2\t0\t1\t945018\n"
	if _, _, err := ParseImport(bad2); err == nil {
		t.Error("Id 非整数应报错")
	}
}
