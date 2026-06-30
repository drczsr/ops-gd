package mergepreview

import "gongdan/internal/configdiff"

// mergeCols 预告表展示列序(与 Row.fields 一致,Id 为主键列)。
var mergeCols = []string{
	"Id", "Name", "PreviewOpenTime", "PreviewDuration",
	"CompensationOpenTime", "CompensationDurationTime",
	"IsCompensationItem", "IsGrandGift", "MiaoShu",
}

// Tableize 把库行转 configdiff.Table。
func Tableize(rows []Row) configdiff.Table {
	out := make([]map[string]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.fields())
	}
	return configdiff.Table{Cols: mergeCols, Rows: out, KeyCol: "Id"}
}

// FileToTable 把一份 MergeServerFunction.txt 字节(GBK/UTF-8)解析成 Table(线上基准用)。
func FileToTable(raw []byte) (configdiff.Table, error) {
	text, err := DecodeAuto(raw)
	if err != nil {
		return configdiff.Table{}, err
	}
	_, rows, err := ParseImport(text)
	if err != nil {
		return configdiff.Table{}, err
	}
	return Tableize(rows), nil
}
