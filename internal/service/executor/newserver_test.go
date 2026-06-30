package executor

import (
	"strings"
	"sync"
	"testing"

	"gongdan/internal/model"
)

type fakeCreator struct {
	existing map[string]bool
	added    [][]map[string]string
	mu       sync.Mutex
}

func (f *fakeCreator) AllRows() ([]map[string]string, error) {
	var out []map[string]string
	for id := range f.existing {
		out = append(out, map[string]string{"Id": id})
	}
	return out, nil
}
func (f *fakeCreator) AddServers(rows []map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, rows)
	for _, r := range rows {
		f.existing[r["Id"]] = true
	}
	return nil
}

func newServerWO(t *testing.T, p model.NewServerCreateParams) *model.WorkOrder {
	s, err := model.MarshalParams(p)
	if err != nil {
		t.Fatal(err)
	}
	return &model.WorkOrder{ID: 1, Type: model.TypeNewServer, Params: s}
}

func sampleCreateParams() model.NewServerCreateParams {
	return model.NewServerCreateParams{
		RegionName: "洛阳", VersionPackage: "ProjectT_Release16.0-All_9_1_426_202605281817.zip",
		Rows: []model.NewServerRow{
			{Kind: "battle", ID: 12802, DataBaseUser: "root", DataBasePsw: "pw", MySqlIp: "b-rds",
				MySqlPort: "3306", SelfPublicIp: "10.1.0.2", Fields: map[string]string{"Id": "12802", "WorldType": "2"}},
			{Kind: "copy", ID: 12002, DataBaseUser: "root", DataBasePsw: "pw", MySqlIp: "b-rds",
				MySqlPort: "3306", SelfPublicIp: "10.1.0.2", Fields: map[string]string{"Id": "12002", "WorldType": "3"}},
			{Kind: "game", ID: 10002, DataBaseUser: "root", DataBasePsw: "pw", MySqlIp: "g-rds",
				MySqlPort: "3306", SelfPublicIp: "10.0.0.1", Fields: map[string]string{"Id": "10002", "WorldType": "0"}},
		},
	}
}

func TestShouldSetupALBForRows(t *testing.T) {
	rows := []model.NewServerRow{
		{Kind: "game", ID: 10001, Fields: map[string]string{"RealSelfPublicUrl": "1.1.1.1"}},
		{Kind: "battle", ID: 12801, Fields: map[string]string{"RealSelfPublicUrl": "wss://ws-tw.example/s12801"}},
	}
	if !shouldSetupALBForRows(rows) {
		t.Fatal("expected ALB setup when any row uses wss domain")
	}

	rows = []model.NewServerRow{
		{Kind: "game", ID: 10001, Fields: map[string]string{"RealSelfPublicUrl": "1.1.1.1"}},
		{Kind: "battle", ID: 12801, Fields: map[string]string{"RealSelfPublicUrl": "2.2.2.2"}},
	}
	if shouldSetupALBForRows(rows) {
		t.Fatal("expected skip ALB when no row uses wss domain")
	}
}

func TestNewServerExecutorHappyPath(t *testing.T) {
	creator := &fakeCreator{existing: map[string]bool{}}
	published := 0
	var sshCmds []string
	var mu sync.Mutex
	e := &NewServerExecutor{
		creator: creator,
		publish: func(log LogFunc) error { published++; return nil },
		runSSH: func(ip, cmd string, log LogFunc) (string, error) {
			mu.Lock()
			sshCmds = append(sshCmds, cmd)
			mu.Unlock()
			if strings.Contains(cmd, "PTDBInit_") {
				return "/export/server/server_x/SqlScript/CreateDB/PTDBInit_9_1_426/\n", nil
			}
			if strings.Contains(cmd, "status.sh") {
				return "7", nil
			}
			return "", nil
		},
		limit:      4,
		scriptsDir: "/scripts",
	}
	if err := e.Execute(newServerWO(t, sampleCreateParams()), func(string) {}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(creator.added) != 1 || len(creator.added[0]) != 3 {
		t.Fatalf("AddServers 应一次写 3 行, got %+v", creator.added)
	}
	if published != 1 {
		t.Errorf("应发布一次, got %d", published)
	}
	var dbInit int
	for _, c := range sshCmds {
		if strings.Contains(c, "Create_DB.sh") {
			dbInit++
		}
	}
	if dbInit != 3 {
		t.Errorf("应 3 次建库, got %d", dbInit)
	}
}

func TestNewServerExecutorSkipsExistingRows(t *testing.T) {
	creator := &fakeCreator{existing: map[string]bool{"12802": true, "12002": true}}
	e := &NewServerExecutor{
		creator: creator,
		publish: func(log LogFunc) error { return nil },
		runSSH: func(ip, cmd string, log LogFunc) (string, error) {
			if strings.Contains(cmd, "PTDBInit_") {
				return "PTDBInit_9_1_426/\n", nil
			}
			if strings.Contains(cmd, "status.sh") {
				return "7", nil
			}
			return "", nil
		},
		limit:      4,
		scriptsDir: "/scripts",
	}
	if err := e.Execute(newServerWO(t, sampleCreateParams()), func(string) {}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(creator.added) != 1 || len(creator.added[0]) != 1 || creator.added[0][0]["Id"] != "10002" {
		t.Fatalf("应只写缺失的 10002, got %+v", creator.added)
	}
}

func TestNewServerExecutorRetryOnlyFailedSubset(t *testing.T) {
	creator := &fakeCreator{existing: map[string]bool{"12802": true, "12002": true, "10002": true}}
	var deployed []string
	var mu sync.Mutex
	e := &NewServerExecutor{
		creator: creator,
		publish: func(log LogFunc) error { return nil },
		runSSH: func(ip, cmd string, log LogFunc) (string, error) {
			if strings.Contains(cmd, "update_server") {
				mu.Lock()
				deployed = append(deployed, cmd)
				mu.Unlock()
			}
			if strings.Contains(cmd, "PTDBInit_") {
				return "PTDBInit_9_1_426/\n", nil
			}
			if strings.Contains(cmd, "status.sh") {
				return "7", nil
			}
			return "", nil
		},
		limit:      4,
		scriptsDir: "/scripts",
	}
	p := sampleCreateParams()
	p.LastFailedIDs = []int{10002}
	if err := e.Execute(newServerWO(t, p), func(string) {}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(deployed) != 1 || !strings.Contains(deployed[0], "10002") {
		t.Fatalf("重试应只部署 10002, got %+v", deployed)
	}
}

func TestNewServerExecutorReportsFailed(t *testing.T) {
	creator := &fakeCreator{existing: map[string]bool{}}
	e := &NewServerExecutor{
		creator: creator,
		publish: func(log LogFunc) error { return nil },
		runSSH: func(ip, cmd string, log LogFunc) (string, error) {
			if strings.Contains(cmd, "PTDBInit_") {
				return "PTDBInit_9_1_426/\n", nil
			}
			if strings.Contains(cmd, "status.sh") {
				return "7", nil
			}
			if strings.Contains(cmd, "start.sh") && strings.Contains(cmd, "10002") {
				return "", &deployErr{}
			}
			return "", nil
		},
		limit:      4,
		scriptsDir: "/scripts",
	}
	err := e.Execute(newServerWO(t, sampleCreateParams()), func(string) {})
	if err == nil {
		t.Fatal("有服失败时 Execute 应返回错误")
	}
	if got := e.FailedTargets(); len(got) != 1 || got[0] != 10002 {
		t.Errorf("FailedTargets = %v, want [10002]", got)
	}
}

type deployErr struct{}

func (*deployErr) Error() string { return "boom" }
