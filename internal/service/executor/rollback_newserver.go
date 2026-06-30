package executor

import (
	"fmt"
	"strconv"

	"gongdan/internal/gameserver"
	"gongdan/internal/model"
)

// RollbackNewServerExecutor 回滚新建服工单:对本单 Rows 每台拆除(停服→丢库→删包→拆ALB→删配置行),
// 末尾统一重生 serverlist。逐台尽力而为,某台失败不阻断其余台;有任何台未清净则返回错误。
type RollbackNewServerExecutor struct {
	deleter    ServerDeleter
	publish    func(log LogFunc) error
	runSSH     sshRunner
	scriptsDir string
	alb        *ALBScripts // nil = 未启用 ALB,跳过拆 ALB
}

func NewRollbackNewServerExecutor(cfg *RealConfig) *RollbackNewServerExecutor {
	rel := NewReleaseExecutor(cfg)
	e := &RollbackNewServerExecutor{
		deleter:    cfg.Deleter,
		publish:    func(log LogFunc) error { return cfg.ConfigPublisher.GenerateAndPublish() },
		runSSH:     rel.runSSH,
		scriptsDir: cfg.RemoteScriptsDir,
	}
	if cfg.ALB != nil && cfg.ALB.Enabled {
		e.alb = NewALBScripts(cfg.ALB)
	}
	return e
}

// deleteParamsFromRow 把新建行映射为删服参数(供复用拆除步骤)。
func deleteParamsFromRow(r model.NewServerRow) model.DeleteServerParams {
	return model.DeleteServerParams{
		ID: r.ID, Kind: r.Kind, SelfPublicIp: r.SelfPublicIp,
		DataBaseName: r.Fields["DataBaseName"], MySqlIp: r.MySqlIp, MySqlPort: r.MySqlPort,
		DataBaseUser: r.DataBaseUser, DataBasePsw: r.DataBasePsw,
		Fields: r.Fields, RemoveALB: usesPublicDomain(r.Fields),
	}
}

func (e *RollbackNewServerExecutor) Execute(wo *model.WorkOrder, log LogFunc) error {
	p, err := model.UnmarshalNewServerCreateParams(wo.Params)
	if err != nil {
		return fmt.Errorf("解析创建参数: %w", err)
	}
	var failed []int
	for _, r := range p.Rows {
		if derr := e.teardownOne(deleteParamsFromRow(r), log); derr != nil {
			log(fmt.Sprintf("  服 %d 拆除未完成: %v", r.ID, derr))
			failed = append(failed, r.ID)
		}
	}
	log("重生 serverlist 并发布 COS")
	if perr := e.publish(log); perr != nil {
		return fmt.Errorf("重生 serverlist: %w", perr)
	}
	if len(failed) > 0 {
		return fmt.Errorf("有 %d 个服未清净: %v", len(failed), failed)
	}
	log("回滚清理完成")
	return nil
}

// teardownOne 拆一台:停服→丢库→删包→拆ALB→删配置行。任一步失败即返回(该台计入未清净)。
func (e *RollbackNewServerExecutor) teardownOne(p model.DeleteServerParams, log LogFunc) error {
	ip := p.SelfPublicIp
	log(fmt.Sprintf("停服 %d", p.ID))
	if _, err := e.runSSH(ip, buildStopCmd(p.ID), log); err != nil {
		log(fmt.Sprintf("  优雅停服失败,kill 兜底: %v", err))
		if _, kerr := e.runSSH(ip, buildKillCmd(e.scriptsDir, p.ID), log); kerr != nil {
			log(fmt.Sprintf("  kill 也失败(可能本就未起),继续: %v", kerr))
		}
	}
	if p.DataBaseName != "" && p.MySqlIp != "" {
		log(fmt.Sprintf("删除数据库 %s@%s", p.DataBaseName, p.MySqlIp))
		conn := gameserver.DBConn{User: p.DataBaseUser, Pwd: p.DataBasePsw, IP: p.MySqlIp, Port: p.MySqlPort}
		if _, err := e.runSSH(ip, buildDropDBCmd(conn, p.DataBaseName), log); err != nil {
			return fmt.Errorf("丢库: %w", err)
		}
	}
	log(fmt.Sprintf("删除部署包 server_%d", p.ID))
	if _, err := e.runSSH(ip, buildRemovePackageCmd(p.ID), log); err != nil {
		return fmt.Errorf("删包: %w", err)
	}
	if p.RemoveALB && e.alb != nil {
		// 回滚新建服:部署失败的服多半没到 ALB 阶段(从未建过),删服脚本会因找不到规则而报错——
		// 这里按尽力而为,出错只记录不阻断清理(避免把"本就没建"误判成清理失败)。
		log("拆除 ALB 转发规则(失败服可能从未建过,出错忽略)")
		if err := e.alb.TeardownForRow(p, log); err != nil {
			log(fmt.Sprintf("  拆 ALB 跳过/失败(多半未建过): %v", err))
		}
	}
	log(fmt.Sprintf("删除配置库行 Id=%d", p.ID))
	if err := e.deleter.DeleteServer(strconv.Itoa(p.ID)); err != nil {
		return fmt.Errorf("删配置行: %w", err)
	}
	return nil
}
