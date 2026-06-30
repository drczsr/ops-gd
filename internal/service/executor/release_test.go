package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gongdan/internal/gameserver"
	"gongdan/internal/model"
)

// writeNormalSCL 生成含 n 台普通服(WorldType=0, 各有IP)的配置文件,服ID从10001起。
func writeNormalSCL(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scl.txt")
	var b strings.Builder
	b.WriteString("Id\tDesc\tWorldName\tShow\tWorldType\tc5\tc6\tBattleWorldID\tc8\tSelfPublicIp\n")
	b.WriteString("INT\t-\nconfig\t-\n#ID\t-\n")
	for i := 0; i < n; i++ {
		id := 10001 + i
		fmt.Fprintf(&b, "%d\tD\tW\t\t0\t-1\t%d\t-1\t-1\t10.0.0.%d\n", id, id, i+1)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func seqIDs(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = 10001 + i
	}
	return out
}

// stubDB 让 DB 升级步骤变成"库版本==包版本→跳过":包装当前 e.runSSH 拦截
// packetName 读取返回合法包串(tgt=426),并把 queryDBVer 设为返回 426。
// 用法:在测试设好 e.runSSH 之后调一次 e.stubDB()。给不关心 DB 升级的 Execute 用例用。
func (e *ReleaseExecutor) stubDB() {
	inner := e.runSSH
	e.queryDBVer = func(conn gameserver.DBConn, id int) (int, error) { return 426, nil }
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "packetName.txt") {
			return "ProjectT_All_9_1_426_202601020304\n", nil
		}
		return inner(ip, cmd, log)
	}
}

// TestReleaseExecutorStaggers 每启动一台服前都延时(避免瞬时并发SSH被拒)。
func TestReleaseExecutorStaggers(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 3)),
		Concurrency: 10, LaunchDelay: 400 * time.Millisecond}
	e := NewReleaseExecutor(cfg)

	var mu sync.Mutex
	var delays []time.Duration
	e.sleep = func(d time.Duration) { mu.Lock(); delays = append(delays, d); mu.Unlock() }
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(3), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err != nil {
		t.Fatalf("发版应成功: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	// 3台游戏服(无连带):停服阶段3次 + 换包阶段3次 + 更新配置3次 = 9 次错峰,每次都应是 400ms
	if len(delays) != 9 {
		t.Fatalf("应每台启动前延时一次(停服3+换包3+配置更新3=9次), got %d 次", len(delays))
	}
	for _, d := range delays {
		if d != 400*time.Millisecond {
			t.Errorf("延时应为400ms, got %v", d)
		}
	}
}

// writeSameHostSCL 生成 n 台普通服,全部同一个IP(测试按主机限并发)。
func writeSameHostSCL(t *testing.T, n int, ip string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scl.txt")
	var b strings.Builder
	b.WriteString("Id\tDesc\tWorldName\tShow\tWorldType\tc5\tc6\tBattleWorldID\tc8\tSelfPublicIp\n")
	b.WriteString("INT\t-\nconfig\t-\n#ID\t-\n")
	for i := 0; i < n; i++ {
		id := 10001 + i
		fmt.Fprintf(&b, "%d\tD\tW\t\t0\t-1\t%d\t-1\t-1\t%s\n", id, id, ip)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReleaseExecutorRetriesConnRefused 连接被拒(瞬时)应自动重试,最终成功。
func TestReleaseExecutorRetriesConnRefused(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)),
		Concurrency: 1, SSHRetries: 3}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {} // 不真等
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	var attempts int32
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "update_server.sh") {
			n := atomic.AddInt32(&attempts, 1)
			if n < 3 { // 前两次拒绝,第三次成功
				return "", fmt.Errorf("ssh: connect to host %s port 7722: Connection refused", ip)
			}
			return "", nil
		}
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(1), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("换包应重试到第3次成功, 实际尝试 %d 次", got)
	}
}

// TestReleaseExecutorNoRetryOnScriptFail 非连接类错误(脚本真失败)不应重试。
func TestReleaseExecutorNoRetryOnScriptFail(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)),
		Concurrency: 1, SSHRetries: 3}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	var attempts int32
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "update_server.sh") {
			atomic.AddInt32(&attempts, 1)
			return "", fmt.Errorf("update_server.sh: 磁盘空间不足") // 非连接错误
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(1), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err == nil {
		t.Fatal("脚本真失败应判失败")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("非连接错误不应重试, 实际尝试 %d 次", got)
	}
}

// TestReleaseExecutorPerHostLimit 同一台机器上的并发SSH不超过 perHostLimit。
func TestReleaseExecutorPerHostLimit(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeSameHostSCL(t, 4, "10.0.0.9")),
		Concurrency: 10, PerHostConcurrency: 2}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	var inflight, maxSeen int32
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "update_server.sh") {
			n := atomic.AddInt32(&inflight, 1)
			for {
				m := atomic.LoadInt32(&maxSeen)
				if n <= m || atomic.CompareAndSwapInt32(&maxSeen, m, n) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&inflight, -1)
		}
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(4), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err != nil {
		t.Fatalf("发版应成功: %v", err)
	}
	if maxSeen > 2 {
		t.Errorf("同机并发应≤2, 实测峰值=%d", maxSeen)
	}
	if maxSeen < 2 {
		t.Errorf("同机应能并发到2, 实测=%d", maxSeen)
	}
}

// TestReleaseExecutorWaitsForStartup 开服后进程在热身,status 暂未到7,应轮询等待至7再判成功。
func TestReleaseExecutorWaitsForStartup(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)),
		Concurrency: 1, StatusPollInterval: time.Millisecond, StatusTimeout: time.Second}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {} // 不真等
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	var statusCalls int32
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "status.sh") {
			n := atomic.AddInt32(&statusCalls, 1)
			switch n {
			case 1:
				return "4\n", nil // 只有 GameServer
			case 2:
				return "5\n", nil // 缺 HttpAgent
			default:
				return "7\n", nil // 三个全起来了
			}
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(1), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err != nil {
		t.Fatalf("轮询到7应成功: %v", err)
	}
	if got := atomic.LoadInt32(&statusCalls); got != 3 {
		t.Errorf("应轮询到第3次拿到7, 实际查了 %d 次", got)
	}
}

// TestReleaseExecutorStatusTimeout 超时仍未到7应判失败。
func TestReleaseExecutorStatusTimeout(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)),
		Concurrency: 1, StatusPollInterval: time.Millisecond, StatusTimeout: 5 * time.Millisecond}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "status.sh") {
			return "4\n", nil // 永远起不全
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(1), VersionPackage: "v.zip"})
	err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {})
	if err == nil {
		t.Fatal("超时未达7应判失败")
	}
}

// TestReleaseExecutorBatches 分批执行:每批最多 batchSize 台并发,批与批之间串行。
func TestReleaseExecutorBatches(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 5)),
		Concurrency: 10, BatchSize: 2}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {} // 测试不真延时
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	var inflight, maxSeen int32
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "update_server.sh") {
			n := atomic.AddInt32(&inflight, 1)
			for {
				m := atomic.LoadInt32(&maxSeen)
				if n <= m || atomic.CompareAndSwapInt32(&maxSeen, m, n) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond) // 制造重叠窗口
			atomic.AddInt32(&inflight, -1)
		}
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(5), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err != nil {
		t.Fatalf("发版应成功: %v", err)
	}
	if maxSeen > 2 {
		t.Errorf("每批最多2台并发,实测峰值=%d", maxSeen)
	}
	if maxSeen < 2 {
		t.Errorf("批内应并发(期望峰值=2),实测=%d", maxSeen)
	}
}

func newReleaseTestCfg(t *testing.T) *RealConfig {
	t.Helper()
	return &RealConfig{
		RemoteScriptsDir:   "/export/packages/scripts",
		Source:             fileSource(t, "../../../testdata/ServerConfigList.txt"),
		Concurrency:        4,
		StatusPollInterval: time.Millisecond, // 测试加速:轮询间隔极小
		StatusTimeout:      5 * time.Millisecond,
	}
}

func TestReleaseExecutorSuccess(t *testing.T) {
	e := NewReleaseExecutor(newReleaseTestCfg(t))
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	var mu sync.Mutex
	var cmds []string
	e.runSSH = func(ip, remoteCmd string, log LogFunc) (string, error) {
		mu.Lock()
		cmds = append(cmds, remoteCmd)
		mu.Unlock()
		if strings.Contains(remoteCmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{210}, VersionPackage: "v.zip"})
	wo := &model.WorkOrder{Type: model.TypeRelease, Params: params}

	if err := e.Execute(wo, func(string) {}); err != nil {
		t.Fatalf("发版应成功: %v", err)
	}

	joined := strings.Join(cmds, "\n")
	for _, want := range []string{"update_server.sh 210 v.zip", "gd_pull_cnf.sh 210", "start.sh 210 -worldid=210", "status.sh 210"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少命令 %q, got:\n%s", want, joined)
		}
	}
	// 顺序:换包 → 更新配置 → 开服(gd_pull_cnf 必须在换包之后、开服之前)
	idx := func(sub string) int {
		for i, c := range cmds {
			if strings.Contains(c, sub) {
				return i
			}
		}
		return -1
	}
	if !(idx("update_server.sh") < idx("gd_pull_cnf.sh") && idx("gd_pull_cnf.sh") < idx("start.sh")) {
		t.Errorf("顺序错:应 换包<更新配置<开服, got 换包=%d 配置=%d 开服=%d\n%s",
			idx("update_server.sh"), idx("gd_pull_cnf.sh"), idx("start.sh"), joined)
	}
}

func TestReleaseExecutorStatusFail(t *testing.T) {
	e := NewReleaseExecutor(newReleaseTestCfg(t))
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	e.runSSH = func(ip, remoteCmd string, log LogFunc) (string, error) {
		if strings.Contains(remoteCmd, "status.sh") {
			return "3\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{210}, VersionPackage: "v.zip"})
	wo := &model.WorkOrder{Type: model.TypeRelease, Params: params}

	if err := e.Execute(wo, func(string) {}); err == nil {
		t.Fatal("status≠7 应判为失败")
	}
}

func TestReleaseExecutorMissingIP(t *testing.T) {
	e := NewReleaseExecutor(newReleaseTestCfg(t))
	e.runSSH = func(ip, remoteCmd string, log LogFunc) (string, error) { return "7\n", nil }
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{999999}, VersionPackage: "v.zip"})
	wo := &model.WorkOrder{Type: model.TypeRelease, Params: params}

	if err := e.Execute(wo, func(string) {}); err == nil {
		t.Fatal("找不到IP的服应判为失败")
	}
}

func TestReleaseExecutorIncludeBattle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scl.txt")
	header := "Id\tDesc\tWorldName\tShow\tWorldType\tc5\tc6\tBattleWorldID\tc8\tSelfPublicIp\n" +
		"INT\t-\nconfig\t-\n#ID\t-\n"
	row := func(id, wt, battle int, ip string) string {
		return fmt.Sprintf("%d\tD\tW\t\t%d\t-1\t%d\t%d\t-1\t%s\n", id, wt, id, battle, ip)
	}
	content := header +
		row(10001, 0, 12801, "1.1.1.1") + // 普通服
		row(12801, 2, 12801, "2.2.2.1") + // 战斗服
		row(12901, 3, 12801, "2.2.2.2") // 副本服
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &RealConfig{RemoteScriptsDir: "/export/packages/scripts", Source: fileSource(t, path), Concurrency: 4}
	e := NewReleaseExecutor(cfg)
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	var mu sync.Mutex
	updated := map[int]bool{}
	e.runSSH = func(ip, remoteCmd string, log LogFunc) (string, error) {
		if strings.Contains(remoteCmd, "update_server.sh") {
			var id int
			fmt.Sscanf(remoteCmd, "cd /export/packages/scripts && ./update_server.sh %d", &id)
			mu.Lock()
			updated[id] = true
			mu.Unlock()
		}
		if strings.Contains(remoteCmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{10001}, VersionPackage: "v.zip", IncludeBattle: true})
	wo := &model.WorkOrder{Type: model.TypeRelease, Params: params}
	if err := e.Execute(wo, func(string) {}); err != nil {
		t.Fatalf("发版应成功: %v", err)
	}
	for _, id := range []int{10001, 12801, 12901} {
		if !updated[id] {
			t.Errorf("勾选连带后,服 %d 应被换包, 实际换包集=%v", id, updated)
		}
	}
}

func TestReleaseExecutorRejectsBadPackage(t *testing.T) {
	e := NewReleaseExecutor(newReleaseTestCfg(t))
	var called bool
	e.runSSH = func(ip, remoteCmd string, log LogFunc) (string, error) {
		called = true
		return "7\n", nil
	}
	e.stubDB()
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{210}, VersionPackage: "v.zip; rm -rf /"})
	wo := &model.WorkOrder{Type: model.TypeRelease, Params: params}
	if err := e.Execute(wo, func(string) {}); err == nil {
		t.Fatal("恶意版本包名应被拒绝")
	}
	if called {
		t.Error("校验失败时不应发起任何 SSH 调用")
	}
}

// TestReleaseExecutorRejectsBadHotFields 勾选热更时,非法热更包名/文件列表应在发任何 SSH 前被拒。
func TestReleaseExecutorRejectsBadHotFields(t *testing.T) {
	cases := []struct {
		name  string
		pkg   string
		files string
	}{
		{"恶意热更包名", "cfg.zip; rm -rf /", "a.ini"},
		{"恶意文件列表", "cfg.zip", "a.ini; rm -rf /"},
		{"空热更包名", "", "a.ini"},
		{"空文件列表", "cfg.zip", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := NewReleaseExecutor(newReleaseTestCfg(t))
			var called bool
			e.runSSH = func(ip, remoteCmd string, log LogFunc) (string, error) {
				called = true
				return "7\n", nil
			}
			e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
				called = true
				return "", nil
			}
			e.stubDB()
			params, _ := model.MarshalParams(model.ReleaseParams{
				ServerIDs: []int{210}, VersionPackage: "v.zip",
				IncludeHotUpdate: true, HotScope: "all", HotPackage: c.pkg, HotFiles: c.files,
			})
			wo := &model.WorkOrder{Type: model.TypeRelease, Params: params}
			if err := e.Execute(wo, func(string) {}); err == nil {
				t.Fatal("非法热更字段应被拒绝")
			}
			if called {
				t.Error("校验失败时不应发起任何 SSH/本地调用")
			}
		})
	}
}

// TestReleaseSetsMaintenance 设维护成功后才继续;维护用所选游戏服ID。
func TestReleaseSetsMaintenance(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 2)),
		Concurrency: 4, HefuDir: "/export/op/hefu",
		StatusPollInterval: time.Millisecond, StatusTimeout: 5 * time.Millisecond}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}

	var mu sync.Mutex
	var maintArgs []string
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "update_serverlist_stat.sh 0") {
			maintArgs = args
		}
		return "", nil
	}
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(2), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	joined := strings.Join(maintArgs, " ")
	if !strings.Contains(joined, "update_serverlist_stat.sh 0 10001,10002") {
		t.Errorf("设维护命令不对: %v", maintArgs)
	}
}

// TestReleaseMaintenanceFailAborts 设维护失败 → 整单失败,且不发起任何停服/换包SSH。
func TestReleaseMaintenanceFailAborts(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 2)),
		Concurrency: 4, HefuDir: "/export/op/hefu"}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		return "", fmt.Errorf("exit status 1")
	}
	var sshCalled int32
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		atomic.AddInt32(&sshCalled, 1)
		return "", nil
	}
	e.stubDB()
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(2), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err == nil {
		t.Fatal("设维护失败应整单失败")
	}
	if atomic.LoadInt32(&sshCalled) != 0 {
		t.Errorf("维护失败后不应发起任何SSH, 实际 %d 次", sshCalled)
	}
}

// writeWithBattleSCL 1普通服(10001,IP a, BattleWorldID=12801)+1战斗服(12801,WT2)+1副本服(12901,WT3)
func writeWithBattleSCL(t *testing.T) string {
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

// TestReleaseMaintenanceGameOnly 连带发版时,设维护只含游戏服ID(不含战斗服/副本服)。
func TestReleaseMaintenanceGameOnly(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeWithBattleSCL(t)),
		Concurrency: 4, HefuDir: "/export/op/hefu",
		StatusPollInterval: time.Millisecond, StatusTimeout: time.Second}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}

	var mu sync.Mutex
	var maintArgs []string
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(strings.Join(args, " "), "update_serverlist_stat.sh 0") {
			maintArgs = args
		}
		return "", nil
	}
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{10001}, VersionPackage: "v.zip", IncludeBattle: true})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	joined := strings.Join(maintArgs, " ")
	if !strings.Contains(joined, "update_serverlist_stat.sh 0 10001") {
		t.Errorf("维护应含游戏服10001: %v", maintArgs)
	}
	if strings.Contains(joined, "12801") || strings.Contains(joined, "12901") {
		t.Errorf("维护不应含战斗服/副本服(12801/12901): %v", maintArgs)
	}
}

// TestReleaseStopsBattleBeforeGame 连带发版:先停战斗/副本服,再停游戏服。
func TestReleaseStopsBattleBeforeGame(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeWithBattleSCL(t)),
		Concurrency: 4, StatusPollInterval: time.Millisecond, StatusTimeout: time.Second}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	var mu sync.Mutex
	var stopOrder []int
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "gameserver_stop.py") {
			var id int
			fmt.Sscanf(cmd, "cd /export/server/server_%d/OperationalTools", &id)
			mu.Lock()
			stopOrder = append(stopOrder, id)
			mu.Unlock()
		}
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{10001}, VersionPackage: "v.zip", IncludeBattle: true})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	gameIdx, battleMax := -1, -1
	for i, id := range stopOrder {
		if id == 10001 {
			gameIdx = i
		} else if i > battleMax {
			battleMax = i
		}
	}
	if gameIdx == -1 || gameIdx < battleMax {
		t.Errorf("应先停战斗/副本再停游戏服, 停服顺序=%v", stopOrder)
	}
}

// TestReleaseStopRetryThenKill 停服一直失败→重试stop_retries次后强杀,该服用 repair.sh 启动。
func TestReleaseStopRetryThenKill(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)),
		Concurrency: 1, StopRetries: 2,
		StatusPollInterval: time.Millisecond, StatusTimeout: time.Second}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	var stopTries, killCalls, repairCalls, startCalls int32
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		switch {
		case strings.Contains(cmd, "gameserver_stop.py"):
			atomic.AddInt32(&stopTries, 1)
			return "", fmt.Errorf("exit status 1")
		case strings.Contains(cmd, "kill.sh"):
			atomic.AddInt32(&killCalls, 1)
			return "", nil
		case strings.Contains(cmd, "repair.sh"):
			atomic.AddInt32(&repairCalls, 1)
			return "", nil
		case strings.Contains(cmd, "start.sh"):
			atomic.AddInt32(&startCalls, 1)
			return "", nil
		case strings.Contains(cmd, "status.sh"):
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(1), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err != nil {
		t.Fatalf("强杀兜底后应成功: %v", err)
	}
	if got := atomic.LoadInt32(&stopTries); got != 3 {
		t.Errorf("停服应尝试3次, 实际 %d", got)
	}
	if atomic.LoadInt32(&killCalls) != 1 {
		t.Errorf("应强杀1次, 实际 %d", killCalls)
	}
	if atomic.LoadInt32(&repairCalls) != 1 || atomic.LoadInt32(&startCalls) != 0 {
		t.Errorf("被强杀的服应用 repair.sh(repair=%d start=%d)", repairCalls, startCalls)
	}
}

// status 始终不到7 → 校验失败,应拉回远程启动日志(/tmp/start_<id>.log)定位。
// 参考 merge.sh 第8步:启动输出重定向,仅失败时才 cat 回来。
func TestReleaseDumpsStartLogOnStatusFail(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)),
		Concurrency: 1, StatusPollInterval: time.Millisecond, StatusTimeout: 3 * time.Millisecond}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	var catCalls int32
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		switch {
		case strings.Contains(cmd, "status.sh"):
			return "3\n", nil // 永远不到7,触发校验超时
		case strings.Contains(cmd, "cat /tmp/start_"):
			atomic.AddInt32(&catCalls, 1)
			return "boot 失败堆栈...\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(1), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err == nil {
		t.Fatal("status 始终不到7,应判失败")
	}
	if atomic.LoadInt32(&catCalls) != 1 {
		t.Errorf("status 校验失败应拉一次启动日志, 实际 %d", catCalls)
	}
}

// writeBattleSCL 生成:游戏服10001(WT0,BattleWorldID=20001)、
// 战斗服20001(WT2)、战斗副本服20002(WT3,BattleWorldID=20001),各有IP。
func writeBattleSCL(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "scl.txt")
	var b strings.Builder
	b.WriteString("Id\tDesc\tWorldName\tShow\tWorldType\tc5\tc6\tBattleWorldID\tc8\tSelfPublicIp\n")
	b.WriteString("INT\t-\nconfig\t-\n#ID\t-\n")
	// id, Desc, World, Show, WorldType, c5, c6, BattleWorldID, c8, IP
	fmt.Fprintf(&b, "10001\tD\tW\t\t0\t-1\t10001\t20001\t-1\t10.0.0.1\n")
	fmt.Fprintf(&b, "20001\tD\tW\t\t2\t-1\t20001\t20001\t-1\t10.0.0.2\n")
	fmt.Fprintf(&b, "20002\tD\tW\t\t3\t-1\t20002\t20001\t-1\t10.0.0.3\n")
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// recordUpdates 返回一个 runSSH 实现:status 永远返回7(成功),并把执行了
// update_server.sh 的服ID记录到 rec 里(线程安全)。
func recordUpdates(rec *[]int, mu *sync.Mutex) sshRunner {
	return func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		if strings.Contains(cmd, "update_server.sh") {
			// 命令形如: cd /s && ./update_server.sh 10001 v.zip
			var id int
			fmt.Sscanf(cmd[strings.Index(cmd, "update_server.sh"):], "update_server.sh %d", &id)
			mu.Lock()
			*rec = append(*rec, id)
			mu.Unlock()
		}
		return "", nil
	}
}

// 重试模式:LastFailedIDs 非空时只发这些服,且不做连带扩展。
func TestReleaseRetryOnlyFailedSkipsExpand(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeBattleSCL(t)), Concurrency: 5}
	e := NewReleaseExecutor(cfg)
	var rec []int
	var mu sync.Mutex
	e.runSSH = recordUpdates(&rec, &mu)
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	e.stubDB()

	// 原始勾了连带(IncludeBattle),但有 LastFailedIDs=[10001] → 只发10001,不扩展出20001/20002
	params, _ := model.MarshalParams(model.ReleaseParams{
		ServerIDs: []int{10001}, VersionPackage: "v.zip",
		IncludeBattle: true, LastFailedIDs: []int{10001},
	})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(rec) != 1 || rec[0] != 10001 {
		t.Errorf("重试应只换包10001(不连带), got %v", rec)
	}
	if got := e.FailedTargets(); len(got) != 0 {
		t.Errorf("全成功时 FailedTargets 应为空, got %v", got)
	}
}

// 某台换包失败 → FailedTargets 返回该具体服ID。
func TestReleaseFailedTargetsReportsFailures(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 3)), Concurrency: 5}
	e := NewReleaseExecutor(cfg)
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "update_server.sh 10002") {
			return "", fmt.Errorf("换包脚本失败")
		}
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(3), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err == nil {
		t.Fatal("应返回失败")
	}
	got := e.FailedTargets()
	if len(got) != 1 || got[0] != 10002 {
		t.Errorf("FailedTargets = %v, want [10002]", got)
	}
}

func log_noop(string) {}

// TestIsRetryableSSHExchangeID ssh_exchange_identification 应判为可重试
func TestIsRetryableSSHExchangeID(t *testing.T) {
	err := fmt.Errorf("ssh_exchange_identification: Connection closed by remote host")
	if !isRetryableSSHErr(err) {
		t.Error("ssh_exchange_identification 应判为可重试")
	}
}

// DB升级:cur<tgt 时逐级调 Update_DB.sh,成功后开服。
func TestReleaseDBUpgradeSteps(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)), Concurrency: 2}
	e := NewReleaseExecutor(cfg)
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	e.dbConns = map[int]gameserver.DBConn{10001: {IP: "1.1.1.1", Port: "3306", User: "u", Pwd: "p"}}
	e.queryDBVer = func(conn gameserver.DBConn, id int) (int, error) { return 424, nil }
	var cmds []string
	var mu sync.Mutex
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		mu.Lock()
		cmds = append(cmds, cmd)
		mu.Unlock()
		if strings.Contains(cmd, "packetName.txt") {
			return "ProjectT_All_9_1_426_202601020304\n", nil
		}
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	if err := e.dbUpgradeOne(10001, "1.1.1.1", log_noop); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	var ups []string
	for _, c := range cmds {
		if strings.Contains(c, "Update_DB.sh") {
			ups = append(ups, c)
		}
	}
	if len(ups) != 2 {
		t.Fatalf("应升级2步,got %d: %v", len(ups), ups)
	}
	if !strings.Contains(ups[0], "-version=9_1_424_to_9_1_425") || !strings.Contains(ups[1], "-version=9_1_425_to_9_1_426") {
		t.Errorf("版本步顺序错: %v", ups)
	}
}

// DB升级:cur==tgt 跳过升级,不发 Update_DB.sh。
func TestReleaseDBUpgradeSkipWhenEqual(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)), Concurrency: 2}
	e := NewReleaseExecutor(cfg)
	e.dbConns = map[int]gameserver.DBConn{10001: {IP: "1.1.1.1", Port: "3306", User: "u", Pwd: "p"}}
	e.queryDBVer = func(conn gameserver.DBConn, id int) (int, error) { return 426, nil }
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "Update_DB.sh") {
			t.Error("cur==tgt 不应调 Update_DB.sh")
		}
		if strings.Contains(cmd, "packetName.txt") {
			return "ProjectT_All_9_1_426_202601020304\n", nil
		}
		return "", nil
	}
	if err := e.dbUpgradeOne(10001, "1.1.1.1", log_noop); err != nil {
		t.Fatalf("应成功: %v", err)
	}
}

// DB升级:某步失败 → 返回错误。
func TestReleaseDBUpgradeStepFails(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)), Concurrency: 2}
	e := NewReleaseExecutor(cfg)
	e.dbConns = map[int]gameserver.DBConn{10001: {IP: "1.1.1.1", Port: "3306", User: "u", Pwd: "p"}}
	e.queryDBVer = func(conn gameserver.DBConn, id int) (int, error) { return 424, nil }
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "Update_DB.sh") {
			return "", fmt.Errorf("exit status 107")
		}
		if strings.Contains(cmd, "packetName.txt") {
			return "ProjectT_All_9_1_426_202601020304\n", nil
		}
		return "", nil
	}
	if err := e.dbUpgradeOne(10001, "1.1.1.1", log_noop); err == nil {
		t.Fatal("升级失败应返回错误")
	}
}

func TestHotUpdateClusterGroupsByHost(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Concurrency: 5}
	e := NewReleaseExecutor(cfg)
	servers := []gameserver.Server{
		{ID: 10001, IP: "10.0.0.1", WorldType: 0},
		{ID: 10002, IP: "10.0.0.1", WorldType: 2},
		{ID: 10003, IP: "10.0.0.2", WorldType: 3},
		{ID: 10009, IP: "10.0.0.3", WorldType: 1},
		{ID: 9000, IP: "10.0.0.4", WorldType: 0},
	}
	var mu sync.Mutex
	hit := map[string]string{}
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		mu.Lock()
		hit[ip] = cmd
		mu.Unlock()
		return "", nil
	}
	failed := e.hotUpdateCluster(servers, "cfg.zip", "a.ini", log_noop)
	if len(failed) != 0 {
		t.Fatalf("应全成功, got failed=%v", failed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hit) != 2 {
		t.Fatalf("应命中2台主机, got %v", hit)
	}
	if _, ok := hit["10.0.0.3"]; ok {
		t.Error("WT=1 的主机不应热更")
	}
	if _, ok := hit["10.0.0.4"]; ok {
		t.Error("ID<=10000 的服(即使 WorldType 合格)不应热更")
	}
	if !strings.Contains(hit["10.0.0.1"], "hot_update.sh cfg.zip a.ini") {
		t.Errorf("命令格式错: %q", hit["10.0.0.1"])
	}
}

func TestHotUpdateWaveRetry(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Concurrency: 5}
	e := NewReleaseExecutor(cfg)
	e.hotRetryBackoffs = []time.Duration{2 * time.Second, 3 * time.Second, 5 * time.Second}
	var delays []time.Duration
	var mu sync.Mutex
	e.sleep = func(d time.Duration) { mu.Lock(); delays = append(delays, d); mu.Unlock() }
	var attempts int
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n <= 2 {
			return "", fmt.Errorf("ssh_exchange_identification: Connection closed by remote host")
		}
		return "", nil
	}
	servers := []gameserver.Server{{ID: 10001, IP: "10.0.0.1", WorldType: 0}}
	failed := e.hotUpdateCluster(servers, "cfg.zip", "a.ini", log_noop)
	if len(failed) != 0 {
		t.Fatalf("第3波应成功, got failed=%v", failed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(delays) != 2 || delays[0] != 2*time.Second || delays[1] != 3*time.Second {
		t.Errorf("波间隔 = %v, want [2s 3s]", delays)
	}
}

func TestHotUpdateWaveExhausted(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Concurrency: 5}
	e := NewReleaseExecutor(cfg)
	e.hotRetryBackoffs = []time.Duration{1, 1}
	e.sleep = func(time.Duration) {}
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		return "", fmt.Errorf("ssh_exchange_identification: Connection closed by remote host")
	}
	servers := []gameserver.Server{{ID: 10001, IP: "10.0.0.1", WorldType: 0}}
	failed := e.hotUpdateCluster(servers, "cfg.zip", "a.ini", log_noop)
	if len(failed) != 1 || failed[0] != "10.0.0.1" {
		t.Errorf("应返回失败主机 [10.0.0.1], got %v", failed)
	}
}

func TestGMHotUpdateBestEffort(t *testing.T) {
	cfg := &RealConfig{GMHotUpdateScript: "/export/packages/scripts/gd_gmhot.sh"}
	e := NewReleaseExecutor(cfg)
	var called string
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		called = name + " " + strings.Join(args, " ")
		return "", fmt.Errorf("gm boom")
	}
	e.gmHotUpdate(log_noop) // 不应 panic、无返回值(尽力而为)
	if !strings.Contains(called, "gd_gmhot.sh") {
		t.Errorf("GM命令未正确调用: %q", called)
	}
}

// TestReleaseStop104NoRetry 停服返回104→不重试不强杀,用普通 start.sh。
func TestReleaseStop104NoRetry(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)),
		Concurrency: 1, StopRetries: 2,
		StatusPollInterval: time.Millisecond, StatusTimeout: time.Second}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	var stopTries, killCalls, startCalls int32
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		switch {
		case strings.Contains(cmd, "gameserver_stop.py"):
			atomic.AddInt32(&stopTries, 1)
			return "", fmt.Errorf("exit status 104")
		case strings.Contains(cmd, "kill.sh"):
			atomic.AddInt32(&killCalls, 1)
			return "", nil
		case strings.Contains(cmd, "start.sh"):
			atomic.AddInt32(&startCalls, 1)
			return "", nil
		case strings.Contains(cmd, "status.sh"):
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(1), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	if atomic.LoadInt32(&stopTries) != 1 || atomic.LoadInt32(&killCalls) != 0 || atomic.LoadInt32(&startCalls) != 1 {
		t.Errorf("104应一次成功不强杀用start(stop=%d kill=%d start=%d)", stopTries, killCalls, startCalls)
	}
}

func TestExecuteRunsHotUpdateAfterRelease(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 2)), Concurrency: 5}
	e := NewReleaseExecutor(cfg)
	var hotHosts, gmCalls int
	var mu sync.Mutex
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		if strings.Contains(cmd, "hot_update.sh") {
			mu.Lock()
			hotHosts++
			mu.Unlock()
		}
		return "", nil
	}
	e.stubDB()
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		if strings.Contains(name+strings.Join(args, " "), "gd_gmhot.sh") {
			mu.Lock()
			gmCalls++
			mu.Unlock()
		}
		return "", nil
	}
	params, _ := model.MarshalParams(model.ReleaseParams{
		ServerIDs: seqIDs(2), VersionPackage: "v.zip",
		IncludeHotUpdate: true, HotScope: "all", HotPackage: "cfg.zip", HotFiles: "a.ini",
	})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, log_noop); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hotHosts < 1 {
		t.Error("应执行全服热更")
	}
	if gmCalls != 1 {
		t.Errorf("应执行1次GM热更, got %d", gmCalls)
	}
}

// TestExecuteHotUpdateCoversAllRegardlessOfSelection 热更范围是全服,与本次发版只选了哪几台无关:
// 仅选 1 台发版,但配置里有 5 台合格服(分布在 5 台主机)→ 热更应命中全部 5 台主机。
func TestExecuteHotUpdateCoversAllRegardlessOfSelection(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 5)), Concurrency: 5}
	e := NewReleaseExecutor(cfg)
	var mu sync.Mutex
	hotHosts := map[string]bool{}
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		if strings.Contains(cmd, "hot_update.sh") {
			mu.Lock()
			hotHosts[ip] = true
			mu.Unlock()
		}
		return "", nil
	}
	e.stubDB()
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }

	params, _ := model.MarshalParams(model.ReleaseParams{
		ServerIDs: []int{10001}, VersionPackage: "v.zip", // 只选 1 台发版
		IncludeHotUpdate: true, HotScope: "all", HotPackage: "cfg.zip", HotFiles: "a.ini",
	})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, log_noop); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hotHosts) != 5 {
		t.Errorf("热更应覆盖全部5台合格主机(与发版只选1台无关), got %d: %v", len(hotHosts), hotHosts)
	}
}

func TestExecuteSkipsHotUpdateOnReleaseFailure(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 2)), Concurrency: 5}
	e := NewReleaseExecutor(cfg)
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "hot_update.sh") {
			t.Error("发版失败时不应热更")
		}
		if strings.Contains(cmd, "update_server.sh") {
			return "", fmt.Errorf("换包失败")
		}
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()
	params, _ := model.MarshalParams(model.ReleaseParams{
		ServerIDs: seqIDs(2), VersionPackage: "v.zip",
		IncludeHotUpdate: true, HotScope: "all", HotPackage: "cfg.zip", HotFiles: "a.ini",
	})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, log_noop); err == nil {
		t.Fatal("发版失败应返回错误")
	}
}

func TestExecuteHotOnlyRetry(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 3)), Concurrency: 5}
	e := NewReleaseExecutor(cfg)
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	var updateCalls, hotCalls int
	var mu sync.Mutex
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		mu.Lock()
		if strings.Contains(cmd, "update_server.sh") {
			updateCalls++
		}
		if strings.Contains(cmd, "hot_update.sh") {
			hotCalls++
		}
		mu.Unlock()
		return "", nil
	}
	e.stubDB()
	params, _ := model.MarshalParams(model.ReleaseParams{
		ServerIDs: seqIDs(3), VersionPackage: "v.zip",
		IncludeHotUpdate: true, HotScope: "all", HotPackage: "cfg.zip", HotFiles: "a.ini",
		HotFailedHosts: []string{"10.0.0.1"},
	})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, log_noop); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if updateCalls != 0 {
		t.Errorf("HotFailedHosts重试应跳过发版(换包), got %d 次换包", updateCalls)
	}
	if hotCalls < 1 {
		t.Error("应只热更失败主机")
	}
}

func TestExecuteHotFailureReported(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)), Concurrency: 5}
	e := NewReleaseExecutor(cfg)
	e.hotRetryBackoffs = nil
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "hot_update.sh") {
			return "", fmt.Errorf("ssh_exchange_identification: Connection closed by remote host")
		}
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()
	params, _ := model.MarshalParams(model.ReleaseParams{
		ServerIDs: seqIDs(1), VersionPackage: "v.zip",
		IncludeHotUpdate: true, HotScope: "all", HotPackage: "cfg.zip", HotFiles: "a.ini",
	})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, log_noop); err == nil {
		t.Fatal("热更失败应返回错误")
	}
	if hosts := e.HotFailedHosts(); len(hosts) != 1 || hosts[0] != "10.0.0.1" {
		t.Errorf("HotFailedHosts = %v, want [10.0.0.1]", hosts)
	}
}

// 换包单独限流:全局并发20、swap限3,8台分布在不同主机(避开per-host)→ 同时换包≤3。
func TestReleaseSwapConcurrencyLimit(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 8)),
		Concurrency: 20, SwapConcurrency: 3,
		StatusPollInterval: time.Millisecond, StatusTimeout: time.Second}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	var inflight, maxSeen int32
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "update_server.sh") {
			n := atomic.AddInt32(&inflight, 1)
			for {
				m := atomic.LoadInt32(&maxSeen)
				if n <= m || atomic.CompareAndSwapInt32(&maxSeen, m, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inflight, -1)
		}
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	e.stubDB()
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(8), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, log_noop); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	if maxSeen > 3 {
		t.Errorf("换包并发应≤3, 实测峰值=%d", maxSeen)
	}
	if maxSeen < 2 {
		t.Errorf("换包应能并发(>1), 实测峰值=%d", maxSeen)
	}
}

// 无需勾选:发版对每台服必做DB升级,库<包时逐级升。
func TestReleaseOpen(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", HefuDir: "/h",
		Source: fileSource(t, writeBattleSCL(t)), Concurrency: 5}
	e := NewReleaseExecutor(cfg)
	var mu sync.Mutex
	var limitIDs []int
	var restoreCmd string
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "limit.sh") {
			var id int
			if n, _ := fmt.Sscanf(cmd, "cd /s && ./limit.sh %d", &id); n != 1 {
				t.Errorf("无法从命令解析服ID: %q", cmd)
			}
			mu.Lock()
			limitIDs = append(limitIDs, id)
			mu.Unlock()
		}
		return "", nil
	}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		restoreCmd = strings.Join(args, " ")
		return "", nil
	}
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{10001}, IncludeBattle: true})
	if err := e.Open(&model.WorkOrder{Type: model.TypeRelease, Params: params}, log_noop); err != nil {
		t.Fatalf("Open 应成功: %v", err)
	}
	sort.Ints(limitIDs)
	if !reflect.DeepEqual(limitIDs, []int{10001, 20001, 20002}) {
		t.Errorf("limit.sh 应对含连带的全部服跑, 实际 %v", limitIDs)
	}
	if !strings.Contains(restoreCmd, "update_serverlist_stat.sh 1 10001") ||
		strings.Contains(restoreCmd, "20001") {
		t.Errorf("恢复状态应只对游戏服 10001, 实际 %q", restoreCmd)
	}
}

func TestReleaseHotScopeRequired(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)), Concurrency: 2}
	e := NewReleaseExecutor(cfg)
	params, _ := model.MarshalParams(model.ReleaseParams{
		ServerIDs: []int{10001}, VersionPackage: "v.zip",
		IncludeHotUpdate: true, HotPackage: "c.zip", HotFiles: "a.ini",
	})
	wo := &model.WorkOrder{Type: model.TypeRelease, Params: params}
	err := e.Execute(wo, log_noop)
	if err == nil || !strings.Contains(err.Error(), "热更范围") {
		t.Errorf("缺 HotScope 应报错, got %v", err)
	}
}

func TestReleaseHotScopeSelected(t *testing.T) {
	// 集群里有 10001(目标)与 10002(非目标),都在不同主机
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 2)), Concurrency: 5,
		GMHotUpdateScript: "/export/packages/scripts/gd_gmhot.sh"}
	e := NewReleaseExecutor(cfg)
	e.launchDelay = 0
	e.sleep = func(time.Duration) {}
	var hotIDs, dlIDs []int
	var gmCalled bool
	var mu sync.Mutex
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(cmd, "status.sh"):
			return "7", nil
		case strings.Contains(cmd, "hot_update.sh"):
			// cmd 形如 "cd /s && ./hot_update.sh cfg.zip a.ini 10001"
			sub := cmd[strings.Index(cmd, "hot_update.sh"):]
			var id int
			fmt.Sscanf(sub, "hot_update.sh cfg.zip a.ini %d", &id)
			hotIDs = append(hotIDs, id)
		case strings.Contains(cmd, "gd_download.sh"):
			sub := cmd[strings.Index(cmd, "gd_download.sh"):]
			var id int
			fmt.Sscanf(sub, "gd_download.sh %d", &id)
			dlIDs = append(dlIDs, id)
		case strings.Contains(cmd, "packetName.txt"):
			return "ProjectT_9_1_426_202601010000_", nil
		}
		return "", nil
	}
	e.queryDBVer = func(gameserver.DBConn, int) (int, error) { return 426, nil }
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(name+strings.Join(args, " "), "gd_gmhot.sh") {
			gmCalled = true
		}
		return "", nil
	}
	params, _ := model.MarshalParams(model.ReleaseParams{
		ServerIDs: []int{10001}, VersionPackage: "v.zip",
		IncludeHotUpdate: true, HotScope: "selected", HotPackage: "cfg.zip", HotFiles: "a.ini",
	})
	wo := &model.WorkOrder{Type: model.TypeRelease, Params: params}
	if err := e.Execute(wo, log_noop); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// 热更只命中目标服 10001
	if len(hotIDs) != 1 || hotIDs[0] != 10001 {
		t.Errorf("selected 热更应只含 10001, got %v", hotIDs)
	}
	// 更新配置覆盖全服(10001+10002,均为 WT=0, ID>10000)
	if len(dlIDs) != 2 {
		t.Errorf("更新配置应全服 2 台, got %v", dlIDs)
	}
	if !gmCalled {
		t.Error("GM热更应始终执行")
	}
}

func TestReleaseNoHotStillConfigAndGM(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)), Concurrency: 5,
		GMHotUpdateScript: "/export/packages/scripts/gd_gmhot.sh"}
	e := NewReleaseExecutor(cfg)
	e.launchDelay = 0
	e.sleep = func(time.Duration) {}
	var dlCount int
	var gmCalled bool
	var mu sync.Mutex
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(cmd, "status.sh"):
			return "7", nil
		case strings.Contains(cmd, "gd_download.sh"):
			dlCount++
		case strings.Contains(cmd, "packetName.txt"):
			return "ProjectT_9_1_426_202601010000_", nil
		}
		return "", nil
	}
	e.queryDBVer = func(gameserver.DBConn, int) (int, error) { return 426, nil }
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(name+strings.Join(args, " "), "gd_gmhot.sh") {
			gmCalled = true
		}
		return "", nil
	}
	params, _ := model.MarshalParams(model.ReleaseParams{
		ServerIDs: []int{10001}, VersionPackage: "v.zip",
	})
	wo := &model.WorkOrder{Type: model.TypeRelease, Params: params}
	if err := e.Execute(wo, log_noop); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if dlCount < 1 {
		t.Error("不勾热更也应更新配置")
	}
	if !gmCalled {
		t.Error("不勾热更也应执行 GM热更")
	}
}

func TestConfigUpdateWavesRetriesThenSucceeds(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 2)),
		Concurrency: 5, HotRetryBackoffs: []time.Duration{time.Millisecond}}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	var firstTry int32
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "gd_download.sh") && atomic.AddInt32(&firstTry, 1) == 1 {
			return "", fmt.Errorf("boom")
		}
		return "", nil
	}
	servers, _ := cfg.Source.Servers()
	if failed := e.configUpdateWaves(e.clusterConfigTargets(servers), func(string) {}); len(failed) != 0 {
		t.Errorf("重试后应全成功, got %v", failed)
	}
}

func TestConfigUpdateWavesAlwaysFails(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)),
		Concurrency: 5, HotRetryBackoffs: []time.Duration{time.Millisecond}}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "gd_download.sh") {
			return "", fmt.Errorf("boom")
		}
		return "", nil
	}
	servers, _ := cfg.Source.Servers()
	if failed := e.configUpdateWaves(e.clusterConfigTargets(servers), func(string) {}); len(failed) != 1 {
		t.Errorf("应始终失败 1 台, got %v", failed)
	}
}

func TestReleaseConfigUpdateFailsOrder(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)),
		Concurrency: 5, HotRetryBackoffs: []time.Duration{time.Millisecond}}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		if strings.Contains(cmd, "gd_download.sh") {
			return "", fmt.Errorf("boom")
		}
		return "", nil
	}
	e.stubDB()
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(1), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err == nil {
		t.Fatal("配置更新失败应使发版整单失败")
	}
	if got := e.ConfigFailedTargets(); len(got) != 1 || got[0] != 10001 {
		t.Errorf("ConfigFailedTargets=%v want [10001]", got)
	}
}

func TestReleaseConfigOnlyRetrySkipsPipeline(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 2)), Concurrency: 5}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	var mu sync.Mutex
	var cmds []string
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		mu.Lock()
		cmds = append(cmds, cmd)
		mu.Unlock()
		return "", nil
	}
	params, _ := model.MarshalParams(model.ReleaseParams{
		ServerIDs: seqIDs(2), VersionPackage: "v.zip", ConfigFailedIDs: seqIDs(2)})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, func(string) {}); err != nil {
		t.Fatalf("配置补跑应成功: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cmds) != 2 {
		t.Fatalf("应只跑 2 条 gd_download, got %d: %v", len(cmds), cmds)
	}
	for _, c := range cmds {
		if !strings.Contains(c, "gd_download.sh") {
			t.Errorf("不应触发发版步骤: %s", c)
		}
	}
}

func TestExecuteRunsDBUpgradeUnconditionally(t *testing.T) {
	cfg := &RealConfig{RemoteScriptsDir: "/s", Source: fileSource(t, writeNormalSCL(t, 1)), Concurrency: 2,
		StatusPollInterval: time.Millisecond, StatusTimeout: time.Second}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) { return "", nil }
	e.queryDBVer = func(conn gameserver.DBConn, id int) (int, error) { return 424, nil }
	var ups int32
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "packetName.txt") {
			return "ProjectT_All_9_1_426_202601020304\n", nil
		}
		if strings.Contains(cmd, "Update_DB.sh") {
			atomic.AddInt32(&ups, 1)
			return "", nil
		}
		if strings.Contains(cmd, "status.sh") {
			return "7\n", nil
		}
		return "", nil
	}
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: seqIDs(1), VersionPackage: "v.zip"})
	if err := e.Execute(&model.WorkOrder{Type: model.TypeRelease, Params: params}, log_noop); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	if atomic.LoadInt32(&ups) != 2 {
		t.Errorf("无需勾选应必做DB升级(424→426共2步), got %d", atomic.LoadInt32(&ups))
	}
}

func TestPushConfigAll(t *testing.T) {
	servers := []gameserver.Server{
		{ID: 10001, IP: "10.0.0.1", WorldType: 0},
		{ID: 10002, IP: "10.0.0.2", WorldType: 0},
		{ID: 9999, IP: "10.0.0.9", WorldType: 0}, // ID<=10000 应被过滤
	}
	cfg := &RealConfig{Concurrency: 5, RemoteScriptsDir: "/s", Source: staticSource{servers: servers}}
	e := NewReleaseExecutor(cfg)
	e.sleep = func(time.Duration) {}
	var mu sync.Mutex
	var hit []int
	e.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		mu.Lock()
		hit = append(hit, 1)
		mu.Unlock()
		return "", nil
	}
	failed, err := e.PushConfigAll(func(string) {})
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if len(failed) != 0 {
		t.Errorf("应全部成功, got 失败 %v", failed)
	}
	if len(hit) != 2 {
		t.Errorf("应只对 2 个 ID>10000 的服跑, got %d", len(hit))
	}
}