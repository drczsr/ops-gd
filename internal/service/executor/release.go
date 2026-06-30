package executor

import (
	"errors"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gongdan/internal/gameserver"
	"gongdan/internal/model"
)

// sshRunner 远程执行函数签名(可注入,便于测试)。
type sshRunner func(ip, remoteCmd string, log LogFunc) (string, error)

const (
	defaultBatchSize       = 50                     // 默认每批服数量
	defaultLaunchDelay     = 400 * time.Millisecond // 默认每台启动前延时
	defaultPerHostLimit    = 3                      // 默认同机并发SSH上限
	defaultSSHRetries      = 3                      // 默认连接类失败最大尝试次数
	defaultRetryBackoff    = 500 * time.Millisecond // 默认重试基准退避
	defaultStatusPoll      = 3 * time.Second        // 默认 status 轮询间隔
	defaultStatusTimeout   = 60 * time.Second       // 默认等待 status==7 的最长时间(start已等过,这里是保险)
	defaultHefuDir         = "/export/op/hefu"
	defaultStopRetries     = 2
	defaultConcurrency     = 50 // 默认全局并发(同时在跑流水线的总台数)
	defaultSwapConcurrency = 8  // 默认换包并发(吃EFS带宽,单独卡;~5台打满~1.5GB/s,8为甜点)
)

// ReleaseExecutor 发版:每台服 换包 -> 开服 -> status 校验。
// 分批执行(每批 batchSize 台),批内并发(受 Concurrency 限制),
// 同一台机器并发受 perHostLimit 约束,每台启动前延时 launchDelay,
// 连接类失败按指数退避+抖动重试 maxAttempts 次。
type ReleaseExecutor struct {
	cfg              *RealConfig
	runSSH           sshRunner
	batchSize        int
	launchDelay      time.Duration
	perHostLimit     int
	swapLimit        int           // 换包并发上限
	swapSem          chan struct{} // 换包信号量(只卡换包那一步)
	maxAttempts      int
	retryBackoff     time.Duration
	statusPoll       time.Duration
	statusTO         time.Duration
	hefuDir          string
	stopRetries      int
	runLocal         func(name string, args, env []string, log LogFunc) (string, error)
	sleep            func(time.Duration)
	failed           []int                                             // 本次执行未成功的服ID(供 FailedTargets 暴露给调用层写回)
	dbConns          map[int]gameserver.DBConn                         // 服ID -> DB连接信息(仅本执行器内部用)
	queryDBVer       func(conn gameserver.DBConn, id int) (int, error) // 可注入:查库版本
	hotRetryBackoffs []time.Duration                                   // 热更失败主机分波重试的波间隔
	gmScript         string                                            // GM热更脚本本地路径
	hotFailed        []string                                          // 本次热更最终失败的主机IP
	configFailed     []int                                             // 本次配置更新最终失败的服ID
}

func NewReleaseExecutor(cfg *RealConfig) *ReleaseExecutor {
	e := &ReleaseExecutor{cfg: cfg}
	e.runSSH = func(ip, remoteCmd string, log LogFunc) (string, error) {
		return RunSSH(cfg, ip, remoteCmd, log)
	}
	e.batchSize = cfg.BatchSize
	if e.batchSize <= 0 {
		e.batchSize = defaultBatchSize
	}
	e.perHostLimit = cfg.PerHostConcurrency
	if e.perHostLimit <= 0 {
		e.perHostLimit = defaultPerHostLimit
	}
	e.swapLimit = cfg.SwapConcurrency
	if e.swapLimit <= 0 {
		e.swapLimit = defaultSwapConcurrency
	}
	e.swapSem = make(chan struct{}, e.swapLimit)
	e.maxAttempts = cfg.SSHRetries
	if e.maxAttempts <= 0 {
		e.maxAttempts = defaultSSHRetries
	}
	e.retryBackoff = cfg.SSHRetryBackoff
	if e.retryBackoff <= 0 {
		e.retryBackoff = defaultRetryBackoff
	}
	e.statusPoll = cfg.StatusPollInterval
	if e.statusPoll <= 0 {
		e.statusPoll = defaultStatusPoll
	}
	e.statusTO = cfg.StatusTimeout
	if e.statusTO <= 0 {
		e.statusTO = defaultStatusTimeout
	}
	e.launchDelay = cfg.LaunchDelay // 0 表示不延时(测试用);生产由 config 注入
	e.hefuDir = cfg.HefuDir
	if e.hefuDir == "" {
		e.hefuDir = defaultHefuDir
	}
	e.stopRetries = cfg.StopRetries
	if e.stopRetries <= 0 {
		e.stopRetries = defaultStopRetries
	}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		return RunLocal(name, args, env, log)
	}
	e.sleep = time.Sleep
	e.queryDBVer = queryDBVersion
	e.hotRetryBackoffs = cfg.HotRetryBackoffs
	if len(e.hotRetryBackoffs) == 0 {
		e.hotRetryBackoffs = []time.Duration{2 * time.Second, 3 * time.Second, 5 * time.Second}
	}
	e.gmScript = cfg.GMHotUpdateScript
	if e.gmScript == "" {
		e.gmScript = "/export/packages/scripts/gd_gmhot.sh"
	}
	return e
}

// hotRunner 按当前字段现建主机级热更助手(便于测试注入 runSSH/runLocal/sleep 后仍生效)。
func (e *ReleaseExecutor) hotRunner() *hotHostRunner {
	return &hotHostRunner{
		runSSH:           e.runSSH,
		runLocal:         e.runLocal,
		sleep:            e.sleep,
		launchDelay:      e.launchDelay,
		concurrency:      e.cfg.Concurrency,
		scriptsDir:       e.cfg.RemoteScriptsDir,
		hotRetryBackoffs: e.hotRetryBackoffs,
		gmScript:         e.gmScript,
	}
}

// gmHotUpdate 委托给主机级助手(保留方法名,现有调用/测试不变)。
func (e *ReleaseExecutor) gmHotUpdate(log LogFunc) {
	e.hotRunner().gmHotUpdate(log)
}

// isRetryableSSHErr 判断是否为"连接类"瞬时错误(值得重试)。
// 规则:ssh 自身连接失败退出码=255;或错误信息含常见连接故障关键字。
// 远程脚本本身的非零退出(真失败)不在此列,不会重试。
func isRetryableSSHErr(err error) bool {
	if err == nil {
		return false
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 255 {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, m := range []string{
		"connection refused", "connection timed out", "connection reset",
		"connection closed", "ssh_exchange_identification", "kex_exchange", "no route to host",
		"timed out", "broken pipe", "port 7722", "port 22", " 255",
	} {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// runSSHWithRetry 执行一次远程命令;遇连接类失败按指数退避+抖动重试。
func (e *ReleaseExecutor) runSSHWithRetry(ip, cmd string, log LogFunc) (string, error) {
	var out string
	var err error
	for attempt := 1; attempt <= e.maxAttempts; attempt++ {
		out, err = e.runSSH(ip, cmd, log)
		if err == nil || !isRetryableSSHErr(err) {
			return out, err
		}
		if attempt < e.maxAttempts {
			// 指数退避 + 抖动(0~base),避免重试惊群
			backoff := e.retryBackoff * time.Duration(1<<(attempt-1))
			backoff += time.Duration(rand.Int63n(int64(e.retryBackoff) + 1))
			log(fmt.Sprintf("  [重试] %s 第%d/%d次连接失败(%v),%v 后重试",
				ip, attempt, e.maxAttempts, err, backoff.Truncate(time.Millisecond)))
			e.sleep(backoff)
		}
	}
	return out, fmt.Errorf("连接重试%d次仍失败: %w", e.maxAttempts, err)
}

func (e *ReleaseExecutor) Execute(wo *model.WorkOrder, log LogFunc) error {
	p, err := model.UnmarshalReleaseParams(wo.Params)
	if err != nil {
		return fmt.Errorf("解析发版参数: %w", err)
	}
	if len(p.ServerIDs) == 0 {
		return fmt.Errorf("未选择任何服务器")
	}
	if err := validateArg("版本包", p.VersionPackage); err != nil {
		return err
	}
	servers, err := e.cfg.Source.Servers()
	if err != nil {
		return fmt.Errorf("加载服务器列表: %w", err)
	}
	ipMap := make(map[int]string, len(servers))
	wtMap := make(map[int]int, len(servers))
	for _, s := range servers {
		ipMap[s.ID] = s.IP
		wtMap[s.ID] = s.WorldType
	}
	// DB 升级为发版必做步骤:无条件加载连接信息(是否真升级由 dbUpgradeOne 内的版本比对决定)
	conns, err := e.cfg.Source.DBConns()
	if err != nil {
		return fmt.Errorf("加载DB连接信息: %w", err)
	}
	e.dbConns = conns
	// 勾选热更时,热更包名/文件列表会拼进远程 shell 命令,必须先校验(防注入/防空参数);
	// 覆盖 hotOnly 重试分支与 ④ 正常热更分支两条路径。
	if p.IncludeHotUpdate {
		if p.HotScope != "selected" && p.HotScope != "all" {
			return fmt.Errorf("请选择热更范围(当前选中服/全服)")
		}
		if err := validateArg("热更包", p.HotPackage); err != nil {
			return err
		}
		if err := validateFileList(p.HotFiles); err != nil {
			return err
		}
	}

	// HotFailedHosts 重试:发版已成功、仅热更失败主机待补 → 跳过①②③,只热更这些主机。
	if len(p.LastFailedIDs) == 0 && len(p.HotFailedHosts) > 0 && p.IncludeHotUpdate {
		log(fmt.Sprintf("热更重试模式:只重跑 %d 台失败主机: %v", len(p.HotFailedHosts), p.HotFailedHosts))
		e.failed = nil
		e.hotFailed = e.hotUpdateOnHosts(p.HotFailedHosts, servers, p.HotPackage, p.HotFiles, log)
		if len(e.hotFailed) > 0 {
			log(fmt.Sprintf("热更仍失败主机: %v", e.hotFailed))
			return fmt.Errorf("热更失败 %d 台主机: %v", len(e.hotFailed), e.hotFailed)
		}
		e.gmHotUpdate(log)
		log("热更补跑全部成功")
		return nil
	}

	// ConfigFailedIDs 重试:发版+热更早已成功,只补配置更新失败服(跳过①②③④)。
	if len(p.LastFailedIDs) == 0 && len(p.HotFailedHosts) == 0 && len(p.ConfigFailedIDs) > 0 {
		log(fmt.Sprintf("配置更新重试模式:只补跑 %d 台失败服: %s", len(p.ConfigFailedIDs), joinIDs(p.ConfigFailedIDs)))
		e.failed = nil
		cfgFailed := e.configUpdateWaves(e.idConfigTargets(p.ConfigFailedIDs, servers), log)
		if len(cfgFailed) > 0 {
			e.configFailed = cfgFailed
			return fmt.Errorf("配置更新失败 %d 台: %s", len(cfgFailed), joinIDs(cfgFailed))
		}
		log("配置更新补跑全部成功")
		return nil
	}

	// 目标服选择:
	//  - 重试(LastFailedIDs 非空):只发上次失败的具体服,且不再连带扩展
	//    (战斗服自身若失败,它本身就在 LastFailedIDs 里;再扩展会重停已成功的服)。
	//  - 首次(勾连带):扩展为含战斗服/副本服的去重集。
	//  - 首次(未勾连带):原始目标。
	var targetIDs []int
	switch {
	case len(p.LastFailedIDs) > 0:
		// 拷贝一份,避免 targetIDs/e.failed 与 p.LastFailedIDs 共享底层数组
		targetIDs = append([]int(nil), p.LastFailedIDs...)
		log(fmt.Sprintf("重试模式:只重发上次失败的 %d 台: %v", len(targetIDs), targetIDs))
	case p.IncludeBattle:
		targetIDs = gameserver.ExpandWithBattle(servers, p.ServerIDs)
		log(fmt.Sprintf("已连带战斗服/副本服,发版目标共 %d 个: %v", len(targetIDs), targetIDs))
	default:
		targetIDs = p.ServerIDs
	}
	// 早期中止(如设维护失败)时,视全部目标为失败,保住重试子集不被清空;
	// 正常跑完后由批循环结束处重写为实际失败集。
	e.failed = targetIDs

	// 先解析每台服的IP;找不到IP的直接判失败,不参与并发
	var targets []releaseTarget
	var failed []int
	for _, id := range targetIDs {
		ip, ok := ipMap[id]
		if !ok {
			log(fmt.Sprintf("[失败] 服 %d: 在服务器列表中找不到IP", id))
			failed = append(failed, id)
			continue
		}
		targets = append(targets, releaseTarget{id, ip})
	}

	limit := e.cfg.Concurrency
	if limit < 1 {
		limit = defaultConcurrency
	}

	// 阶段①:设维护(仅游戏服 WorldType==0)
	var gameMaintIDs []int
	for _, t := range targets {
		if wtMap[t.id] == 0 {
			gameMaintIDs = append(gameMaintIDs, t.id)
		}
	}
	if err := e.setMaintenance(gameMaintIDs, log); err != nil {
		return err
	}

	// 阶段②:停服(先战斗/副本,再游戏服),得到被强杀的服集合
	forceKilled := e.stopServers(targets, wtMap, limit, log)

	// 分批执行:每批 batchSize 台,批内并发(受 limit 限制),
	// 每台启动前延时 launchDelay,避免瞬时并发SSH被拒;一批跑完再下一批。
	for start := 0; start < len(targets); start += e.batchSize {
		end := start + e.batchSize
		if end > len(targets) {
			end = len(targets)
		}
		batch := targets[start:end]
		log(fmt.Sprintf("=== 第 %d 批,共 %d 台(%d-%d / 总 %d)===",
			start/e.batchSize+1, len(batch), start+1, end, len(targets)))

		failed = append(failed, e.runBatch(batch, p.VersionPackage, limit, forceKilled, log)...)
	}
	e.failed = failed // 正常完成:实际未成功的服(含缺IP + 各批失败)

	total := len(targetIDs)
	succeeded := total - len(failed)
	if len(failed) > 0 {
		log(fmt.Sprintf("发版结束:成功 %d / 总 %d,失败 %d 台", succeeded, total, len(failed)))
		log(fmt.Sprintf("失败服列表(可复制重发): %s", joinIDs(failed)))
		return fmt.Errorf("发版失败 %d 台: %s", len(failed), joinIDs(failed))
	}
	log(fmt.Sprintf("发版全部成功:%d 台", total))

	// ④ 热更(仅当勾选;发版已 100% 成功)。范围由 HotScope 决定。
	if p.IncludeHotUpdate {
		if p.HotScope == "all" {
			log("=== 开始全服热更 ===")
			e.hotFailed = e.hotUpdateCluster(servers, p.HotPackage, p.HotFiles, log)
		} else {
			log(fmt.Sprintf("=== 开始热更当前选中服 %d 台 ===", len(targetIDs)))
			e.hotFailed = e.hotUpdateSelected(targetIDs, servers, p.HotPackage, p.HotFiles, log)
		}
		if len(e.hotFailed) > 0 {
			log(fmt.Sprintf("热更失败主机(可重试): %v", e.hotFailed))
			return fmt.Errorf("热更失败 %d 台主机: %v", len(e.hotFailed), e.hotFailed)
		}
	}
	// ⑤ 更新配置(全服,分波自动重试)
	cfgFailed := e.configUpdateWaves(e.clusterConfigTargets(servers), log)
	// ⑥ GM热更(始终先跑,尽力而为)
	e.gmHotUpdate(log)
	if len(cfgFailed) > 0 {
		e.configFailed = cfgFailed
		log(fmt.Sprintf("配置更新最终失败 %d 台: %s", len(cfgFailed), joinIDs(cfgFailed)))
		return fmt.Errorf("配置更新失败 %d 台: %s", len(cfgFailed), joinIDs(cfgFailed))
	}
	return nil
}

// releaseTarget 一台待发版的服(已解析出IP)。
type releaseTarget struct {
	id int
	ip string
}

// runConcurrent 按 全局并发limit + 单机并发perHostLimit + 启动错峰launchDelay 执行 fn,
// 返回 fn 返回非nil(失败)的服ID。fn 内部负责该服的动作与日志。
func (e *ReleaseExecutor) runConcurrent(batch []releaseTarget, limit int, fn func(id int, ip string) error) []int {
	type res struct {
		id  int
		err error
	}
	results := make(chan res, len(batch))
	sem := make(chan struct{}, limit)
	hostSems := make(map[string]chan struct{})
	var hsMu sync.Mutex
	hostSem := func(ip string) chan struct{} {
		hsMu.Lock()
		defer hsMu.Unlock()
		hs, ok := hostSems[ip]
		if !ok {
			hs = make(chan struct{}, e.perHostLimit)
			hostSems[ip] = hs
		}
		return hs
	}
	var wg sync.WaitGroup
	for _, t := range batch {
		if e.launchDelay > 0 {
			e.sleep(e.launchDelay)
		}
		wg.Add(1)
		go func(id int, ip string) {
			defer wg.Done()
			hs := hostSem(ip)
			sem <- struct{}{}
			defer func() { <-sem }()
			hs <- struct{}{}
			defer func() { <-hs }()
			results <- res{id, fn(id, ip)}
		}(t.id, t.ip)
	}
	wg.Wait()
	close(results)
	var failed []int
	for r := range results {
		if r.err != nil {
			failed = append(failed, r.id)
		}
	}
	return failed
}

// runBatch 并发执行一批服的发版动作,返回本批失败的服ID。
func (e *ReleaseExecutor) runBatch(batch []releaseTarget, pkg string, limit int, forceKilled map[int]bool, log LogFunc) []int {
	return e.runConcurrent(batch, limit, func(id int, ip string) error {
		err := e.releaseOne(id, ip, pkg, forceKilled, log)
		if err != nil {
			log(fmt.Sprintf("[失败] 服 %d: %v", id, err))
		} else {
			log(fmt.Sprintf("[成功] 服 %d 发版完成", id))
		}
		return err
	})
}

func (e *ReleaseExecutor) releaseOne(id int, ip, pkg string, forceKilled map[int]bool, log LogFunc) error {
	// 换包吃 EFS 读带宽,单独用 swapSem 限流(只卡这一步;换完立即释放,DB升级/开服/校验不再占名额)。
	// 用闭包 + defer 释放:换包返回(含 panic)即放名额,且不会把名额占到整条流水线结束。
	swapErr := func() error {
		e.swapSem <- struct{}{}
		defer func() { <-e.swapSem }()
		_, err := e.runSSHWithRetry(ip, buildUpdateCmd(e.cfg.RemoteScriptsDir, id, pkg), log)
		return err
	}()
	if swapErr != nil {
		return fmt.Errorf("换包: %w", swapErr)
	}
	// 换包后、开服前拉最新配置(ServerConfigList.txt + MergeServerFunction.txt),让服带新配置启动。
	if _, err := e.runSSHWithRetry(ip, buildPullConfigCmd(e.cfg.RemoteScriptsDir, id), log); err != nil {
		return fmt.Errorf("更新配置: %w", err)
	}
	log(fmt.Sprintf("  [更新配置] 服 %d 完成", id))
	if err := e.dbUpgradeOne(id, ip, log); err != nil {
		return fmt.Errorf("DB升级: %w", err)
	}
	// 被强杀的服用修复启动,其余普通启动;普通启动遇组件残留(exist)自动强杀+修复启动。
	// boot 输出重定向到远程 /tmp/start_<id>.log,失败时由 openServerWithRecover 拉回判残留。
	if err := openServerWithRecover(id, e.cfg.RemoteScriptsDir, forceKilled[id],
		func(cmd string) (string, error) { return e.runSSHWithRetry(ip, cmd, log) }, log); err != nil {
		return fmt.Errorf("开服: %w", err)
	}
	if err := e.waitStatusOK(id, ip, log); err != nil {
		e.dumpStartLog(id, ip, log)
		return err
	}
	return nil
}

// dumpStartLog 把远程 /tmp/start_<id>.log(被重定向的 boot 输出)拉回工单日志,
// 仅在开服报错或 status 校验失败时调用,便于定位;成功路径不调,避免刷屏。
func (e *ReleaseExecutor) dumpStartLog(id int, ip string, log LogFunc) {
	log(fmt.Sprintf("  ↓ 服 %d 启动异常,拉取远程启动日志 /tmp/start_%d.log:", id, id))
	if _, err := e.runSSH(ip, buildStartLogCmd(id), log); err != nil {
		log(fmt.Sprintf("  (拉取启动日志失败: %v)", err))
	}
}

// waitStatusOK 轮询 status.sh 直到返回7(三进程全在)或超过 statusTO。
func (e *ReleaseExecutor) waitStatusOK(id int, ip string, log LogFunc) error {
	attempts := 1
	if e.statusPoll > 0 {
		attempts = int(e.statusTO/e.statusPoll) + 1
	}
	var last string
	for i := 0; i < attempts; i++ {
		out, err := e.runSSHWithRetry(ip, buildStatusCmd(e.cfg.RemoteScriptsDir, id), log)
		if err != nil {
			return fmt.Errorf("校验: %w", err)
		}
		last = strings.TrimSpace(out)
		if statusOK(out) {
			return nil
		}
		if i < attempts-1 {
			log(fmt.Sprintf("  服 %d 启动中(status=%s,期望7),%v 后重查", id, last, e.statusPoll))
			e.sleep(e.statusPoll)
		}
	}
	return fmt.Errorf("校验超时(%v): status=%q (期望7=DBAgent+HttpAgent+GameServer全在)", e.statusTO, last)
}

// stopOne 优雅停一台服;失败重试 stopRetries 次仍失败则强杀。
// 返回 true=正常停掉;false=被强杀(开服需用修复脚本)。
func (e *ReleaseExecutor) stopOne(id int, ip string, log LogFunc) bool {
	attempts := e.stopRetries + 1
	for i := 0; i < attempts; i++ {
		_, err := e.runSSHWithRetry(ip, buildStopCmd(id), log)
		if isStopOK(err) {
			return true
		}
		if i < attempts-1 {
			log(fmt.Sprintf("  服 %d 停服失败(%v),重试 %d/%d", id, err, i+1, e.stopRetries))
		}
	}
	log(fmt.Sprintf("  服 %d 优雅停失败,执行强杀 kill.sh", id))
	if _, err := e.runSSHWithRetry(ip, buildKillCmd(e.cfg.RemoteScriptsDir, id), log); err != nil {
		log(fmt.Sprintf("  服 %d 强杀命令报错(仍按已停处理): %v", id, err))
	}
	return false
}

// stopWave 并发停一波服;被强杀的写入 forceKilled。
// 与换包一样套用错峰+限并发(停服同样是SSH,需防瞬时并发被拒)。
func (e *ReleaseExecutor) stopWave(wave []releaseTarget, limit int, forceKilled map[int]bool, log LogFunc) {
	if len(wave) == 0 {
		return
	}
	var mu sync.Mutex
	e.runConcurrent(wave, limit, func(id int, ip string) error {
		if e.stopOne(id, ip, log) {
			log(fmt.Sprintf("[停服] 服 %d 已停", id))
		} else {
			mu.Lock()
			forceKilled[id] = true
			mu.Unlock()
			log(fmt.Sprintf("[停服] 服 %d 已强杀(将用修复启动)", id))
		}
		return nil
	})
}

// stopServers 两波停服:先战斗/副本服(WT∈{2,3}),再游戏服(WT==0)。返回被强杀的服集合。
func (e *ReleaseExecutor) stopServers(targets []releaseTarget, wtMap map[int]int, limit int, log LogFunc) map[int]bool {
	forceKilled := make(map[int]bool)
	var battle, game []releaseTarget
	for _, t := range targets {
		if wt := wtMap[t.id]; wt == 2 || wt == 3 {
			battle = append(battle, t)
		} else {
			game = append(game, t)
		}
	}
	if len(battle) > 0 {
		log(fmt.Sprintf("=== 停战斗/副本服 %d 台 ===", len(battle)))
		e.stopWave(battle, limit, forceKilled, log)
	}
	if len(game) > 0 {
		log(fmt.Sprintf("=== 停游戏服 %d 台 ===", len(game)))
		e.stopWave(game, limit, forceKilled, log)
	}
	return forceKilled
}

// setMaintenance 对给定游戏服设维护态(本地一条命令)。失败返回错误以中止发版。
func (e *ReleaseExecutor) setMaintenance(gameIDs []int, log LogFunc) error {
	if len(gameIDs) == 0 {
		return nil
	}
	name, args := buildMaintenanceCmd(e.hefuDir, gameIDs)
	log(fmt.Sprintf("设维护态: %d 个游戏服 %v", len(gameIDs), gameIDs))
	if _, err := e.runLocal(name, args, nil, log); err != nil {
		return fmt.Errorf("设维护失败: %w", err)
	}
	return nil
}

// Open 发版开放:对执行同批服(含连带,若勾选)放开登录,游戏服恢复正常状态。
func (e *ReleaseExecutor) Open(wo *model.WorkOrder, log LogFunc) error {
	p, err := model.UnmarshalReleaseParams(wo.Params)
	if err != nil {
		return fmt.Errorf("解析发版参数: %w", err)
	}
	servers, err := e.cfg.Source.Servers()
	if err != nil {
		return fmt.Errorf("加载服务器列表: %w", err)
	}
	var targetIDs []int
	if p.IncludeBattle {
		targetIDs = gameserver.ExpandWithBattle(servers, p.ServerIDs)
	} else {
		targetIDs = p.ServerIDs
	}
	return e.openServers(targetIDs, servers, log)
}

// openServers 开放核心(发版/合服共用):
// ① 对全部目标服并发跑 limit.sh -limit=0 放开登录;② 本地对游戏服恢复正常状态。
// 任一步有失败,返回错误(调用方据此置 open_failed,可重试)。
func (e *ReleaseExecutor) openServers(targetIDs []int, servers []gameserver.Server, log LogFunc) error {
	ipMap := make(map[int]string, len(servers))
	wtMap := make(map[int]int, len(servers))
	for _, s := range servers {
		ipMap[s.ID] = s.IP
		wtMap[s.ID] = s.WorldType
	}
	var targets []releaseTarget
	var failed []int
	for _, id := range targetIDs {
		ip, ok := ipMap[id]
		if !ok {
			log(fmt.Sprintf("[失败] 服 %d: 找不到IP", id))
			failed = append(failed, id)
			continue
		}
		targets = append(targets, releaseTarget{id, ip})
	}
	limit := e.cfg.Concurrency
	if limit < 1 {
		limit = defaultConcurrency
	}
	log(fmt.Sprintf("=== 放开登录 %d 台 ===", len(targets)))
	failed = append(failed, e.runConcurrent(targets, limit, func(id int, ip string) error {
		_, err := e.runSSHWithRetry(ip, buildLoginLimitCmd(e.cfg.RemoteScriptsDir, id, 0), log)
		return err
	})...)
	limitFailed := make(map[int]bool, len(failed))
	for _, id := range failed {
		limitFailed[id] = true
	}
	var gameIDs []int
	for _, t := range targets {
		if !limitFailed[t.id] && wtMap[t.id] == 0 {
			gameIDs = append(gameIDs, t.id)
		}
	}
	if len(gameIDs) > 0 {
		name, args := buildRestoreStatusCmd(e.hefuDir, gameIDs)
		log(fmt.Sprintf("恢复正常状态: %d 个游戏服 %v", len(gameIDs), gameIDs))
		if _, err := e.runLocal(name, args, nil, log); err != nil {
			log(fmt.Sprintf("[失败] 恢复状态: %v", err))
			failed = append(failed, gameIDs...)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("开放失败 %d 台: %s", len(failed), joinIDs(failed))
	}
	log("开放全部成功")
	return nil
}

// FailedTargets 返回上次 Execute 未成功的服ID(供调用层写回 LastFailedIDs)。
// 实现可选接口,使 order 层无需依赖具体类型即可取失败集。
func (e *ReleaseExecutor) FailedTargets() []int {
	return e.failed
}

// HotFailedHosts 返回上次热更最终失败的主机IP(供调用层写回)。
func (e *ReleaseExecutor) HotFailedHosts() []string {
	return e.hotFailed
}

// ConfigFailedTargets 返回上次配置更新最终失败的服ID(供调用层写回 ConfigFailedIDs)。
func (e *ReleaseExecutor) ConfigFailedTargets() []int { return e.configFailed }

// dbUpgradeOne 比对包期望DB版本与库当前版本,不一致则逐级调 Update_DB.sh 升级。
// 返回非 nil 表示该服 DB 步骤失败(调用方据此不开服)。
func (e *ReleaseExecutor) dbUpgradeOne(id int, ip string, log LogFunc) error {
	out, err := e.runSSHWithRetry(ip, buildPacketNameCmd(id), log)
	if err != nil {
		return fmt.Errorf("读包版本: %w", err)
	}
	big, tgt, err := parsePacketVersion(out)
	if err != nil {
		return fmt.Errorf("解析包版本: %w", err)
	}
	conn, ok := e.dbConns[id]
	if !ok {
		return fmt.Errorf("找不到服 %d 的DB连接信息", id)
	}
	cur, err := e.queryDBVer(conn, id)
	if err != nil {
		return fmt.Errorf("查库版本: %w", err)
	}
	switch {
	case cur == tgt:
		log(fmt.Sprintf("  服 %d 库版本已是 %d,跳过DB升级", id, tgt))
		return nil
	case cur > tgt:
		log(fmt.Sprintf("  [告警] 服 %d 库版本(%d)高于包版本(%d),跳过DB升级", id, cur, tgt))
		return nil
	}
	steps := dbUpgradeSteps(big, cur, tgt)
	log(fmt.Sprintf("  服 %d 库 %d → 包 %d,逐级升级 %d 步", id, cur, tgt, len(steps)))
	for _, step := range steps {
		if _, err := e.runSSHWithRetry(ip, buildUpdateDBCmd(id, conn, step), log); err != nil {
			return fmt.Errorf("DB升级 %s: %w", step, err)
		}
		log(fmt.Sprintf("  服 %d DB升级完成: %s", id, step))
	}
	return nil
}

// hotUpdateCluster 对 ID>10000 且 WT∈{0,2,3} 的全服按主机分组热更,失败主机自动分波重试。
// 返回最终仍失败的主机IP。
func (e *ReleaseExecutor) hotUpdateCluster(servers []gameserver.Server, pkg, files string, log LogFunc) []string {
	hostIDs := make(map[string][]int)
	for _, s := range servers {
		if s.ID > 10000 && (s.WorldType == 0 || s.WorldType == 2 || s.WorldType == 3) {
			hostIDs[s.IP] = append(hostIDs[s.IP], s.ID)
		}
	}
	var hosts []string
	for ip := range hostIDs {
		hosts = append(hosts, ip)
	}
	return e.hotRunner().hotUpdateWaves(hosts, hostIDs, pkg, files, log)
}

// hotUpdateOnHosts 指定主机集直接热更(用于只重跑失败主机)。
func (e *ReleaseExecutor) hotUpdateOnHosts(hosts []string, servers []gameserver.Server, pkg, files string, log LogFunc) []string {
	hostIDs := make(map[string][]int)
	want := make(map[string]bool, len(hosts))
	for _, ip := range hosts {
		want[ip] = true
	}
	for _, s := range servers {
		if want[s.IP] && s.ID > 10000 && (s.WorldType == 0 || s.WorldType == 2 || s.WorldType == 3) {
			hostIDs[s.IP] = append(hostIDs[s.IP], s.ID)
		}
	}
	return e.hotRunner().hotUpdateWaves(hosts, hostIDs, pkg, files, log)
}

// hotUpdateSelected 只对本次发版目标服按主机分组热更(范围=当前选中服,原样不二次过滤)。
func (e *ReleaseExecutor) hotUpdateSelected(ids []int, servers []gameserver.Server, pkg, files string, log LogFunc) []string {
	ipMap := make(map[int]string, len(servers))
	for _, s := range servers {
		ipMap[s.ID] = s.IP
	}
	groups, _ := groupByHost(ids, ipMap)
	var hosts []string
	for ip := range groups {
		hosts = append(hosts, ip)
	}
	return e.hotRunner().hotUpdateWaves(hosts, groups, pkg, files, log)
}

// clusterConfigTargets 全服配置更新目标:ID>10000 且 WT∈{0,2,3}。
func (e *ReleaseExecutor) clusterConfigTargets(servers []gameserver.Server) []releaseTarget {
	var targets []releaseTarget
	for _, s := range servers {
		if s.ID > 10000 && (s.WorldType == 0 || s.WorldType == 2 || s.WorldType == 3) {
			targets = append(targets, releaseTarget{s.ID, s.IP})
		}
	}
	return targets
}

// idConfigTargets 按指定服ID解析 (id,ip) 目标(只补跑失败服用)。
func (e *ReleaseExecutor) idConfigTargets(ids []int, servers []gameserver.Server) []releaseTarget {
	ipMap := make(map[int]string, len(servers))
	for _, s := range servers {
		ipMap[s.ID] = s.IP
	}
	var targets []releaseTarget
	for _, id := range ids {
		if ip, ok := ipMap[id]; ok {
			targets = append(targets, releaseTarget{id, ip})
		}
	}
	return targets
}

// PushConfigAll 对全服跑 gd_download(分波重试),返回最终失败的服 ID。
// 供「合服预告」独立页复用;log 用裸 func(string) 以便跨包接口匹配。
func (e *ReleaseExecutor) PushConfigAll(log func(string)) ([]int, error) {
	servers, err := e.cfg.Source.Servers()
	if err != nil {
		return nil, fmt.Errorf("读取服务器列表: %w", err)
	}
	return e.configUpdateWaves(e.clusterConfigTargets(servers), log), nil
}

// PushConfigIDs 只对给定服跑 gd_download(重推失败服用)。
func (e *ReleaseExecutor) PushConfigIDs(ids []int, log func(string)) ([]int, error) {
	servers, err := e.cfg.Source.Servers()
	if err != nil {
		return nil, fmt.Errorf("读取服务器列表: %w", err)
	}
	return e.configUpdateWaves(e.idConfigTargets(ids, servers), log), nil
}

// configUpdateWaves 逐台 gd_download.sh;失败服按 hotRetryBackoffs 分波只重跑失败服。
// 返回最后一波仍失败的服ID(空=全成功)。发版第⑤步与合服工单第③步共用。
func (e *ReleaseExecutor) configUpdateWaves(targets []releaseTarget, log LogFunc) []int {
	if len(targets) == 0 {
		return nil
	}
	limit := e.cfg.Concurrency
	if limit < 1 {
		limit = defaultConcurrency
	}
	ipMap := make(map[int]string, len(targets))
	for _, t := range targets {
		ipMap[t.id] = t.ip
	}
	remaining := targets
	for wave := 0; ; wave++ {
		log(fmt.Sprintf("=== 更新配置(全服 %d 台,第 %d 波)===", len(remaining), wave+1))
		failedIDs := e.runConcurrent(remaining, limit, func(id int, ip string) error {
			if _, err := e.runSSHWithRetry(ip, buildConfigDownloadCmd(e.cfg.RemoteScriptsDir, id), log); err != nil {
				log(fmt.Sprintf("  [配置更新失败] 服 %d: %v", id, err))
				return err
			}
			log(fmt.Sprintf("  [配置更新] 服 %d 完成", id))
			return nil
		})
		if len(failedIDs) == 0 {
			return nil
		}
		if wave >= len(e.hotRetryBackoffs) {
			return failedIDs
		}
		d := e.hotRetryBackoffs[wave]
		log(fmt.Sprintf("配置更新失败 %d 台,%v 后重试(第 %d 波)", len(failedIDs), d, wave+2))
		e.sleep(d)
		next := make([]releaseTarget, 0, len(failedIDs))
		for _, id := range failedIDs {
			next = append(next, releaseTarget{id, ipMap[id]})
		}
		remaining = next
	}
}
