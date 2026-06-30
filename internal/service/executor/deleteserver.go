package executor

import (
	"fmt"
	"strconv"

	"gongdan/internal/gameserver"
	"gongdan/internal/model"
)

// ServerDeleter 配置库删行(由 gsconfig.Store 实现)。
type ServerDeleter interface {
	DeleteServer(id string) error
}

// DeleteServerExecutor 删除游戏服:停服→丢库→删包→拆ALB→删配置行→重生serverlist。
// 每步幂等,工单重试可从头跑。
type DeleteServerExecutor struct {
	deleter    ServerDeleter
	publish    func(log LogFunc) error
	runSSH     sshRunner
	scriptsDir string
	alb        *ALBScripts // nil 表示未启用 ALB,跳过该步
}

func NewDeleteServerExecutor(cfg *RealConfig) *DeleteServerExecutor {
	rel := NewReleaseExecutor(cfg)
	e := &DeleteServerExecutor{
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

func (e *DeleteServerExecutor) Execute(wo *model.WorkOrder, log LogFunc) error {
	p, err := model.UnmarshalDeleteServerParams(wo.Params)
	if err != nil {
		return fmt.Errorf("解析删除参数: %w", err)
	}
	ip := p.SelfPublicIp

	// 1) 停服(优雅;失败 kill 兜底,"本就没起"忽略)
	log(fmt.Sprintf("停服 %d", p.ID))
	if _, err := e.runSSH(ip, buildStopCmd(p.ID), log); err != nil {
		log(fmt.Sprintf("  优雅停服失败,改用 kill 兜底: %v", err))
		if _, kerr := e.runSSH(ip, buildKillCmd(e.scriptsDir, p.ID), log); kerr != nil {
			log(fmt.Sprintf("  kill 也失败(可能本就未启动),继续: %v", kerr))
		}
	}

	// 2) 丢库
	if p.DataBaseName != "" && p.MySqlIp != "" {
		log(fmt.Sprintf("删除数据库 %s@%s", p.DataBaseName, p.MySqlIp))
		conn := gameserver.DBConn{User: p.DataBaseUser, Pwd: p.DataBasePsw, IP: p.MySqlIp, Port: p.MySqlPort}
		if _, err := e.runSSH(ip, buildDropDBCmd(conn, p.DataBaseName), log); err != nil {
			return fmt.Errorf("删除数据库: %w", err)
		}
	} else {
		log("无数据库信息,跳过丢库")
	}

	// 3) 删包
	log(fmt.Sprintf("删除部署包 /export/server/server_%d", p.ID))
	if _, err := e.runSSH(ip, buildRemovePackageCmd(p.ID), log); err != nil {
		return fmt.Errorf("删除部署包: %w", err)
	}

	// 4) 拆 ALB
	if p.RemoveALB && e.alb != nil {
		log("拆除 ALB 转发规则/服务器组")
		if err := e.alb.TeardownForRow(p, log); err != nil {
			return fmt.Errorf("拆除 ALB: %w", err)
		}
	}

	// 5) 删配置行
	log(fmt.Sprintf("删除配置库行 Id=%d", p.ID))
	if err := e.deleter.DeleteServer(strconv.Itoa(p.ID)); err != nil {
		return fmt.Errorf("删除配置行: %w", err)
	}

	// 6) 重生 serverlist 发 COS
	if p.RegenServerlist {
		log("重生 serverlist 并发布 COS")
		if err := e.publish(log); err != nil {
			return fmt.Errorf("重生 serverlist: %w", err)
		}
	}

	log(fmt.Sprintf("服 %d 删除完成", p.ID))
	return nil
}
