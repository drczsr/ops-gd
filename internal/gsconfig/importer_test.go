package gsconfig

import (
	"os"
	"testing"
)

func loadSample(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/sample.txt")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	return raw
}

func TestParseSample(t *testing.T) {
	cols, meta, rows, err := Parse(loadSample(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cols) != 5 {
		t.Fatalf("列数 = %d, want 5", len(cols))
	}
	if cols[0].Name != "Id" || cols[1].Name != "WorldName" {
		t.Errorf("列名解析错: %+v", cols[:2])
	}
	if cols[1].GameType != "STRING" || cols[2].GameType != "INT" {
		t.Errorf("类型解析错: %+v", cols[1:3])
	}
	if cols[1].Row3Tag != "server" {
		t.Errorf("row3 tag = %q, want server", cols[1].Row3Tag)
	}
	if meta.MaxID != 59999 || meta.MaxRecord != 60000 {
		t.Errorf("meta = %+v, want MaxID 59999 MaxRecord 60000", meta)
	}
	if meta.HeaderCol1Directive != "MAX_ID=59999;MAX_RECORD=60000;HttpAgent;DBAgent;GMServer;" {
		t.Errorf("directive = %q", meta.HeaderCol1Directive)
	}
	if len(rows) != 3 {
		t.Fatalf("服数 = %d, want 3", len(rows))
	}
	if rows[0]["Id"] != "1" || rows[0]["WorldName"] != "测试一服" || rows[0]["DataBasePsw"] != "pwd1" {
		t.Errorf("数据行解析错: %+v", rows[0])
	}
}
