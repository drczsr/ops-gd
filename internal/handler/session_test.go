package handler

import (
	"bytes"
	"testing"
)

func TestSessionSecret(t *testing.T) {
	// 配置非空:原样使用,不标记为生成
	got, gen := sessionSecret("  my-fixed-key  ")
	if gen {
		t.Error("配置了密钥不应标记为随机生成")
	}
	if string(got) != "my-fixed-key" {
		t.Errorf("应去空白后用配置值, got %q", string(got))
	}

	// 配置为空:随机生成 32 字节,且标记 generated
	a, genA := sessionSecret("")
	b, genB := sessionSecret("   ")
	if !genA || !genB {
		t.Error("空配置应标记为随机生成")
	}
	if len(a) != 32 || len(b) != 32 {
		t.Errorf("随机密钥应为 32 字节, got %d/%d", len(a), len(b))
	}
	if bytes.Equal(a, b) {
		t.Error("两次随机生成不应相同")
	}
}
