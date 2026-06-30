package executor

import (
	"fmt"
	"strings"

	"gongdan/internal/gameserver"
)

// ServerControl 单服「直接启停」(非工单流程,供服务器管理页的运维按钮用)。
// 复用执行器的 SSH 配置;mock 模式不连网,直接按成功返回。
type ServerControl struct {
	cfg  *RealConfig
	mock bool
	run  func(ip, cmd string, log LogFunc) (string, error) // 可注入,测试替身
}

// NewServerControl 创建单服控制器。mode 非 "real" 时为 mock(不连网)。
func NewServerControl(mode string, cfg *RealConfig) *ServerControl {
	sc := &ServerControl{cfg: cfg, mock: mode != "real"}
	sc.run = func(ip, cmd string, log LogFunc) (string, error) {
		return RunSSH(cfg, ip, cmd, log)
	}
	return sc
}

// Start 启动单服(start.sh,前台启动,boot 输出重定向到远程 /tmp/start_<id>.log)。
// start.sh 下发即返回,服完全就绪需稍后用「刷新状态」查看。
func (sc *ServerControl) Start(s gameserver.Server, log LogFunc) error {
	if sc.mock {
		log(fmt.Sprintf("[mock] 服 %d 启动成功", s.ID))
		return nil
	}
	if err := requireIP(s); err != nil {
		return err
	}
	_, err := sc.run(s.IP, buildStartCmd(sc.cfg.RemoteScriptsDir, s.ID), log)
	if err != nil {
		sc.dumpStartLog(s, log) // start.sh 输出重定向到远程日志,失败时拉回以便定位原因
	}
	return err
}

// dumpStartLog 失败时把远程 /tmp/start_<id>.log 拉回写入 log(供 UI 展示真正原因)。
func (sc *ServerControl) dumpStartLog(s gameserver.Server, log LogFunc) {
	log(fmt.Sprintf("--- 远程启动日志 /tmp/start_%d.log ---", s.ID))
	if _, err := sc.run(s.IP, buildStartLogCmd(s.ID), log); err != nil {
		log(fmt.Sprintf("(拉取启动日志失败: %v)", err))
	}
}

// residueDetected 判断启动失败是否因「组件残留」:gameserver_start.py 检测到
// DB/HttpAgent/GameServer 进程已存在时输出形如 "error! DBAgent exist(41)"。
func residueDetected(out string) bool { return strings.Contains(out, "exist") }

// openServerWithRecover 通用「开服 + 残留自动恢复」,供单服/批量/发版/新建服等所有启动流程复用。
// 先 start.sh(repairMode=true 的服直接 repair.sh);失败则拉远程启动日志判残留,
// 含 "exist"(组件残留/卡死)→ 自动「强杀(kill)→ 修复启动(repair)」;非残留则按原错误返回,不乱杀。
// run 执行远程命令(可带重试,内部已把输出流给 log)并返回输出文本;返回最终结果。
func openServerWithRecover(id int, scriptsDir string, repairMode bool,
	run func(cmd string) (string, error), log LogFunc) error {
	startCmd := buildStartCmd(scriptsDir, id)
	if repairMode {
		startCmd = buildRepairCmd(scriptsDir, id)
	}
	if _, err := run(startCmd); err == nil {
		return nil
	} else {
		log(fmt.Sprintf("  ↓ 服 %d 启动失败,拉取远程启动日志 /tmp/start_%d.log:", id, id))
		out, _ := run(buildStartLogCmd(id))
		if repairMode || !residueDetected(out) {
			return err // 已是修复启动 / 非残留 → 不自动杀,按原错误返回
		}
	}
	log(fmt.Sprintf("  ⚠ 服 %d 检测到组件残留(exist),自动执行:强制停止 → 修复启动", id))
	if _, kerr := run(buildKillCmd(scriptsDir, id)); kerr != nil {
		log(fmt.Sprintf("  (强制停止返回: %v,继续修复启动)", kerr)) // 残留进程可能本就半死,kill 报错不阻断
	}
	_, rerr := run(buildRepairCmd(scriptsDir, id))
	return rerr
}

// StartAutoRecover 普通启动 + 残留自动恢复(单服/批量用)。委托 openServerWithRecover。
func (sc *ServerControl) StartAutoRecover(s gameserver.Server, log LogFunc) error {
	if sc.mock {
		log(fmt.Sprintf("[mock] 服 %d 启动成功", s.ID))
		return nil
	}
	if err := requireIP(s); err != nil {
		return err
	}
	return openServerWithRecover(s.ID, sc.cfg.RemoteScriptsDir, false,
		func(cmd string) (string, error) { return sc.run(s.IP, cmd, log) }, log)
}

// Stop 优雅停单服(gameserver_stop.py 踢人并关闭各组件)。退出码 104 视为已停。
func (sc *ServerControl) Stop(s gameserver.Server, log LogFunc) error {
	if sc.mock {
		log(fmt.Sprintf("[mock] 服 %d 已停服", s.ID))
		return nil
	}
	if err := requireIP(s); err != nil {
		return err
	}
	_, err := sc.run(s.IP, buildStopCmd(s.ID), log)
	if isStopOK(err) {
		return nil
	}
	return err
}

// Kill 强制停服(kill.sh,kill -9 三个进程)。mock 模式直接成功。
func (sc *ServerControl) Kill(s gameserver.Server, log LogFunc) error {
	if sc.mock {
		log(fmt.Sprintf("[mock] 服 %d 已强制停止", s.ID))
		return nil
	}
	if err := requireIP(s); err != nil {
		return err
	}
	_, err := sc.run(s.IP, buildKillCmd(sc.cfg.RemoteScriptsDir, s.ID), log)
	return err
}

// Repair 修复启动(repair.sh,用于被强杀的服)。mock 模式直接成功。
func (sc *ServerControl) Repair(s gameserver.Server, log LogFunc) error {
	if sc.mock {
		log(fmt.Sprintf("[mock] 服 %d 修复启动成功", s.ID))
		return nil
	}
	if err := requireIP(s); err != nil {
		return err
	}
	_, err := sc.run(s.IP, buildRepairCmd(sc.cfg.RemoteScriptsDir, s.ID), log)
	if err != nil {
		sc.dumpStartLog(s, log) // repair.sh 同样重定向到远程日志,失败时拉回
	}
	return err
}

// LoadSM 内存启动(gameserver_repair_loadsm.py,加载共享内存恢复)。mock 模式直接成功。
func (sc *ServerControl) LoadSM(s gameserver.Server, log LogFunc) error {
	if sc.mock {
		log(fmt.Sprintf("[mock] 服 %d 内存启动成功", s.ID))
		return nil
	}
	if err := requireIP(s); err != nil {
		return err
	}
	_, err := sc.run(s.IP, buildLoadSMCmd(s.ID), log)
	return err
}

// ChangeLoginLimit 修改单服登录限制(gameserver_change_login_limit.py;limit∈{0,1,2,3,4,5,99},0=开放)。
// mock 模式直接成功。
func (sc *ServerControl) ChangeLoginLimit(s gameserver.Server, limit int, log LogFunc) error {
	if sc.mock {
		log(fmt.Sprintf("[mock] 服 %d 设登录限制=%d", s.ID, limit))
		return nil
	}
	if err := requireIP(s); err != nil {
		return err
	}
	_, err := sc.run(s.IP, buildChangeLoginLimitCmd(s.ID, limit), log)
	return err
}

// requireIP 校验服有内网IP(SSH 用),否则报错。
func requireIP(s gameserver.Server) error {
	if strings.TrimSpace(s.IP) == "" {
		return fmt.Errorf("服 %d 无内网IP,无法执行", s.ID)
	}
	return nil
}
