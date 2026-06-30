package gsconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

type stubUploader struct {
	uploads   map[string][]byte
	err       error
	failOnHit int // 0 = 有 err 则每次都失败;>0 = 仅第 N 次调用返回 err
	hits      int
}

func (s *stubUploader) Upload(key string, data []byte) (string, error) {
	s.hits++
	if s.uploads == nil {
		s.uploads = map[string][]byte{}
	}
	s.uploads[key] = data
	if s.err != nil && (s.failOnHit == 0 || s.hits == s.failOnHit) {
		return "", s.err
	}
	return "", nil
}

func TestGenerateAndPublishUploadsBoth(t *testing.T) {
	st := importedStore(t)
	up := &stubUploader{}
	svc := NewService(st, up, "server/ServerConfigList.txt", "client/ServerConfigList.txt", "", "")

	if err := svc.GenerateAndPublish(); err != nil {
		t.Fatalf("GenerateAndPublish: %v", err)
	}
	if up.hits != 2 {
		t.Fatalf("应上传两次, got %d", up.hits)
	}
	srv, ok := up.uploads["server/ServerConfigList.txt"]
	if !ok {
		t.Fatal("缺 server 上传")
	}
	cli, ok := up.uploads["client/ServerConfigList.txt"]
	if !ok {
		t.Fatal("缺 client 上传")
	}
	// sample 夹具 5 列全是 server(无 client 列、无 Tag 列):
	// server 文件含 SelfPublicIp 列;client 文件只剩 Id 列。
	if !strings.Contains(strings.SplitN(string(srv), "\n", 2)[0], "SelfPublicIp") {
		t.Errorf("server 文件应含 SelfPublicIp 列, header=%q", strings.SplitN(string(srv), "\n", 2)[0])
	}
	if strings.SplitN(string(cli), "\n", 2)[0] != "Id" {
		t.Errorf("client 文件应只剩 Id 列, header=%q", strings.SplitN(string(cli), "\n", 2)[0])
	}
}

func TestGenerateAndPublishReturnsUploadError(t *testing.T) {
	st := importedStore(t)
	up := &stubUploader{err: errors.New("cos boom")}
	svc := NewService(st, up, "sk", "ck", "", "")
	if err := svc.GenerateAndPublish(); err == nil {
		t.Error("上传失败应返回错误")
	}
}

func TestGenerateAndPublishClientUploadError(t *testing.T) {
	st := importedStore(t)
	up := &stubUploader{err: errors.New("client boom"), failOnHit: 2}
	svc := NewService(st, up, "sk", "ck", "", "")
	err := svc.GenerateAndPublish()
	if err == nil {
		t.Fatal("client 上传失败应返回错误")
	}
	if !strings.Contains(err.Error(), "server 已上传") {
		t.Errorf("错误应提示 server 已上传, got %v", err)
	}
	if up.hits != 2 {
		t.Errorf("server 应已上传(共2次调用), got hits=%d", up.hits)
	}
}

func TestGenerateServerFile(t *testing.T) {
	st := importedStore(t)
	svc := NewService(st, &stubUploader{}, "server/x", "client/x", "", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "scl.txt")
	if err := svc.GenerateServerFile(path); err != nil {
		t.Fatalf("err: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		t.Fatalf("应写出非空文件: %v", err)
	}
}

func TestGenerateFullGBKBytes(t *testing.T) {
	st := importedStore(t)
	svc := NewService(st, &stubUploader{}, "server/ServerConfigList.txt", "client/ServerConfigList.txt", "", "")

	data, err := svc.GenerateFullGBKBytes()
	if err != nil {
		t.Fatalf("GenerateFullGBKBytes: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("导出内容不应为空")
	}
	// sample 含中文,GBK 字节不应是合法 UTF-8。
	if utf8.Valid(data) {
		t.Fatal("GBK 导出不应是 UTF-8 字节")
	}
	cols, _, rows, err := Parse(data)
	if err != nil {
		t.Fatalf("GBK 导出应可被 Parse: %v", err)
	}
	if len(cols) == 0 || len(rows) == 0 {
		t.Fatalf("导出解析后不应为空, cols=%d rows=%d", len(cols), len(rows))
	}
}
