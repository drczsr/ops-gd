package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gongdan/internal/gameserver"
	"gongdan/internal/model"
)

// localRunner 本地执行函数签名(可注入,便于测试)。
type localRunner func(name string, args, env []string, log LogFunc) (string, error)

// MergeExecutor 合服:改配置库字段 → 生成配置传COS → 全服gd_download → (仅勾合服)跑精简版merge.sh。
type MergeExecutor struct {
	cfg      *RealConfig
	runLocal localRunner
	tmpDir   string
	release  *ReleaseExecutor // 复用 configUpdateWaves / openServers
	store    ConfigStore
	pub      ConfigPublisher
}

func NewMergeExecutor(cfg *RealConfig) *MergeExecutor {
	return &MergeExecutor{
		cfg:      cfg,
		runLocal: RunLocal,
		tmpDir:   os.TempDir(),
		release:  NewReleaseExecutor(cfg),
		store:    cfg.ConfigStore,
		pub:      cfg.ConfigPublisher,
	}
}

// updateFields 改配置库:先全量校验涉及服存在,再按勾选改预合服/合服字段。
func (e *MergeExecutor) updateFields(p model.MergeParams, log LogFunc) error {
	var ids []int
	if p.IncludePreMerge {
		ids = append(ids, p.PreMergeIDs...)
	}
	if p.IncludeMerge {
		for _, pr := range p.Pairs {
			ids = append(ids, pr.Target, pr.Source)
		}
	}
	var missing []int
	for _, id := range ids {
		if _, err := e.store.GetServer(strconv.Itoa(id)); err != nil {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("以下服号在配置库中不存在: %s", joinIDs(missing))
	}
	if p.IncludePreMerge {
		endDate, err := model.MergeDatePlusOneDay(p.PreMergeDate)
		if err != nil {
			return err
		}
		fields := map[string]string{
			"MergeStartDate":  p.PreMergeDate,
			"MergeEndDate":    endDate,
			"MergeStartTime":  "40000",
			"MergeEndTime":    "180000",
			"MergeGuildCount": "100",
		}
		for _, id := range p.PreMergeIDs {
			if err := e.store.UpdateServer(strconv.Itoa(id), fields); err != nil {
				return fmt.Errorf("预合服改服 %d 失败: %w", id, err)
			}
			log(fmt.Sprintf("预合服: 服 %d 设合服公告(开始 %s / 结束 %s)", id, p.PreMergeDate, endDate))
		}
	}
	if p.IncludeMerge {
		for _, pr := range p.Pairs {
			tgt := map[string]string{"MergeStartDate": "-1", "MergeStartTime": "-1",
				"MergeEndDate": "-1", "MergeEndTime": "-1", "MergeGuildCount": "0"}
			if err := e.store.UpdateServer(strconv.Itoa(pr.Target), tgt); err != nil {
				return fmt.Errorf("合服改目标服 %d 失败: %w", pr.Target, err)
			}
			src := map[string]string{"WorldType": "-1", "RealWorldID": strconv.Itoa(pr.Target),
				"MergeStartDate": "-1", "MergeStartTime": "-1", "MergeEndDate": "-1",
				"MergeEndTime": "-1", "MergeGuildCount": "0"}
			if err := e.store.UpdateServer(strconv.Itoa(pr.Source), src); err != nil {
				return fmt.Errorf("合服改源服 %d 失败: %w", pr.Source, err)
			}
			log(fmt.Sprintf("合服: 目标 %d 清公告;源 %d 置废弃(WorldType=-1,RealWorldID=%d)", pr.Target, pr.Source, pr.Target))
		}
	}
	return nil
}

func (e *MergeExecutor) Execute(wo *model.WorkOrder, log LogFunc) error {
	p, err := model.UnmarshalMergeParams(wo.Params)
	if err != nil {
		return fmt.Errorf("解析合服参数: %w", err)
	}
	if !p.IncludeMerge && !p.IncludePreMerge {
		return fmt.Errorf("请至少勾选 合服 或 预合服")
	}
	if p.IncludeMerge {
		if len(p.Pairs) == 0 {
			return fmt.Errorf("合服列表为空")
		}
		if err := validateArg("合服工具包", p.ToolPackage); err != nil {
			return err
		}
	}
	if p.IncludePreMerge && len(p.PreMergeIDs) == 0 {
		return fmt.Errorf("未填写预合服服号")
	}

	// ① 改字段(预合服 + 合服一起)
	if err := e.updateFields(p, log); err != nil {
		return err
	}
	// ② 生成配置 + 提交 COS
	log("=== 生成配置并提交 COS ===")
	if err := e.pub.GenerateAndPublish(); err != nil {
		return fmt.Errorf("生成配置/上传COS: %w", err)
	}
	// ③ 合服(仅勾合服)。放在全服配置更新之前:先把库真正合掉,
	//    再把"源服已合"的新配置下发给在线服,避免"配置说合了、库还没合"的窗口。
	if p.IncludeMerge {
		if err := e.runMergeScript(wo.OrderNo, p, log); err != nil {
			return err // merge.sh 失败直接返回,不跑后续(成功重试时再跑)
		}
	}
	// ④ 全服 gd_download(分波重试)
	servers, err := e.cfg.Source.Servers()
	if err != nil {
		return fmt.Errorf("加载服务器列表: %w", err)
	}
	if failed := e.release.configUpdateWaves(e.release.clusterConfigTargets(servers), log); len(failed) > 0 {
		return fmt.Errorf("全服配置更新失败 %d 台: %s", len(failed), joinIDs(failed))
	}
	// ⑤ GM热更(工单机本地 gd_gmhot.sh,无参,尽力而为,最后一步)
	e.release.gmHotUpdate(log)
	log("合服工单执行完成")
	return nil
}

// runMergeScript 写合服列表 + 生成当前 server 配置,本地调精简版 merge.sh(2 参数)。
func (e *MergeExecutor) runMergeScript(orderNo string, p model.MergeParams, log LogFunc) error {
	listPath := filepath.Join(e.tmpDir, fmt.Sprintf("gongdan_merge_%s.txt", orderNo))
	if err := os.WriteFile(listPath, []byte(mergeListContent(p.Pairs)), 0644); err != nil {
		return fmt.Errorf("写合服列表文件: %w", err)
	}
	log("合服列表文件: " + listPath)

	cfgPath := filepath.Join(e.tmpDir, fmt.Sprintf("gongdan_scl_%s.txt", orderNo))
	if err := e.pub.GenerateServerFile(cfgPath); err != nil {
		return fmt.Errorf("生成 server 配置文件: %w", err)
	}
	log("server 配置文件: " + cfgPath)

	// 把 ssh/hefu 配置注入 merge.sh,使其与 Go 执行器用同一套(config.yaml),
	// 而非脚本里写死的生产路径(测试环境路径不同会导致连不上/找不到目录)。
	env := []string{
		"SERVER_CONFIG_FILE=" + cfgPath,
		"SSH_KEY=" + e.cfg.SSHKey,
		"SSH_PORT=" + strconv.Itoa(e.cfg.SSHPort),
		"SSH_USER=" + e.cfg.SSHUser,
		"HEFU_DIR=" + e.cfg.HefuDir,
	}
	args := []string{e.cfg.MergeScript, p.ToolPackage, listPath}
	if _, err := e.runLocal("bash", args, env, log); err != nil {
		return fmt.Errorf("合服执行失败: %w", err)
	}
	return nil
}

// Open 合服开放:仅勾合服时放开登录+恢复目标服状态;纯预合服无开放动作。
func (e *MergeExecutor) Open(wo *model.WorkOrder, log LogFunc) error {
	p, err := model.UnmarshalMergeParams(wo.Params)
	if err != nil {
		return fmt.Errorf("解析合服参数: %w", err)
	}
	if !p.IncludeMerge {
		log("纯预合服工单,无开放动作")
		return nil
	}
	servers, err := e.cfg.Source.Servers()
	if err != nil {
		return fmt.Errorf("加载服务器列表: %w", err)
	}
	var tgtGameIDs []int
	for _, pair := range p.Pairs {
		tgtGameIDs = append(tgtGameIDs, pair.Target)
	}
	targetIDs := gameserver.ExpandWithBattle(servers, tgtGameIDs)
	return e.release.openServers(targetIDs, servers, log)
}
