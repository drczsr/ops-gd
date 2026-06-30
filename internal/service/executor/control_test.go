package executor

import (
	"errors"
	"strings"
	"testing"

	"gongdan/internal/gameserver"
)

func TestServerControlMockNoSSH(t *testing.T) {
	sc := NewServerControl("mock", &RealConfig{})
	var called int
	sc.run = func(ip, cmd string, log LogFunc) (string, error) { called++; return "", nil }
	s := gameserver.Server{ID: 10001, IP: "10.0.0.1"}
	if err := sc.Start(s, func(string) {}); err != nil {
		t.Fatalf("mock 启动应成功: %v", err)
	}
	if err := sc.Stop(s, func(string) {}); err != nil {
		t.Fatalf("mock 停服应成功: %v", err)
	}
	if called != 0 {
		t.Errorf("mock 模式不应发起 SSH,called=%d", called)
	}
}

func TestServerControlStartStopCmds(t *testing.T) {
	sc := NewServerControl("real", &RealConfig{RemoteScriptsDir: "/scripts"})
	var lastCmd string
	sc.run = func(ip, cmd string, log LogFunc) (string, error) { lastCmd = cmd; return "", nil }
	s := gameserver.Server{ID: 10001, IP: "10.0.0.1"}

	if err := sc.Start(s, func(string) {}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(lastCmd, "start.sh 10001") {
		t.Errorf("启动应调 start.sh,实际: %q", lastCmd)
	}
	if err := sc.Stop(s, func(string) {}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !strings.Contains(lastCmd, "gameserver_stop.py") {
		t.Errorf("停服应调 gameserver_stop.py,实际: %q", lastCmd)
	}
}

func TestServerControlChangeLoginLimit(t *testing.T) {
	// mock 模式不发 SSH
	mock := NewServerControl("mock", &RealConfig{})
	var mc int
	mock.run = func(ip, cmd string, log LogFunc) (string, error) { mc++; return "", nil }
	if err := mock.ChangeLoginLimit(gameserver.Server{ID: 10001, IP: "10.0.0.1"}, 3, func(string) {}); err != nil {
		t.Fatalf("mock 应成功: %v", err)
	}
	if mc != 0 {
		t.Errorf("mock 不应发 SSH, called=%d", mc)
	}
	// real 模式拼直连 python 命令
	sc := NewServerControl("real", &RealConfig{})
	var lastCmd string
	sc.run = func(ip, cmd string, log LogFunc) (string, error) { lastCmd = cmd; return "", nil }
	if err := sc.ChangeLoginLimit(gameserver.Server{ID: 10001, IP: "10.0.0.1"}, 99, func(string) {}); err != nil {
		t.Fatalf("change: %v", err)
	}
	want := "cd /export/server/server_10001/OperationalTools && ./gameserver_change_login_limit.py -worldid=10001 -limit=99"
	if lastCmd != want {
		t.Errorf("命令错误:\n got %q\nwant %q", lastCmd, want)
	}
	// 无内网IP应报错
	if err := sc.ChangeLoginLimit(gameserver.Server{ID: 10002, IP: ""}, 0, func(string) {}); err == nil {
		t.Error("无内网IP应报错")
	}
}

// 启动失败时应回头拉取远程启动日志(cat /tmp/start_<id>.log),把原因写进 log。
func TestServerControlStartDumpsLogOnFailure(t *testing.T) {
	sc := NewServerControl("real", &RealConfig{RemoteScriptsDir: "/scripts"})
	var cmds []string
	sc.run = func(ip, cmd string, log LogFunc) (string, error) {
		cmds = append(cmds, cmd)
		if strings.Contains(cmd, "start.sh") {
			return "", errors.New("exit status 200") // 启动失败
		}
		// cat 日志:把内容写进 log
		log("error! DBAgent exist(41)")
		return "error! DBAgent exist(41)", nil
	}
	var out strings.Builder
	err := sc.Start(gameserver.Server{ID: 235, IP: "10.0.0.1"}, func(s string) { out.WriteString(s + "\n") })
	if err == nil {
		t.Fatal("启动失败应返回错误")
	}
	// 第二条命令应是 cat 启动日志
	if len(cmds) < 2 || !strings.Contains(cmds[1], "cat /tmp/start_235.log") {
		t.Fatalf("失败后应 cat 远程启动日志, cmds=%v", cmds)
	}
	if !strings.Contains(out.String(), "DBAgent exist") {
		t.Errorf("日志原因应写入 log, got: %q", out.String())
	}
}

// 普通启动遇组件残留(exist)→ 自动强杀 + 修复启动。
func TestStartAutoRecoverOnResidue(t *testing.T) {
	sc := NewServerControl("real", &RealConfig{RemoteScriptsDir: "/scripts"})
	var cmds []string
	sc.run = func(ip, cmd string, log LogFunc) (string, error) {
		cmds = append(cmds, cmd)
		switch {
		case strings.Contains(cmd, "start.sh"):
			return "", errors.New("exit status 104") // 启动失败
		case strings.Contains(cmd, "cat /tmp/start_"):
			return "error! DBAgent exist(41)", nil // 远程日志暴露残留
		default: // kill.sh / repair.sh
			return "", nil
		}
	}
	err := sc.StartAutoRecover(gameserver.Server{ID: 235, IP: "10.0.0.1"}, func(string) {})
	if err != nil {
		t.Fatalf("残留后应自动修复成功: %v", err)
	}
	var killed, repaired bool
	for _, c := range cmds {
		if strings.Contains(c, "kill.sh") {
			killed = true
		}
		if strings.Contains(c, "repair.sh") {
			repaired = true
		}
	}
	if !killed || !repaired {
		t.Fatalf("残留应触发 kill+repair, cmds=%v", cmds)
	}
}

// 非残留类启动失败 → 不乱杀,按原错误返回。
func TestStartAutoRecoverNonResidueNoKill(t *testing.T) {
	sc := NewServerControl("real", &RealConfig{RemoteScriptsDir: "/scripts"})
	var cmds []string
	sc.run = func(ip, cmd string, log LogFunc) (string, error) {
		cmds = append(cmds, cmd)
		if strings.Contains(cmd, "start.sh") {
			return "", errors.New("exit status 200") // 非残留:某组件真起不来
		}
		return "", nil // cat 日志:无 exist
	}
	err := sc.StartAutoRecover(gameserver.Server{ID: 235, IP: "10.0.0.1"}, func(string) {})
	if err == nil {
		t.Fatal("非残留失败应返回错误")
	}
	for _, c := range cmds {
		if strings.Contains(c, "kill.sh") || strings.Contains(c, "repair.sh") {
			t.Fatalf("非残留不应触发 kill/repair, cmds=%v", cmds)
		}
	}
}

func TestServerControlStopExit104IsOK(t *testing.T) {
	sc := NewServerControl("real", &RealConfig{})
	sc.run = func(ip, cmd string, log LogFunc) (string, error) {
		return "", errors.New("exit status 104")
	}
	if err := sc.Stop(gameserver.Server{ID: 1, IP: "10.0.0.1"}, func(string) {}); err != nil {
		t.Errorf("退出码 104 应视为已停: %v", err)
	}
}

func TestServerControlRealRequiresIP(t *testing.T) {
	sc := NewServerControl("real", &RealConfig{RemoteScriptsDir: "/scripts"})
	sc.run = func(ip, cmd string, log LogFunc) (string, error) { return "", nil }
	if err := sc.Start(gameserver.Server{ID: 1, IP: ""}, func(string) {}); err == nil {
		t.Error("无内网IP应报错")
	}
}

func TestServerControlKillCmd(t *testing.T) {
	sc := NewServerControl("real", &RealConfig{RemoteScriptsDir: "/scripts"})
	var lastCmd string
	sc.run = func(ip, cmd string, log LogFunc) (string, error) { lastCmd = cmd; return "", nil }
	if err := sc.Kill(gameserver.Server{ID: 10001, IP: "10.0.0.1"}, func(string) {}); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if !strings.Contains(lastCmd, "kill.sh 10001") {
		t.Errorf("强制停止应调 kill.sh,实际: %q", lastCmd)
	}
}

func TestServerControlRepairLoadSMCmds(t *testing.T) {
	sc := NewServerControl("real", &RealConfig{RemoteScriptsDir: "/scripts"})
	var lastCmd string
	sc.run = func(ip, cmd string, log LogFunc) (string, error) { lastCmd = cmd; return "", nil }
	s := gameserver.Server{ID: 10001, IP: "10.0.0.1"}

	if err := sc.Repair(s, func(string) {}); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if !strings.Contains(lastCmd, "repair.sh 10001") {
		t.Errorf("修复启动应调 repair.sh,实际: %q", lastCmd)
	}
	if err := sc.LoadSM(s, func(string) {}); err != nil {
		t.Fatalf("loadsm: %v", err)
	}
	if !strings.Contains(lastCmd, "gameserver_repair_loadsm.py") || !strings.Contains(lastCmd, "-worldid=10001") {
		t.Errorf("内存启动应调 gameserver_repair_loadsm.py,实际: %q", lastCmd)
	}
}

func TestServerControlKillMockNoSSH(t *testing.T) {
	sc := NewServerControl("mock", &RealConfig{})
	var called int
	sc.run = func(ip, cmd string, log LogFunc) (string, error) { called++; return "", nil }
	if err := sc.Kill(gameserver.Server{ID: 1, IP: "10.0.0.1"}, func(string) {}); err != nil || called != 0 {
		t.Errorf("mock 强制停止应成功且不发 SSH, err=%v called=%d", err, called)
	}
}
