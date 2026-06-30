package executor

import (
	"strings"
	"testing"

	"gongdan/internal/model"
)

func rollbackParams() model.NewServerCreateParams {
	return model.NewServerCreateParams{
		RegionName: "洛阳",
		Rows: []model.NewServerRow{
			{Kind: "game", ID: 10002, DataBaseUser: "root", DataBasePsw: "pw", MySqlIp: "g-rds",
				MySqlPort: "3306", SelfPublicIp: "10.0.0.1",
				Fields: map[string]string{"Id": "10002", "DataBaseName": "ptdb_10002"}},
			{Kind: "battle", ID: 12802, DataBaseUser: "root", DataBasePsw: "pw", MySqlIp: "b-rds",
				MySqlPort: "3306", SelfPublicIp: "10.1.0.2",
				Fields: map[string]string{"Id": "12802", "DataBaseName": "ptdb_12802"}},
		},
	}
}

func TestDeleteParamsFromRowRemoveALBGatedByPublicDomain(t *testing.T) {
	rowWithDomain := model.NewServerRow{
		Kind:         "game",
		ID:           10002,
		SelfPublicIp: "10.0.0.1",
		Fields:       map[string]string{"RealSelfPublicUrl": "wss://gs-tw.example/s10002"},
	}
	if !deleteParamsFromRow(rowWithDomain).RemoveALB {
		t.Fatal("expected RemoveALB=true for wss domain row")
	}

	rowWithIP := model.NewServerRow{
		Kind:         "game",
		ID:           10003,
		SelfPublicIp: "10.0.0.2",
		Fields:       map[string]string{"RealSelfPublicUrl": "1.1.1.1"},
	}
	if deleteParamsFromRow(rowWithIP).RemoveALB {
		t.Fatal("expected RemoveALB=false for non-domain row")
	}
}

func TestRollbackNewServerHappyPath(t *testing.T) {
	del := &fakeDeleter{}
	published := 0
	var cmds []string
	e := &RollbackNewServerExecutor{
		deleter: del,
		publish: func(log LogFunc) error { published++; return nil },
		runSSH: func(ip, cmd string, log LogFunc) (string, error) {
			cmds = append(cmds, cmd)
			return "", nil
		},
		scriptsDir: "/scripts",
	}
	wo := &model.WorkOrder{ID: 1, Type: model.TypeNewServer}
	wo.Params, _ = model.MarshalParams(rollbackParams())
	if err := e.Execute(wo, func(string) {}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(del.deleted) != 2 {
		t.Errorf("应删 2 个配置行, got %v", del.deleted)
	}
	if published != 1 {
		t.Errorf("serverlist 应只重生一次, got %d", published)
	}
	var dbCmds int
	for _, c := range cmds {
		lc := strings.ToLower(c)
		if strings.Contains(lc, "drop") || strings.Contains(lc, "ptdb_") {
			dbCmds++
		}
	}
	if dbCmds < 2 {
		t.Errorf("应对 2 台丢库, 命中 drop/库名 的命令数=%d, cmds=%v", dbCmds, cmds)
	}
}

func TestRollbackNewServerPartialFailureContinues(t *testing.T) {
	del := &fakeDeleter{}
	e := &RollbackNewServerExecutor{
		deleter: del,
		publish: func(log LogFunc) error { return nil },
		runSSH: func(ip, cmd string, log LogFunc) (string, error) {
			lc := strings.ToLower(cmd)
			if ip == "10.0.0.1" && (strings.Contains(lc, "drop") || strings.Contains(lc, "ptdb_10002")) {
				return "", &deployErr{} // 10002 丢库失败
			}
			return "", nil
		},
		scriptsDir: "/scripts",
	}
	wo := &model.WorkOrder{ID: 1, Type: model.TypeNewServer}
	wo.Params, _ = model.MarshalParams(rollbackParams())
	err := e.Execute(wo, func(string) {})
	if err == nil {
		t.Fatal("有台未清净应返回错误")
	}
	if len(del.deleted) != 1 || del.deleted[0] != "12802" {
		t.Errorf("失败台不删配置行、成功台应删, got %v", del.deleted)
	}
}
