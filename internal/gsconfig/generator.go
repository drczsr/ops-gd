package gsconfig

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func idInt(r ServerRow) int {
	n, _ := strconv.Atoi(strings.TrimSpace(r["Id"]))
	return n
}

// GenerateFor 按 server/client 标记把配置库内容投影为部署文件(UTF-8)。
// tag 取 "server" / "client"(按 Row3Tag 取舍,始终保留 Id 与 Tag 列),或 "all"(保留全部列,
// 用于 Web 导出:与导入的原始 ServerConfigList.txt 列集一致、可无损再导入)。
// 第1/2/3行投影表头(第3行首列写 meta.HeaderCol1Directive);第4行清成 #+空列;
// 数据行按 Id 数值升序;制表符分隔、\n 行尾、末尾一个换行。
func GenerateFor(cols []Column, meta Meta, rows []ServerRow, tag string) ([]byte, error) {
	ordered := make([]Column, len(cols))
	copy(ordered, cols)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Ordinal < ordered[j].Ordinal })

	kept := make([]Column, 0, len(ordered))
	for _, c := range ordered {
		if tag == "all" || c.Name == "Id" || c.Name == "Tag" || strings.TrimSpace(c.Row3Tag) == tag {
			kept = append(kept, c)
		}
	}

	if len(kept) == 0 {
		return nil, fmt.Errorf("没有可投影的列(tag=%q)", tag)
	}

	var b strings.Builder
	writeLine := func(fields []string) {
		b.WriteString(strings.Join(fields, "\t"))
		b.WriteByte('\n')
	}

	names := make([]string, len(kept))
	types := make([]string, len(kept))
	row3 := make([]string, len(kept))
	for i, c := range kept {
		names[i] = c.Name
		types[i] = c.GameType
		if i == 0 {
			row3[i] = meta.HeaderCol1Directive
		} else {
			row3[i] = c.Row3Tag
		}
	}
	writeLine(names)
	writeLine(types)
	writeLine(row3)
	// 第4行:# + (列数-1) 个 tab(去掉源表中文注释)
	b.WriteString("#" + strings.Repeat("\t", len(kept)-1) + "\n")

	sorted := make([]ServerRow, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool { return idInt(sorted[i]) < idInt(sorted[j]) })
	for _, r := range sorted {
		fields := make([]string, len(kept))
		for i, c := range kept {
			fields[i] = r[c.Name]
		}
		writeLine(fields)
	}

	return []byte(b.String()), nil
}
