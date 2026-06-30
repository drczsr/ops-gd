package gsconfig

import "testing"

func TestDecodeGBKFromBytes(t *testing.T) {
	// "中" 的 GBK(GB2312)编码为 0xD6 0xD0
	out, err := decodeGBK([]byte{0xD6, 0xD0})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(out) != "中" {
		t.Errorf("GBK 解码错: got %q want 中", out)
	}
}

func TestDecodeGBKPassesThroughUTF8(t *testing.T) {
	in := []byte("Id\tWorldName")
	out, err := decodeGBK(in)
	if err != nil || string(out) != string(in) {
		t.Errorf("合法 UTF-8 应原样返回, got %q err %v", out, err)
	}
}

func TestEncodeGBK(t *testing.T) {
	in := []byte("测试")
	gbk, err := encodeGBK(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(gbk) == string(in) {
		t.Fatalf("GBK 字节应与 UTF-8 不同")
	}
	out, err := decodeGBK(gbk)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(out) != "测试" {
		t.Errorf("roundtrip 错误: got %q", out)
	}
}
