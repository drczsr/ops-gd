package mergepreview

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func TestGenerateFile(t *testing.T) {
	meta, rows, err := ParseImport(importTSV)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 调换顺序,验证生成时保持传入顺序(源文件顺序),不按 Id 重排
	rows = []Row{rows[1], rows[0]} // [10060, 10054]
	out, err := GenerateFile(meta, rows)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// GBK 回解码
	dec, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), out)
	if err != nil {
		t.Fatalf("回解码: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(dec), "\n"), "\n")
	if lines[0] != meta.NamesLine || lines[1] != meta.TypesLine || lines[2] != meta.Row3Line || lines[3] != meta.Row4Line {
		t.Errorf("表头4行未原样还原")
	}
	// 保持传入顺序(不按 Id 重排):10060 在 10054 前
	if !strings.HasPrefix(lines[4], "10060\t") || !strings.HasPrefix(lines[5], "10054\t") {
		t.Errorf("数据行未保持传入顺序: %q / %q", lines[4], lines[5])
	}
	// 列序与表头一致、含中文
	if !strings.Contains(lines[5], "合服目标服") {
		t.Errorf("缺 Name 列内容: %q", lines[5])
	}
}
