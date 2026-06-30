package gsconfig

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 投影测试用的合成夹具:Id(框架列) / Desc(空标记,两端都丢) /
// WorldName(server) / ServerShowName(client) / Tag(空标记,两端都留)。
func projFixture() ([]Column, Meta, []ServerRow) {
	cols := []Column{
		{Ordinal: 1, Name: "Id", GameType: "INT", Row3Tag: ""},
		{Ordinal: 2, Name: "Desc", GameType: "STRING", Row3Tag: ""},
		{Ordinal: 3, Name: "WorldName", GameType: "STRING", Row3Tag: "server"},
		{Ordinal: 4, Name: "ServerShowName", GameType: "STRING", Row3Tag: "client"},
		{Ordinal: 5, Name: "Tag", GameType: "STRING", Row3Tag: ""},
	}
	meta := Meta{HeaderCol1Directive: "MAX_ID=9;MAX_RECORD=10;", Row4Line: "源表第4行(应被忽略)", Encoding: "GBK"}
	rows := []ServerRow{
		{"Id": "10", "Desc": "d10", "WorldName": "w10", "ServerShowName": "s10", "Tag": "t10"},
		{"Id": "2", "Desc": "d2", "WorldName": "w2", "ServerShowName": "s2", "Tag": "t2"},
	}
	return cols, meta, rows
}

// 导出(all)的文件必须能被导入(Parse)无损解析回来——导入/导出格式一致。
func TestGenerateAllReimportLossless(t *testing.T) {
	cols, meta, rows := projFixture()
	out, err := GenerateFor(cols, meta, rows, "all")
	if err != nil {
		t.Fatalf("GenerateFor(all): %v", err)
	}
	cols2, _, rows2, err := Parse(out)
	if err != nil {
		t.Fatalf("导出的文件应能再导入: %v", err)
	}
	if len(cols2) != len(cols) {
		t.Errorf("列数变了: %d -> %d(导入导出应一致)", len(cols), len(cols2))
	}
	if len(rows2) != len(rows) {
		t.Errorf("行数变了: %d -> %d", len(rows), len(rows2))
	}
	byID := map[string]ServerRow{}
	for _, r := range rows2 {
		byID[r["Id"]] = r
	}
	// client 列(ServerShowName)、空标记列(Desc)在 all 导出里也保留(server 投影会丢)
	if byID["10"]["ServerShowName"] != "s10" || byID["10"]["Desc"] != "d10" || byID["10"]["WorldName"] != "w10" {
		t.Errorf("all 导出应保留全部列的值, got %+v", byID["10"])
	}
}

func TestGenerateForServer(t *testing.T) {
	cols, meta, rows := projFixture()
	out, err := GenerateFor(cols, meta, rows, "server")
	if err != nil {
		t.Fatalf("GenerateFor: %v", err)
	}
	if !utf8.Valid(out) {
		t.Fatal("输出应为合法 UTF-8")
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	// 选列:Id + WorldName(server) + Tag;丢 Desc、ServerShowName
	if lines[0] != "Id\tWorldName\tTag" {
		t.Errorf("列名行错: %q", lines[0])
	}
	if lines[1] != "INT\tSTRING\tSTRING" {
		t.Errorf("类型行错: %q", lines[1])
	}
	// 第3行:第1列=指令,其余=各列 Row3Tag(Tag 列为空)
	if lines[2] != "MAX_ID=9;MAX_RECORD=10;\tserver\t" {
		t.Errorf("标记行错: %q", lines[2])
	}
	// 第4行:# + (列数-1) 个 tab
	if lines[3] != "#\t\t" {
		t.Errorf("第4行应清成#+空列: %q", lines[3])
	}
	// 数据行按 Id 升序:2 在 10 前
	if lines[4] != "2\tw2\tt2" || lines[5] != "10\tw10\tt10" {
		t.Errorf("数据行错: %q / %q", lines[4], lines[5])
	}
}

func TestGenerateForClient(t *testing.T) {
	cols, meta, rows := projFixture()
	out, err := GenerateFor(cols, meta, rows, "client")
	if err != nil {
		t.Fatalf("GenerateFor: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	// 选列:Id + ServerShowName(client) + Tag
	if lines[0] != "Id\tServerShowName\tTag" {
		t.Errorf("client 列名行错: %q", lines[0])
	}
	if lines[3] != "#\t\t" {
		t.Errorf("client 第4行错: %q", lines[3])
	}
	if lines[4] != "2\ts2\tt2" || lines[5] != "10\ts10\tt10" {
		t.Errorf("client 数据行错: %q / %q", lines[4], lines[5])
	}
}

func TestGenerateForDropsDesc(t *testing.T) {
	cols, meta, rows := projFixture()
	for _, tag := range []string{"server", "client"} {
		out, err := GenerateFor(cols, meta, rows, tag)
		if err != nil {
			t.Fatalf("%s GenerateFor: %v", tag, err)
		}
		if strings.Contains(string(out), "Desc") || strings.Contains(string(out), "d10") {
			t.Errorf("%s 文件不应含 Desc 列或其数据", tag)
		}
	}
}
