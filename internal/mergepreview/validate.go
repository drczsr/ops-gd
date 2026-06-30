// Package mergepreview 编排合服预告(MergeServerFunction.txt)的校验、编码、上传与全服推送。
package mergepreview

import (
	"fmt"
	"strings"
)

// Validate 对粘贴的 TSV 做结构校验:
// 至少 4 行头部 + ≥1 行数据;首行首列为 Id;类型行/第3行/每行数据列数与表头一致。
// 任一不过返回错误(用于阻止把坏文件推全服)。
func Validate(text string) error {
	lines := splitLines(text)
	if len(lines) < 5 {
		return fmt.Errorf("至少需要 4 行头部 + 1 行数据,当前 %d 行", len(lines))
	}
	names := strings.Split(lines[0], "\t")
	if len(names) < 2 {
		return fmt.Errorf("列名行至少 2 列")
	}
	if strings.TrimSpace(names[0]) != "Id" {
		return fmt.Errorf("首列必须是 Id,实际 %q", names[0])
	}
	if got := len(strings.Split(lines[1], "\t")); got != len(names) {
		return fmt.Errorf("类型行列数 %d 与表头 %d 不一致", got, len(names))
	}
	if got := len(strings.Split(lines[2], "\t")); got != len(names) {
		return fmt.Errorf("第3行列数 %d 与表头 %d 不一致", got, len(names))
	}
	if got := len(strings.Split(lines[3], "\t")); got != len(names) {
		return fmt.Errorf("第4行列数 %d 与表头 %d 不一致", got, len(names))
	}
	dataRows := 0
	for i := 4; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if got := len(strings.Split(line, "\t")); got != len(names) {
			return fmt.Errorf("第 %d 行数据列数 %d 与表头 %d 不一致", i+1, got, len(names))
		}
		dataRows++
	}
	if dataRows == 0 {
		return fmt.Errorf("没有任何数据行")
	}
	return nil
}

// splitLines 按 \n 切分并去掉每行尾部 \r 与整体尾部空行。
func splitLines(text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	for len(raw) > 0 && strings.TrimSpace(raw[len(raw)-1]) == "" {
		raw = raw[:len(raw)-1]
	}
	return raw
}
