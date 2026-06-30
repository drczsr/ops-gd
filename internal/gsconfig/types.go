// Package gsconfig 把游戏服配置文件(ServerConfigList.txt)建模进数据库,
// 并能从数据库还原出同格式(54 列 / 制表符 / GBK / 4 行头部)的文件字节。
// 核心包不依赖 MySQL / COS:Store 用驱动无关的 *gorm.DB(测试用内存 SQLite),
// 上传走 Uploader 接口(实现在子包 cos)。
package gsconfig

import (
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// Column 一列的表头元信息(对应文件第 1~3 行)。
type Column struct {
	Ordinal  int    // 列序号 1..N,决定生成时列顺序
	Name     string // 第 1 行列名
	GameType string // 第 2 行:INT / STRING
	Row3Tag  string // 第 3 行该列标记(第 1 列除外,见 Meta.HeaderCol1Directive)
}

// Meta 全局头部信息。
type Meta struct {
	HeaderCol1Directive string // 第 3 行第 1 列:MAX_ID=...;MAX_RECORD=...;...
	MaxID               int    // 从上串解析
	MaxRecord           int    // 从上串解析
	Row4Line            string // 第 4 行原样(已解码为 UTF-8)
	Encoding            string // 固定 "GBK"
}

// ServerRow 一个服的数据:列名 -> 文本值。
type ServerRow = map[string]string

// decodeGBK:若已是合法 UTF-8 原样返回,否则按 GBK 解码为 UTF-8。
func decodeGBK(raw []byte) ([]byte, error) {
	if utf8.Valid(raw) {
		return raw, nil
	}
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), raw)
	return out, err
}

// encodeGBK:把 UTF-8 字节编码为 GBK。
func encodeGBK(raw []byte) ([]byte, error) {
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), raw)
	return out, err
}
