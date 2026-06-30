package mergepreview

import (
	"strings"
)

// GenerateFile 还原表头4行 + 数据行(保持传入顺序,即源文件原始行序;按表头列序),整体转 GBK。
// 行序由调用方决定(AllRows 按 seq 升序=源文件顺序,新追加的接末尾)。
func GenerateFile(meta Meta, rows []Row) ([]byte, error) {
	names := strings.Split(meta.NamesLine, "\t")

	var b strings.Builder
	for _, h := range []string{meta.NamesLine, meta.TypesLine, meta.Row3Line, meta.Row4Line} {
		b.WriteString(h)
		b.WriteByte('\n')
	}
	for _, r := range rows {
		f := r.fields()
		cells := make([]string, len(names))
		for i, name := range names {
			cells[i] = f[strings.TrimSpace(name)] // 未知列名→空(本表固定9列不会发生)
		}
		b.WriteString(strings.Join(cells, "\t"))
		b.WriteByte('\n')
	}
	return encodeGBK(b.String())
}
