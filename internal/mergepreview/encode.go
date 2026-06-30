package mergepreview

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// encodeGBK 把 UTF-8 文本转成 GBK 字节(游戏配置文件编码)。
func encodeGBK(text string) ([]byte, error) {
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(text))
	if err != nil {
		return nil, fmt.Errorf("GBK 编码: %w", err)
	}
	return out, nil
}

// DecodeGBK 把 GBK 字节(磁盘上的游戏配置文件)解成 UTF-8 文本。
func DecodeGBK(data []byte) (string, error) {
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), data)
	if err != nil {
		return "", fmt.Errorf("GBK 解码: %w", err)
	}
	return string(out), nil
}

// DecodeAuto 自动判编码:去 UTF-8 BOM;若整体是合法 UTF-8 则直接用,否则按 GBK 解。
// 兼顾源文件可能是 UTF-8 或 GBK 两种情况(导入用)。
func DecodeAuto(data []byte) (string, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	if utf8.Valid(data) {
		return string(data), nil
	}
	return DecodeGBK(data)
}
