package mergepreview

import (
	"fmt"
	"strconv"
	"strings"
)

// 9 个必需列名(顺序无关,但都必须存在)。
var requiredCols = []string{
	"Id", "Name", "PreviewOpenTime", "PreviewDuration", "CompensationOpenTime",
	"CompensationDurationTime", "IsCompensationItem", "IsGrandGift", "MiaoShu",
}

// ParseImport 解析整份 TSV:前4行作表头存入 Meta(原样),数据行按列名映射成 Row。
// 调用前应先 Validate(结构合法)。
func ParseImport(text string) (Meta, []Row, error) {
	lines := splitLines(text)
	if len(lines) < 5 {
		return Meta{}, nil, fmt.Errorf("至少4行头部+1行数据")
	}
	meta := Meta{
		NamesLine: lines[0],
		TypesLine: lines[1],
		Row3Line:  lines[2],
		Row4Line:  lines[3],
	}
	names := strings.Split(lines[0], "\t")
	idx := map[string]int{}
	for i, n := range names {
		idx[strings.TrimSpace(n)] = i
	}
	for _, c := range requiredCols {
		if _, ok := idx[c]; !ok {
			return Meta{}, nil, fmt.Errorf("缺列 %q", c)
		}
	}

	atoi := func(fields []string, col string) (int, error) {
		v := strings.TrimSpace(fields[idx[col]])
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("列 %s 值 %q 不是整数", col, v)
		}
		return n, nil
	}

	var rows []Row
	for i := 4; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != len(names) {
			return Meta{}, nil, fmt.Errorf("第 %d 行列数 %d 与表头 %d 不一致", i+1, len(f), len(names))
		}
		var r Row
		var err error
		if r.ID, err = atoi(f, "Id"); err != nil {
			return Meta{}, nil, fmt.Errorf("第 %d 行: %w", i+1, err)
		}
		r.Name = strings.TrimSpace(f[idx["Name"]])
		for _, c := range []struct {
			col string
			dst *int
		}{
			{"PreviewOpenTime", &r.PreviewOpenTime},
			{"PreviewDuration", &r.PreviewDuration},
			{"CompensationOpenTime", &r.CompensationOpenTime},
			{"CompensationDurationTime", &r.CompensationDurationTime},
			{"IsCompensationItem", &r.IsCompensationItem},
			{"IsGrandGift", &r.IsGrandGift},
			{"MiaoShu", &r.MiaoShu},
		} {
			if *c.dst, err = atoi(f, c.col); err != nil {
				return Meta{}, nil, fmt.Errorf("第 %d 行: %w", i+1, err)
			}
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return Meta{}, nil, fmt.Errorf("没有数据行")
	}
	return meta, rows, nil
}
