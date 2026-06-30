package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gongdan/internal/model"
)

func newHotTestCfg(t *testing.T) *RealConfig {
	t.Helper()
	return &RealConfig{
		RemoteScriptsDir: "/export/packages/scripts",
		Source:           fileSource(t, "../../../testdata/ServerConfigList.txt"),
		Concurrency:      4,
	}
}

func TestHotUpdateExecutorSuccess(t *testing.T) {
	e := NewHotUpdateExecutor(newHotTestCfg(t))

	var mu sync.Mutex
	var cmds []string
	var gmCalled bool
	e.runSSH = func(ip, remoteCmd string, log LogFunc) (string, error) {
		mu.Lock()
		cmds = append(cmds, remoteCmd)
		mu.Unlock()
		return "", nil
	}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		mu.Lock()
		gmCalled = true
		mu.Unlock()
		return "", nil
	}

	params, _ := model.MarshalParams(model.HotupdateParams{
		ServerIDs:     []int{210},
		ConfigPackage: "cfg.zip",
		HotFiles:      "a.ini,b.txt",
	})
	wo := &model.WorkOrder{Type: model.TypeHotupdate, Params: params}

	if err := e.Execute(wo, func(string) {}); err != nil {
		t.Fatalf("热更应成功: %v", err)
	}
	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, "hot_update.sh cfg.zip a.ini,b.txt 210") {
		t.Errorf("缺少热更命令, got:\n%s", joined)
	}
	if !gmCalled {
		t.Error("成功后应执行 GM 热更")
	}
	if len(e.FailedTargets()) != 0 {
		t.Errorf("全成功 FailedTargets 应为空, got %v", e.FailedTargets())
	}
}

func TestHotUpdateExecutorMissingIP(t *testing.T) {
	e := NewHotUpdateExecutor(newHotTestCfg(t))
	e.runSSH = func(ip, remoteCmd string, log LogFunc) (string, error) { return "", nil }
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	params, _ := model.MarshalParams(model.HotupdateParams{
		ServerIDs:     []int{999999},
		ConfigPackage: "cfg.zip",
		HotFiles:      "a.ini",
	})
	wo := &model.WorkOrder{Type: model.TypeHotupdate, Params: params}

	if err := e.Execute(wo, func(string) {}); err == nil {
		t.Fatal("找不到IP的服应判为失败")
	}
}

// writeHotMultiSCL 写一份含 3 台普通服(同一台机器 host1 上有 210/211,host2 上有 220)的配置。
func writeHotMultiSCL(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scl.txt")
	var b strings.Builder
	b.WriteString("Id\tDesc\tWorldName\tShow\tWorldType\tc5\tc6\tBattleWorldID\tc8\tSelfPublicIp\n")
	b.WriteString("INT\t-\nconfig\t-\n#ID\t-\n")
	b.WriteString("210\tD\tW\t\t0\t-1\t210\t-1\t-1\t10.0.0.1\n")
	b.WriteString("211\tD\tW\t\t0\t-1\t211\t-1\t-1\t10.0.0.1\n") // 与 210 同机
	b.WriteString("220\tD\tW\t\t0\t-1\t220\t-1\t-1\t10.0.0.2\n")
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeHotBattleSCL 写一份普通服 10001 -> 战斗服 12801(WT2) + 副本服 12901(WT3),各在不同机器。
func writeHotBattleSCL(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scl.txt")
	var b strings.Builder
	b.WriteString("Id\tDesc\tWorldName\tShow\tWorldType\tc5\tc6\tBattleWorldID\tc8\tSelfPublicIp\n")
	b.WriteString("INT\t-\nconfig\t-\n#ID\t-\n")
	b.WriteString("10001\tD\tW\t\t0\t-1\t10001\t12801\t-1\t10.0.0.1\n")
	b.WriteString("12801\tD\tW\t\t2\t-1\t12801\t12801\t-1\t10.0.0.2\n")
	b.WriteString("12901\tD\tW\t\t3\t-1\t12901\t12801\t-1\t10.0.0.3\n")
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// 某主机始终失败 → 分波用尽后返回失败,FailedTargets 含该主机所有目标服,且不调 GM。
func TestHotUpdateExecutorWaveExhaustedFails(t *testing.T) {
	cfg := newHotTestCfg(t)
	cfg.Source = fileSource(t, writeHotMultiSCL(t))
	e := NewHotUpdateExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.hotRetryBackoffs = []time.Duration{1, 1}

	var gmCalled bool
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if ip == "10.0.0.1" {
			return "", fmt.Errorf("connection refused") // host1 始终失败
		}
		return "", nil
	}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		gmCalled = true
		return "", nil
	}

	params, _ := model.MarshalParams(model.HotupdateParams{
		ServerIDs: []int{210, 211, 220}, ConfigPackage: "cfg.zip", HotFiles: "a.ini",
	})
	wo := &model.WorkOrder{Type: model.TypeHotupdate, Params: params}

	if err := e.Execute(wo, func(string) {}); err == nil {
		t.Fatal("应返回失败")
	}
	if gmCalled {
		t.Error("有失败时不应调 GM")
	}
	got := e.FailedTargets()
	gotSet := map[int]bool{}
	for _, id := range got {
		gotSet[id] = true
	}
	if len(got) != 2 || !gotSet[210] || !gotSet[211] {
		t.Errorf("FailedTargets 应为 host1 上的 210,211, got %v", got)
	}
}

// IncludeBattle → 目标连带战斗服/副本服,各主机都收到热更命令。
func TestHotUpdateExecutorIncludeBattle(t *testing.T) {
	cfg := newHotTestCfg(t)
	cfg.Source = fileSource(t, writeHotBattleSCL(t))
	e := NewHotUpdateExecutor(cfg)
	e.sleep = func(time.Duration) {}

	var mu sync.Mutex
	got := map[int]bool{}
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		mu.Lock()
		for _, id := range []int{10001, 12801, 12901} {
			if strings.Contains(cmd, fmt.Sprintf(" %d", id)) {
				got[id] = true
			}
		}
		mu.Unlock()
		return "", nil
	}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	params, _ := model.MarshalParams(model.HotupdateParams{
		ServerIDs: []int{10001}, ConfigPackage: "cfg.zip", HotFiles: "a.ini", IncludeBattle: true,
	})
	wo := &model.WorkOrder{Type: model.TypeHotupdate, Params: params}

	if err := e.Execute(wo, func(string) {}); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	if !got[10001] || !got[12801] || !got[12901] {
		t.Errorf("连带后应覆盖 10001/12801/12901, got %v", got)
	}
}

// LastFailedIDs 非空 → 只对失败服分组下发,且不连带扩展。
func TestHotUpdateExecutorRetryOnlyFailed(t *testing.T) {
	cfg := newHotTestCfg(t)
	cfg.Source = fileSource(t, writeHotBattleSCL(t))
	e := NewHotUpdateExecutor(cfg)
	e.sleep = func(time.Duration) {}

	var mu sync.Mutex
	var ipsHit []string
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		mu.Lock()
		ipsHit = append(ipsHit, ip)
		mu.Unlock()
		return "", nil
	}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	// 即便勾了 IncludeBattle,重试模式也只发 LastFailedIDs、不连带
	params, _ := model.MarshalParams(model.HotupdateParams{
		ServerIDs: []int{10001}, ConfigPackage: "cfg.zip", HotFiles: "a.ini",
		IncludeBattle: true, LastFailedIDs: []int{10001},
	})
	wo := &model.WorkOrder{Type: model.TypeHotupdate, Params: params}

	if err := e.Execute(wo, func(string) {}); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	if len(ipsHit) != 1 || ipsHit[0] != "10.0.0.1" {
		t.Errorf("重试只应命中 10001 所在主机 10.0.0.1, got %v", ipsHit)
	}
}
