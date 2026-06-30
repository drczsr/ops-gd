package executor

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"gongdan/internal/gameserver"
)

// ProbeResult 单服探测结果。Mask 为 status.sh 位掩码(7=DB+HttpAgent+GameServer 全在);
// 连接/执行失败或无法解析时 Mask=-1,Err 带原因。
// Version 为包版本(形如 9_1_426),与状态同一次SSH顺带取得;读不到/解析失败留空(展示用,不影响状态)。
type ProbeResult struct {
	Mask    int    `json:"mask"`
	Err     string `json:"err,omitempty"`
	Version string `json:"version,omitempty"`
}

// StatusProber 并发探测各服进程状态(只读,复用执行器 SSH 配置)。
// 与发版的 waitStatusOK 同源:远程跑 status.sh <id> 读位掩码。
type StatusProber struct {
	cfg    *RealConfig
	runSSH sshRunner
	limit  int
}

// NewStatusProber 创建探测器。并发上限取 cfg.Concurrency(缺省同执行器默认)。
func NewStatusProber(cfg *RealConfig) *StatusProber {
	p := &StatusProber{cfg: cfg}
	p.runSSH = func(ip, remoteCmd string, log LogFunc) (string, error) {
		return runProbeSSH(cfg, ip, remoteCmd)
	}
	p.limit = cfg.Concurrency
	if p.limit <= 0 {
		p.limit = defaultConcurrency
	}
	return p
}

// Probe 探测一批服,返回 服ID -> 结果。mode 非 "real" 时不连网,全部按在线(7)返回。
func (p *StatusProber) Probe(mode string, servers []gameserver.Server) map[int]ProbeResult {
	out := make(map[int]ProbeResult, len(servers))
	if mode != "real" {
		for _, s := range servers {
			out[s.ID] = ProbeResult{Mask: 7}
		}
		return out
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, p.limit)
	)
	for _, s := range servers {
		if strings.TrimSpace(s.IP) == "" {
			out[s.ID] = ProbeResult{Mask: -1, Err: "无内网IP"}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(s gameserver.Server) {
			defer wg.Done()
			defer func() { <-sem }()
			r := p.probeOne(s)
			mu.Lock()
			out[s.ID] = r
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	return out
}

func (p *StatusProber) probeOne(s gameserver.Server) ProbeResult {
	noop := func(string) {}
	raw, err := p.runSSH(s.IP, buildStatusVersionCmd(p.cfg.RemoteScriptsDir, s.ID), noop)
	if err != nil {
		return ProbeResult{Mask: -1, Err: err.Error()}
	}
	maskPart, verPart := splitProbeOutput(raw)
	mask, perr := strconv.Atoi(strings.TrimSpace(maskPart))
	if perr != nil {
		return ProbeResult{Mask: -1, Err: "无法解析状态输出"}
	}
	r := ProbeResult{Mask: mask}
	// 版本是展示用,解析失败(包名不规范/未部署)静默留空,绝不连累状态判定。
	if big, tgt, e := parsePacketVersion(verPart); e == nil {
		r.Version = fmt.Sprintf("%s_%d", big, tgt)
	}
	return r
}

// splitProbeOutput 按 probeVerMarker 把探测输出拆成 状态段 与 包名段。
// 无标记(旧式只回状态的桩/输出)时整段当状态、版本段为空,保持向后兼容。
func splitProbeOutput(raw string) (maskPart, verPart string) {
	if i := strings.Index(raw, probeVerMarker); i >= 0 {
		return raw[:i], raw[i+len(probeVerMarker):]
	}
	return raw, ""
}

// runProbeSSH 同 RunSSH,但追加 ConnectTimeout=5:探活遇到死机不长时间挂住。
func runProbeSSH(cfg *RealConfig, ip, remoteCmd string) (string, error) {
	if cfg.SSHMultiplex {
		_ = os.MkdirAll(sshControlDir(), 0700)
	}
	args := append([]string{"-o", "ConnectTimeout=5"}, buildSSHArgs(cfg, ip, remoteCmd)...)
	return runCmd(exec.Command("ssh", args...), func(string) {})
}
