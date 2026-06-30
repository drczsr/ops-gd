package executor

import (
	"fmt"
	"strings"
	"testing"

	"gongdan/internal/model"
)

type fakeDeleter struct{ deleted []string }

func (f *fakeDeleter) DeleteServer(id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func delWO(t *testing.T, p model.DeleteServerParams) *model.WorkOrder {
	s, err := model.MarshalParams(p)
	if err != nil {
		t.Fatal(err)
	}
	return &model.WorkOrder{ID: 1, Type: model.TypeDeleteServer, Params: s}
}

func gameDelParams() model.DeleteServerParams {
	return model.DeleteServerParams{
		ID: 10002, Kind: "game", ServerName: "洛阳", SelfPublicIp: "10.0.0.1",
		DataBaseName: "ptdb_10002", MySqlIp: "g-rds", MySqlPort: "3306",
		DataBaseUser: "root", DataBasePsw: "pw",
		Fields:    map[string]string{"Id": "10002", "WorldType": "0"},
		RemoveALB: true, RegenServerlist: true,
	}
}

func TestDeleteServerExecutorHappyPath(t *testing.T) {
	del := &fakeDeleter{}
	published := 0
	var cmds []string
	e := &DeleteServerExecutor{
		deleter:    del,
		publish:    func(log LogFunc) error { published++; return nil },
		runSSH:     func(ip, cmd string, log LogFunc) (string, error) { cmds = append(cmds, cmd); return "", nil },
		scriptsDir: "/scripts",
		alb:        nil, // ALB 未启用
	}
	if err := e.Execute(delWO(t, gameDelParams()), func(string) {}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	joined := strings.Join(cmds, "\n")
	iStop := strings.Index(joined, "gameserver_stop")
	iDrop := strings.Index(joined, "DROP DATABASE IF EXISTS ptdb_10002")
	iRm := strings.Index(joined, "rm -rf /export/server/server_10002")
	if iStop < 0 || iDrop < 0 || iRm < 0 {
		t.Fatalf("缺少步骤命令: %s", joined)
	}
	if !(iStop < iDrop && iDrop < iRm) {
		t.Errorf("步骤顺序应为 停→丢库→删包: %s", joined)
	}
	if len(del.deleted) != 1 || del.deleted[0] != "10002" {
		t.Errorf("应删配置行 10002, got %v", del.deleted)
	}
	if published != 1 {
		t.Errorf("应重生 serverlist 一次, got %d", published)
	}
}

func TestDeleteServerExecutorKillFallback(t *testing.T) {
	del := &fakeDeleter{}
	var cmds []string
	e := &DeleteServerExecutor{
		deleter: del,
		publish: func(log LogFunc) error { return nil },
		runSSH: func(ip, cmd string, log LogFunc) (string, error) {
			cmds = append(cmds, cmd)
			if strings.Contains(cmd, "gameserver_stop") {
				return "", fmt.Errorf("boom") // 优雅停服失败
			}
			return "", nil
		},
		scriptsDir: "/scripts",
	}
	if err := e.Execute(delWO(t, gameDelParams()), func(string) {}); err != nil {
		t.Fatalf("Execute 不应因停服失败而中断: %v", err)
	}
	if !strings.Contains(strings.Join(cmds, "\n"), "kill.sh 10002") {
		t.Error("优雅停服失败应改用 kill.sh 兜底")
	}
}
