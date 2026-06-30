package executor

import (
	"fmt"
	"time"

	"gongdan/internal/gameserver"
	"gongdan/internal/model"
)

// HotUpdateExecutor 热更:不停服,按主机分组下发 hot_update.sh。
// 与发版后热更共用 hotHostRunner:错峰下发 + 失败主机分波重试 + GM收尾;
// 支持连带战斗/副本服与智能重试(只重发上次失败的服)。
type HotUpdateExecutor struct {
	cfg              *RealConfig
	runSSH           sshRunner
	runLocal         func(name string, args, env []string, log LogFunc) (string, error)
	sleep            func(time.Duration)
	launchDelay      time.Duration
	hotRetryBackoffs []time.Duration
	gmScript         string
	failed           []int // 本次未成功的服ID(供 FailedTargets 写回 LastFailedIDs)
}

func NewHotUpdateExecutor(cfg *RealConfig) *HotUpdateExecutor {
	e := &HotUpdateExecutor{cfg: cfg}
	e.runSSH = func(ip, remoteCmd string, log LogFunc) (string, error) {
		return RunSSH(cfg, ip, remoteCmd, log)
	}
	e.runLocal = func(name string, args, env []string, log LogFunc) (string, error) {
		return RunLocal(name, args, env, log)
	}
	e.sleep = time.Sleep
	e.launchDelay = cfg.LaunchDelay
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
func (e *HotUpdateExecutor) hotRunner() *hotHostRunner {
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

// FailedTargets 返回上次 Execute 未成功的服ID(供 order 层写回 LastFailedIDs)。
func (e *HotUpdateExecutor) FailedTargets() []int { return e.failed }

func (e *HotUpdateExecutor) Execute(wo *model.WorkOrder, log LogFunc) error {
	e.failed = nil // 每次执行先清空,避免早退(校验失败)时残留上次失败集
	p, err := model.UnmarshalHotupdateParams(wo.Params)
	if err != nil {
		return fmt.Errorf("解析热更参数: %w", err)
	}
	if len(p.ServerIDs) == 0 {
		return fmt.Errorf("未选择任何服务器")
	}
	if err := validateArg("配置包", p.ConfigPackage); err != nil {
		return err
	}
	if err := validateFileList(p.HotFiles); err != nil {
		return err
	}
	servers, err := e.cfg.Source.Servers()
	if err != nil {
		return fmt.Errorf("加载服务器列表: %w", err)
	}
	ipMap := make(map[int]string, len(servers))
	for _, s := range servers {
		ipMap[s.ID] = s.IP
	}

	// 目标选择(与发版同优先级):重试只发失败服且不再连带;首次勾连带则扩展。
	ids := p.ServerIDs
	switch {
	case len(p.LastFailedIDs) > 0:
		ids = append([]int(nil), p.LastFailedIDs...)
		log(fmt.Sprintf("重试模式:只重发上次失败的 %d 台: %v", len(ids), ids))
	case p.IncludeBattle:
		ids = gameserver.ExpandWithBattle(servers, p.ServerIDs)
		log(fmt.Sprintf("已连带战斗服/副本服,热更目标共 %d 个: %v", len(ids), ids))
	}

	groups, missing := groupByHost(ids, ipMap)
	var hosts []string
	for ip := range groups {
		hosts = append(hosts, ip)
	}

	runner := e.hotRunner()
	failedHosts := runner.hotUpdateWaves(hosts, groups, p.ConfigPackage, p.HotFiles, log)

	// 失败服 = 缺IP的 + 失败主机上的所有目标服(供智能重试)
	e.failed = append([]int(nil), missing...)
	for _, ip := range failedHosts {
		e.failed = append(e.failed, groups[ip]...)
	}

	if len(missing) == 0 && len(failedHosts) == 0 {
		runner.gmHotUpdate(log)
		return nil
	}
	if len(missing) > 0 {
		return fmt.Errorf("以下服在列表中找不到IP: %v", missing)
	}
	return fmt.Errorf("热更失败的主机: %v", failedHosts)
}
