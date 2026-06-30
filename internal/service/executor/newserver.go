package executor

import (
	"fmt"
	"strings"
	"sync"

	"gongdan/internal/gameserver"
	"gongdan/internal/model"
)

// NewServerExecutor 创建游戏服:写配置库(只插缺失)→生成 serverlist 发 COS→逐服五步部署→建 ALB 转发。
type NewServerExecutor struct {
	creator    ServerCreator
	publish    func(log LogFunc) error
	runSSH     sshRunner
	limit      int
	scriptsDir string
	alb        *ALBScripts // nil 表示未启用 ALB,跳过该阶段
	failed     []int
	mu         sync.Mutex
}

func NewNewServerExecutor(cfg *RealConfig) *NewServerExecutor {
	rel := NewReleaseExecutor(cfg)
	limit := cfg.Concurrency
	if limit <= 0 {
		limit = defaultConcurrency
	}
	e := &NewServerExecutor{
		creator:    cfg.Creator,
		publish:    func(log LogFunc) error { return cfg.ConfigPublisher.GenerateAndPublish() },
		runSSH:     rel.runSSH,
		limit:      limit,
		scriptsDir: cfg.RemoteScriptsDir,
	}
	if cfg.ALB != nil && cfg.ALB.Enabled {
		e.alb = NewALBScripts(cfg.ALB)
	}
	return e
}

// FailedTargets 暴露本次部署失败的服ID(供 order 写回 LastFailedIDs)。
func (e *NewServerExecutor) FailedTargets() []int { return e.failed }

func (e *NewServerExecutor) Execute(wo *model.WorkOrder, log LogFunc) error {
	p, err := model.UnmarshalNewServerCreateParams(wo.Params)
	if err != nil {
		return fmt.Errorf("解析创建参数: %w", err)
	}

	// 前置解析包版本(fail-fast):建库要选与包版本一致的 PTDBInit_<ver> 目录。
	// 包名形如 ..._9_1_426_<时间戳>...,解析失败则整单不动,不写库不部署。
	big, tgt, verr := parsePacketVersion(p.VersionPackage)
	if verr != nil {
		return fmt.Errorf("解析包版本: %w", verr)
	}
	dbVer := fmt.Sprintf("%s_%d", big, tgt)

	// 1) 写库:只插配置库中尚不存在的 Id(重试安全)
	existing, err := e.existingIDs()
	if err != nil {
		return fmt.Errorf("读现有配置: %w", err)
	}
	var toAdd []map[string]string
	for _, r := range p.Rows {
		if !existing[r.ID] {
			toAdd = append(toAdd, r.Fields)
		}
	}
	if len(toAdd) > 0 {
		log(fmt.Sprintf("写配置库:新增 %d 行", len(toAdd)))
		if err := e.creator.AddServers(toAdd); err != nil {
			return fmt.Errorf("写配置库(整批回滚): %w", err)
		}
	} else {
		log("配置库已有全部新行,跳过写库")
	}

	// 2) 生成 serverlist 发 COS
	log("生成 serverlist 并发布 COS")
	if err := e.publish(log); err != nil {
		return fmt.Errorf("生成发布配置: %w", err)
	}

	// 3) 逐服部署(重试时只部署 LastFailedIDs)
	rows := filterDeployRows(p.Rows, p.LastFailedIDs)
	e.failed = nil
	sem := make(chan struct{}, e.limit)
	var wg sync.WaitGroup
	for _, r := range rows {
		wg.Add(1)
		sem <- struct{}{}
		go func(r model.NewServerRow) {
			defer wg.Done()
			defer func() { <-sem }()
			if derr := e.deployOne(r, p.VersionPackage, dbVer, log); derr != nil {
				log(fmt.Sprintf("  服 %d 部署失败: %v", r.ID, derr))
				e.mu.Lock()
				e.failed = append(e.failed, r.ID)
				e.mu.Unlock()
			}
		}(r)
	}
	wg.Wait()

	if len(e.failed) > 0 {
		return fmt.Errorf("有 %d 个服部署失败: %v", len(e.failed), e.failed)
	}

	// 4) 建 ALB 转发规则(幂等:重试安全)。串行,避免 priority 撞号。
	if e.alb != nil {
		if !shouldSetupALBForRows(p.Rows) {
			log("未选择外网域名,跳过 ALB 转发规则")
			return nil
		}
		log("配置 ALB 转发规则")
		if err := e.alb.SetupForRows(p.Rows, log); err != nil {
			return fmt.Errorf("配置 ALB: %w", err)
		}
	}
	return nil
}

func (e *NewServerExecutor) existingIDs() (map[int]bool, error) {
	rows, err := e.creator.AllRows()
	if err != nil {
		return nil, err
	}
	set := map[int]bool{}
	for _, r := range rows {
		var id int
		fmt.Sscanf(r["Id"], "%d", &id)
		if id > 0 {
			set[id] = true
		}
	}
	return set, nil
}

// filterDeployRows 重试时只保留 LastFailedIDs;为空则全部。
func filterDeployRows(rows []model.NewServerRow, failed []int) []model.NewServerRow {
	if len(failed) == 0 {
		return rows
	}
	keep := map[int]bool{}
	for _, id := range failed {
		keep[id] = true
	}
	var out []model.NewServerRow
	for _, r := range rows {
		if keep[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

func shouldSetupALBForRows(rows []model.NewServerRow) bool {
	for _, r := range rows {
		if usesPublicDomain(r.Fields) {
			return true
		}
	}
	return false
}

func usesPublicDomain(fields map[string]string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(fields["RealSelfPublicUrl"])), "wss://")
}

// deployOne 单服五步:铺包→探DB版本→初始化DB→拉配置→开服+status。
// dbVer 为包版本(如 9_1_426),建库选包内与之一致的 PTDBInit_<ver> 目录。
func (e *NewServerExecutor) deployOne(r model.NewServerRow, pkg, dbVer string, log LogFunc) error {
	ip := r.SelfPublicIp
	conn := gameserver.DBConn{User: r.DataBaseUser, Pwd: r.DataBasePsw, IP: r.MySqlIp, Port: r.MySqlPort}

	log(fmt.Sprintf("  服 %d 铺包 %s", r.ID, pkg))
	if _, err := e.runSSH(ip, buildUpdateCmd(e.scriptsDir, r.ID, pkg), log); err != nil {
		return fmt.Errorf("铺包: %w", err)
	}
	out, err := e.runSSH(ip, buildCreateDBVersionProbeCmd(r.ID), log)
	if err != nil {
		return fmt.Errorf("探DB版本: %w", err)
	}
	ver, err := pickCreateDBVersion(out, dbVer)
	if err != nil {
		return err
	}
	log(fmt.Sprintf("  服 %d 初始化DB(版本 %s)", r.ID, ver))
	if _, err := e.runSSH(ip, buildCreateDBCmd(r.ID, conn, ver), log); err != nil {
		return fmt.Errorf("初始化DB: %w", err)
	}
	if _, err := e.runSSH(ip, buildPullConfigCmd(e.scriptsDir, r.ID), log); err != nil {
		return fmt.Errorf("拉配置: %w", err)
	}
	// 普通启动;遇组件残留(exist)自动强杀+修复启动。
	if err := openServerWithRecover(r.ID, e.scriptsDir, false,
		func(cmd string) (string, error) { return e.runSSH(ip, cmd, log) }, log); err != nil {
		return fmt.Errorf("开服: %w", err)
	}
	stOut, err := e.runSSH(ip, buildStatusCmd(e.scriptsDir, r.ID), log)
	if err != nil {
		return fmt.Errorf("查状态: %w", err)
	}
	if !statusOK(stOut) {
		return fmt.Errorf("status 未就绪: %q", stOut)
	}
	log(fmt.Sprintf("  服 %d 部署完成", r.ID))
	return nil
}
