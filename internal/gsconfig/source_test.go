package gsconfig

import (
	"testing"

	"gongdan/internal/gameserver"
)

func TestServerSourceMapsByColumnName(t *testing.T) {
	st := newMemStore(t)
	cols := []Column{
		{Ordinal: 1, Name: "Id", GameType: "INT"},
		{Ordinal: 2, Name: "WorldName", GameType: "STRING", Row3Tag: "server"},
		{Ordinal: 3, Name: "WorldType", GameType: "INT", Row3Tag: "server"},
		{Ordinal: 4, Name: "BattleWorldID", GameType: "INT", Row3Tag: "server"},
		{Ordinal: 11, Name: "RealWorldID", GameType: "INT", Row3Tag: "server"},
		{Ordinal: 5, Name: "SelfPublicIp", GameType: "STRING", Row3Tag: "server"},
		{Ordinal: 6, Name: "MySqlIp", GameType: "STRING", Row3Tag: "server"},
		{Ordinal: 7, Name: "MySqlPort", GameType: "INT", Row3Tag: "server"},
		{Ordinal: 8, Name: "DataBaseUser", GameType: "STRING", Row3Tag: "server"},
		{Ordinal: 9, Name: "DataBasePsw", GameType: "STRING", Row3Tag: "server"},
		{Ordinal: 10, Name: "Desc", GameType: "STRING", Row3Tag: ""},
	}
	if err := st.EnsureSchema(cols); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := st.AddServer("10001", map[string]string{
		"Id": "10001", "WorldName": "L1", "Desc": "洛阳1服", "WorldType": "0", "BattleWorldID": "-1",
		"RealWorldID": "10001",
		"SelfPublicIp": "10.0.0.1", "MySqlIp": "10.0.0.9", "MySqlPort": "3306",
		"DataBaseUser": "root", "DataBasePsw": "secret",
	}); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	// 合服废弃服:WorldType=-1、RealWorldID=目标服 10001
	if err := st.AddServer("10005", map[string]string{
		"Id": "10005", "Desc": "drc", "WorldType": "-1", "RealWorldID": "10001",
	}); err != nil {
		t.Fatalf("AddServer merged: %v", err)
	}

	src := NewServerSource(st)
	servers, err := src.Servers()
	if err != nil {
		t.Fatalf("Servers: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("servers=%d, want 2", len(servers))
	}
	var s, merged gameserver.Server
	for _, x := range servers {
		switch x.ID {
		case 10001:
			s = x
		case 10005:
			merged = x
		}
	}
	// Name/Desc 应取 Desc 列「洛阳1服」,而非 WorldName「L1」;RealWorldID 正常服==自身
	if s.ID != 10001 || s.WorldType != 0 || s.BattleWorldID != -1 || s.IP != "10.0.0.1" || s.Name != "洛阳1服" || s.Desc != "洛阳1服" || s.RealWorldID != 10001 {
		t.Errorf("server 映射错: %+v", s)
	}
	// 合服废弃服:WorldType=-1,RealWorldID 指向目标服(供「已合入」归并)
	if merged.WorldType != -1 || merged.RealWorldID != 10001 {
		t.Errorf("废弃服 RealWorldID 应映射为目标服, got %+v", merged)
	}
	conns, err := src.DBConns()
	if err != nil {
		t.Fatalf("DBConns: %v", err)
	}
	c := conns[10001]
	if c.IP != "10.0.0.9" || c.Port != "3306" || c.User != "root" || c.Pwd != "secret" {
		t.Errorf("dbconn 映射错: %+v", c)
	}
}
