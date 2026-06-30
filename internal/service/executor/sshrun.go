package executor

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// streamLines 按行读取 r,每行回传给 log;若 sb 非 nil,同时收集到 sb。
func streamLines(r io.Reader, log LogFunc, sb *strings.Builder) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if sb != nil {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		log(line)
	}
}

// runCmd 启动命令,stdout 流式回传并收集、stderr 仅流式回传,返回收集到的 stdout 与退出错误。
func runCmd(cmd *exec.Cmd, log LogFunc) (string, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}

	var sb strings.Builder
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); streamLines(stdout, log, &sb) }()
	go func() { defer wg.Done(); streamLines(stderr, log, nil) }()
	wg.Wait()

	return sb.String(), cmd.Wait()
}

// sshControlDir 连接复用的 socket 存放目录。
func sshControlDir() string {
	return filepath.Join(os.TempDir(), "gongdan-ssh")
}

// buildSSHArgs 组装 ssh 命令行参数。
// 开启复用(cfg.SSHMultiplex)时追加 ControlMaster 选项:对同一主机的多次调用
// 共用一条 TCP 通道(只握手/认证一次),大幅降低并发连接数、缓解 sshd MaxStartups 限流。
func buildSSHArgs(cfg *RealConfig, ip, remoteCmd string) []string {
	args := []string{
		"-n",
		"-p", strconv.Itoa(cfg.SSHPort),
		"-i", cfg.SSHKey,
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
	}
	if cfg.SSHMultiplex {
		// %C = 连接四元组的哈希,短且唯一,避免 socket 路径过长
		args = append(args,
			"-o", "ControlMaster=auto",
			"-o", "ControlPath="+filepath.Join(sshControlDir(), "cm-%C"),
			"-o", "ControlPersist=60s",
		)
	}
	args = append(args, fmt.Sprintf("%s@%s", cfg.SSHUser, ip), remoteCmd)
	return args
}

// RunSSH 通过系统 ssh 在远程主机执行命令。
func RunSSH(cfg *RealConfig, ip, remoteCmd string, log LogFunc) (string, error) {
	if cfg.SSHMultiplex {
		_ = os.MkdirAll(sshControlDir(), 0700) // 复用 socket 目录
	}
	args := buildSSHArgs(cfg, ip, remoteCmd)
	log(fmt.Sprintf("$ ssh %s@%s %q", cfg.SSHUser, ip, remoteCmd))
	return runCmd(exec.Command("ssh", args...), log)
}

// RunLocal 在本机执行命令(用于 merge.sh);env 为附加环境变量(KEY=VALUE)。
func RunLocal(name string, args, env []string, log LogFunc) (string, error) {
	cmd := exec.Command(name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	log(fmt.Sprintf("$ %s %s", name, strings.Join(args, " ")))
	return runCmd(cmd, log)
}
