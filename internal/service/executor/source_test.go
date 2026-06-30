package executor

import (
	"testing"

	"gongdan/internal/gameserver"
)

// staticSource 内存实现 gameserver.Source。
type staticSource struct {
	servers []gameserver.Server
	conns   map[int]gameserver.DBConn
}

func (s staticSource) Servers() ([]gameserver.Server, error)       { return s.servers, nil }
func (s staticSource) DBConns() (map[int]gameserver.DBConn, error) { return s.conns, nil }

// fileSource 把现有临时配置文件用 gameserver 解析包成 Source,
// 行为与原 release/hotupdate 读文件完全一致(迁移测试用)。
func fileSource(t *testing.T, path string) gameserver.Source {
	t.Helper()
	servers, err := gameserver.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile(%s): %v", path, err)
	}
	conns, err := gameserver.BuildDBConnMap(path)
	if err != nil {
		t.Fatalf("BuildDBConnMap(%s): %v", path, err)
	}
	return staticSource{servers: servers, conns: conns}
}
