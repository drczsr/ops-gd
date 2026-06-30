package executor

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"gongdan/internal/gameserver"
)

func TestProbeMockModeNoSSH(t *testing.T) {
	p := NewStatusProber(&RealConfig{})
	var called int32
	p.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		atomic.AddInt32(&called, 1)
		return "7", nil
	}
	servers := []gameserver.Server{{ID: 1, IP: "10.0.0.1"}, {ID: 2, IP: "10.0.0.2"}}
	got := p.Probe("mock", servers)
	if called != 0 {
		t.Errorf("mock 模式不应发起 SSH,called=%d", called)
	}
	for _, s := range servers {
		if got[s.ID].Mask != 7 {
			t.Errorf("mock 模式服%d应在线(7),got %+v", s.ID, got[s.ID])
		}
	}
}

func TestProbeRealParsesMaskAndErrors(t *testing.T) {
	p := NewStatusProber(&RealConfig{RemoteScriptsDir: "/scripts"})
	var sawStatusCmd atomic.Bool
	p.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		if strings.Contains(cmd, "status.sh") {
			sawStatusCmd.Store(true)
		}
		switch ip {
		case "10.0.0.1":
			return "7\n", nil
		case "10.0.0.2":
			return "5", nil
		case "10.0.0.3":
			return "", errors.New("connect refused")
		default:
			return "garbage", nil
		}
	}
	servers := []gameserver.Server{
		{ID: 1, IP: "10.0.0.1"},
		{ID: 2, IP: "10.0.0.2"},
		{ID: 3, IP: "10.0.0.3"},
		{ID: 4, IP: "10.0.0.4"},
		{ID: 5}, // 无内网IP
	}
	got := p.Probe("real", servers)
	if !sawStatusCmd.Load() {
		t.Errorf("探测命令应包含 status.sh")
	}
	if got[1].Mask != 7 {
		t.Errorf("服1应为7,got %+v", got[1])
	}
	if got[2].Mask != 5 {
		t.Errorf("服2应为5(部分异常),got %+v", got[2])
	}
	if got[3].Mask != -1 || got[3].Err == "" {
		t.Errorf("服3连不上应 Mask=-1 且带错误,got %+v", got[3])
	}
	if got[4].Mask != -1 {
		t.Errorf("服4输出不可解析应 Mask=-1,got %+v", got[4])
	}
	if got[5].Mask != -1 || got[5].Err == "" {
		t.Errorf("服5无IP应 Mask=-1 且带原因,got %+v", got[5])
	}
}

func TestProbeRealParsesVersion(t *testing.T) {
	p := NewStatusProber(&RealConfig{RemoteScriptsDir: "/scripts"})
	p.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		// 一次SSH应同时含状态查询与读包名文件。
		if !strings.Contains(cmd, "status.sh") || !strings.Contains(cmd, "packetName.txt") {
			t.Errorf("探测命令应同时含 status.sh 与 packetName.txt,got %q", cmd)
		}
		switch ip {
		case "10.0.0.1": // 状态7 + 规范包名 → 解析出版本
			return "7\n" + probeVerMarker + "\nProjectT_Release16.0-All_9_1_426_202605281817_6618_76255\n", nil
		case "10.0.0.2": // 状态正常但包名文件为空(cat 无输出)→ 版本留空
			return "7\n" + probeVerMarker + "\n", nil
		case "10.0.0.3": // 包名不规范 → 解析失败,版本留空、状态照常
			return "5\n" + probeVerMarker + "\ngarbage-name\n", nil
		default:
			return "", errors.New("unreachable")
		}
	}
	servers := []gameserver.Server{
		{ID: 1, IP: "10.0.0.1"},
		{ID: 2, IP: "10.0.0.2"},
		{ID: 3, IP: "10.0.0.3"},
	}
	got := p.Probe("real", servers)
	if got[1].Mask != 7 || got[1].Version != "9_1_426" {
		t.Errorf("服1应 Mask=7、Version=9_1_426,got %+v", got[1])
	}
	if got[2].Mask != 7 || got[2].Version != "" {
		t.Errorf("服2包名空应版本留空、状态不变,got %+v", got[2])
	}
	if got[3].Mask != 5 || got[3].Version != "" {
		t.Errorf("服3包名不规范应版本留空、状态=5,got %+v", got[3])
	}
}

func TestProbeRealConcurrencyBounded(t *testing.T) {
	p := NewStatusProber(&RealConfig{Concurrency: 2})
	var cur, peak int32
	p.runSSH = func(ip, cmd string, log LogFunc) (string, error) {
		n := atomic.AddInt32(&cur, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if n <= old || atomic.CompareAndSwapInt32(&peak, old, n) {
				break
			}
		}
		defer atomic.AddInt32(&cur, -1)
		return "7", nil
	}
	var servers []gameserver.Server
	for i := 1; i <= 20; i++ {
		servers = append(servers, gameserver.Server{ID: i, IP: "10.0.0.1"})
	}
	got := p.Probe("real", servers)
	if len(got) != 20 {
		t.Fatalf("应探测全部20台,got %d", len(got))
	}
	if atomic.LoadInt32(&peak) > 2 {
		t.Errorf("并发峰值应≤2,got %d", peak)
	}
}
