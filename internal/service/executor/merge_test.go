package executor

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gongdan/internal/gameserver"
	"gongdan/internal/model"
)

type fakeConfigStore struct {
	existing map[string]bool
	updates  map[string]map[string]string
}

func (f *fakeConfigStore) GetServer(id string) (map[string]string, error) {
	if f.existing[id] {
		return map[string]string{"Id": id}, nil
	}
	return nil, fmt.Errorf("not found")
}
func (f *fakeConfigStore) UpdateServer(id string, fields map[string]string) error {
	if f.updates == nil {
		f.updates = map[string]map[string]string{}
	}
	f.updates[id] = fields
	return nil
}

type fakeConfigPublisher struct {
	publishErr  error
	publishN    int
	serverFileN int
}

func (f *fakeConfigPublisher) GenerateAndPublish() error { f.publishN++; return f.publishErr }
func (f *fakeConfigPublisher) GenerateServerFile(path string) error {
	f.serverFileN++
	return os.WriteFile(path, []byte("Id\n"), 0644)
}

func TestMergeUpdateFieldsPreMergeAndMerge(t *testing.T) {
	store := &fakeConfigStore{existing: map[string]bool{"10001": true, "10004": true, "20001": true}}
	cfg := &RealConfig{ConfigStore: store, ConfigPublisher: &fakeConfigPublisher{}}
	e := NewMergeExecutor(cfg)
	p := model.MergeParams{
		IncludePreMerge: true, PreMergeIDs: []int{20001}, PreMergeDate: "20260630",
		IncludeMerge: true, Pairs: []model.MergePair{{Target: 10001, Source: 10004}},
	}
	if err := e.updateFields(p, func(string) {}); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	pm := store.updates["20001"]
	if pm["MergeStartDate"] != "20260630" || pm["MergeEndDate"] != "20260701" ||
		pm["MergeStartTime"] != "40000" || pm["MergeEndTime"] != "180000" || pm["MergeGuildCount"] != "100" {
		t.Errorf("预合服字段错误: %+v", pm)
	}
	tgt := store.updates["10001"]
	if tgt["MergeStartDate"] != "-1" || tgt["MergeGuildCount"] != "0" {
		t.Errorf("合服目标服应清零: %+v", tgt)
	}
	src := store.updates["10004"]
	if src["WorldType"] != "-1" || src["RealWorldID"] != "10001" || src["MergeStartDate"] != "-1" {
		t.Errorf("合服源服应置废弃: %+v", src)
	}
}

func TestMergeUpdateFieldsMissingFails(t *testing.T) {
	store := &fakeConfigStore{existing: map[string]bool{"10001": true}} // 缺 10004
	cfg := &RealConfig{ConfigStore: store, ConfigPublisher: &fakeConfigPublisher{}}
	e := NewMergeExecutor(cfg)
	p := model.MergeParams{IncludeMerge: true, Pairs: []model.MergePair{{Target: 10001, Source: 10004}}}
	if err := e.updateFields(p, func(string) {}); err == nil {
		t.Fatal("缺服号应失败")
	}
	if len(store.updates) != 0 {
		t.Errorf("失败时不应改库, got %v", store.updates)
	}
}

func TestMergeExecuteOrder(t *testing.T) {
	store := &fakeConfigStore{existing: map[string]bool{"10001": true, "10004": true}}
	pub := &fakeConfigPublisher{}
	servers := []gameserver.Server{{ID: 10001, IP: "10.0.0.1", WorldType: 0}, {ID: 10004, IP: "10.0.0.2", WorldType: 0}}
	cfg := &RealConfig{MergeScript: "/m/merge.sh", RemoteScriptsDir: "/s", Concurrency: 5,
		SSHKey: "/k/op", SSHPort: 2222, SSHUser: "ops", HefuDir: "/export/tmp",
		ConfigStore: store, ConfigPublisher: pub, Source: staticSource{servers: servers}}
	e := NewMergeExecutor(cfg)
	e.tmpDir = t.TempDir()
	e.release.sleep = func(time.Duration) {}
	var mu sync.Mutex
	var seq []string
	e.release.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "gd_download.sh") {
			mu.Lock()
			seq = append(seq, "gd")
			mu.Unlock()
		}
		return "", nil
	}
	var gotArgs, gotEnv []string
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		gotArgs = args
		gotEnv = env
		mu.Lock()
		seq = append(seq, "merge")
		mu.Unlock()
		return "", nil
	}
	var gmCalled bool
	e.release.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		if len(args) > 0 && strings.Contains(args[0], "gd_gmhot") {
			gmCalled = true
		}
		return "", nil
	}
	p := model.MergeParams{IncludeMerge: true, Pairs: []model.MergePair{{Target: 10001, Source: 10004}}, ToolPackage: "tool.zip"}
	params, _ := model.MarshalParams(p)
	if err := e.Execute(&model.WorkOrder{Type: model.TypeMerge, OrderNo: "WO1", Params: params}, func(string) {}); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	if pub.publishN != 1 {
		t.Errorf("应发布一次 COS, got %d", pub.publishN)
	}
	if pub.serverFileN != 1 {
		t.Errorf("应生成一次 server 文件, got %d", pub.serverFileN)
	}
	if len(gotArgs) != 3 || gotArgs[0] != "/m/merge.sh" || gotArgs[1] != "tool.zip" {
		t.Fatalf("merge.sh 参数应为 [script tool list], got %v", gotArgs)
	}
	// merge.sh 必须拿到与 Go 执行器一致的 ssh/hefu 配置(否则用脚本里写死的生产路径)
	for _, want := range []string{"SSH_KEY=/k/op", "SSH_PORT=2222", "SSH_USER=ops", "HEFU_DIR=/export/tmp"} {
		found := false
		for _, kv := range gotEnv {
			if kv == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("merge.sh env 缺 %q, got %v", want, gotEnv)
		}
	}
	if !gmCalled {
		t.Error("合服应在最后调用 GM 热更 gd_gmhot.sh")
	}
	if len(seq) == 0 || seq[0] != "merge" {
		t.Errorf("应先执行合服(merge.sh)再全服 gd_download,实际顺序: %v", seq)
	}
}

func TestMergeExecutePreMergeOnlyNoScript(t *testing.T) {
	store := &fakeConfigStore{existing: map[string]bool{"20001": true}}
	pub := &fakeConfigPublisher{}
	cfg := &RealConfig{RemoteScriptsDir: "/s", Concurrency: 5,
		ConfigStore: store, ConfigPublisher: pub, Source: staticSource{servers: nil}}
	e := NewMergeExecutor(cfg)
	e.tmpDir = t.TempDir()
	e.release.sleep = func(time.Duration) {}
	e.release.runSSH = func(ip, cmd string, log LogFunc) (string, error) { return "", nil }
	called := false
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { called = true; return "", nil }
	var gmCalled bool
	e.release.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		if len(args) > 0 && strings.Contains(args[0], "gd_gmhot") {
			gmCalled = true
		}
		return "", nil
	}
	p := model.MergeParams{IncludePreMerge: true, PreMergeIDs: []int{20001}, PreMergeDate: "20260605"}
	params, _ := model.MarshalParams(p)
	if err := e.Execute(&model.WorkOrder{Type: model.TypeMerge, OrderNo: "WO2", Params: params}, func(string) {}); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	if called {
		t.Error("纯预合服不应调 merge.sh")
	}
	if !gmCalled {
		t.Error("纯预合服也应在最后调用 GM 热更")
	}
}

func TestMergeExecuteNeitherChecked(t *testing.T) {
	cfg := &RealConfig{ConfigStore: &fakeConfigStore{}, ConfigPublisher: &fakeConfigPublisher{}}
	e := NewMergeExecutor(cfg)
	params, _ := model.MarshalParams(model.MergeParams{})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeMerge, Params: params}, func(string) {}); err == nil {
		t.Fatal("两个都不勾应失败")
	}
}

func TestMergeOpenPreMergeOnlyNoop(t *testing.T) {
	cfg := &RealConfig{ConfigStore: &fakeConfigStore{}, ConfigPublisher: &fakeConfigPublisher{},
		Source: staticSource{servers: nil}}
	e := NewMergeExecutor(cfg)
	params, _ := model.MarshalParams(model.MergeParams{IncludePreMerge: true, PreMergeIDs: []int{1}})
	if err := e.Open(&model.WorkOrder{Type: model.TypeMerge, Params: params}, func(string) {}); err != nil {
		t.Fatalf("纯预合服开放应空操作成功: %v", err)
	}
}

func TestMergeOpenRunsLimit(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", HefuDir: "/h", Concurrency: 5,
		ConfigStore: &fakeConfigStore{}, ConfigPublisher: &fakeConfigPublisher{},
		Source: fileSource(t, writeBattleSCL(t))}
	e := NewMergeExecutor(cfg)
	var mu sync.Mutex
	var limitIDs []int
	e.release.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "limit.sh") {
			var id int
			fmt.Sscanf(cmd, "cd /s && ./limit.sh %d", &id)
			mu.Lock()
			limitIDs = append(limitIDs, id)
			mu.Unlock()
		}
		return "", nil
	}
	e.release.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	params, _ := model.MarshalParams(model.MergeParams{IncludeMerge: true, Pairs: []model.MergePair{{Target: 10001, Source: 10009}}})
	if err := e.Open(&model.WorkOrder{Type: model.TypeMerge, Params: params}, func(string) {}); err != nil {
		t.Fatalf("Open 应成功: %v", err)
	}
	sort.Ints(limitIDs)
	if !reflect.DeepEqual(limitIDs, []int{10001, 20001, 20002}) {
		t.Errorf("limit.sh 应对目标+战斗+副本跑, 实际 %v", limitIDs)
	}
}
