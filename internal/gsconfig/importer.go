package gsconfig

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	maxIDRe     = regexp.MustCompile(`MAX_ID=(\d+)`)
	maxRecordRe = regexp.MustCompile(`MAX_RECORD=(\d+)`)
)

// Parse 解析配置文件字节(GBK 或 UTF-8),返回列定义、全局元信息、数据行。
// 文件结构:第1行列名 / 第2行类型 / 第3行(第1列指令+其余server) / 第4行#注释 / 第5行起数据。
func Parse(raw []byte) (cols []Column, meta Meta, rows []ServerRow, err error) {
	decoded, err := decodeGBK(raw)
	if err != nil {
		return nil, Meta{}, nil, fmt.Errorf("GBK 解码失败: %w", err)
	}
	text := strings.ReplaceAll(string(decoded), "\r", "")
	lines := strings.Split(text, "\n")
	if len(lines) < 4 {
		return nil, Meta{}, nil, fmt.Errorf("文件不足 4 行头部")
	}

	names := strings.Split(lines[0], "\t")
	types := strings.Split(lines[1], "\t")
	row3 := strings.Split(lines[2], "\t")
	if len(types) != len(names) || len(row3) != len(names) {
		return nil, Meta{}, nil, fmt.Errorf("头部列数不一致: names=%d types=%d row3=%d",
			len(names), len(types), len(row3))
	}

	cols = make([]Column, len(names))
	for i, name := range names {
		tag := row3[i]
		if i == 0 {
			tag = ""
		}
		cols[i] = Column{Ordinal: i + 1, Name: strings.TrimSpace(name), GameType: strings.TrimSpace(types[i]), Row3Tag: tag}
	}

	meta = Meta{
		HeaderCol1Directive: row3[0],
		Row4Line:            lines[3],
		Encoding:            "GBK",
	}
	if m := maxIDRe.FindStringSubmatch(row3[0]); m != nil {
		meta.MaxID, _ = strconv.Atoi(m[1])
	}
	if m := maxRecordRe.FindStringSubmatch(row3[0]); m != nil {
		meta.MaxRecord, _ = strconv.Atoi(m[1])
	}

	for _, line := range lines[4:] {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		row := make(ServerRow, len(cols))
		for i, c := range cols {
			if i < len(fields) {
				row[c.Name] = fields[i]
			} else {
				row[c.Name] = ""
			}
		}
		rows = append(rows, row)
	}
	return cols, meta, rows, nil
}
