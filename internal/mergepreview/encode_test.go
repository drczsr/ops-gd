package mergepreview

import (
	"bytes"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func TestEncodeGBK(t *testing.T) {
	in := "10054\t合服目标服\t10\n"
	out, err := encodeGBK(in)
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	back, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), out)
	if err != nil {
		t.Fatalf("回解码失败: %v", err)
	}
	if string(back) != in {
		t.Errorf("往返不一致: %q != %q", back, in)
	}
	if bytes.Equal(out, []byte(in)) {
		t.Errorf("中文未被转成 GBK")
	}
}

func TestDecodeGBK(t *testing.T) {
	in := "10054\t合服目标服\t10\n"
	gbk, err := encodeGBK(in)
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	got, err := DecodeGBK(gbk)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if got != in {
		t.Errorf("GBK 往返不一致: %q != %q", got, in)
	}
}

func TestDecodeAuto(t *testing.T) {
	in := "10054\t锡壕清镜\t10\n"
	// UTF-8 原样
	if got, err := DecodeAuto([]byte(in)); err != nil || got != in {
		t.Errorf("UTF-8: got=%q err=%v", got, err)
	}
	// 带 BOM 的 UTF-8
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte(in)...)
	if got, err := DecodeAuto(withBOM); err != nil || got != in {
		t.Errorf("UTF-8+BOM: got=%q err=%v", got, err)
	}
	// GBK 字节
	gbk, _ := encodeGBK(in)
	if got, err := DecodeAuto(gbk); err != nil || got != in {
		t.Errorf("GBK: got=%q err=%v", got, err)
	}
}
