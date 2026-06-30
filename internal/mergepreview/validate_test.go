package mergepreview

import "testing"

const validTSV = "Id\tName\tPreviewOpenTime\n" +
	"INT\tSTRING\tINT\n" +
	"MAX_ID=59999;MAX_RECORD=60000;\t\tserver\n" +
	"#\t\t\n" +
	"10054\t合服目标服\t10\n"

func TestValidateOK(t *testing.T) {
	if err := Validate(validTSV); err != nil {
		t.Fatalf("合法 TSV 应通过: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]string{
		"空内容":      "",
		"无数据行":     "Id\tName\nINT\tSTRING\nx\t\n#\t\n",
		"首列非Id":    "Xx\tName\tPreviewOpenTime\nINT\tSTRING\tINT\nm\t\tserver\n#\t\t\n10054\t合服目标服\t10\n",
		"类型行列数不符": "Id\tName\tPreviewOpenTime\nINT\tSTRING\nm\t\tserver\n#\t\t\n10054\t合服目标服\t10\n",
		"数据行列数不符": "Id\tName\tPreviewOpenTime\nINT\tSTRING\tINT\nm\t\tserver\n#\t\t\n10054\t合服目标服\n",
	}
	for name, txt := range cases {
		if err := Validate(txt); err == nil {
			t.Errorf("[%s] 应报错但通过了", name)
		}
	}
}
