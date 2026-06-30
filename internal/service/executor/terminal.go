package executor

import (
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHShell 一个已建立的交互式 SSH PTY 会话(供 Web 终端桥接用)。
// Stdin 写入用户键盘输入,Stdout 读取远端输出(已合并 stderr)。
type SSHShell struct {
	client  *ssh.Client
	session *ssh.Session
	Stdin   io.WriteCloser
	Stdout  io.Reader
}

// Resize 调整远端 PTY 窗口大小。
func (s *SSHShell) Resize(rows, cols int) error {
	return s.session.WindowChange(rows, cols)
}

// Close 关闭会话与连接。
func (s *SSHShell) Close() error {
	if s.session != nil {
		_ = s.session.Close()
	}
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// OpenSSHShell 用执行器的私钥连到 ip:SSHPort,开一个交互式 shell(PTY)。
// 与现有探测/执行一致:忽略 HostKey(StrictHostKeyChecking=no)、复用 root@端口配置。
func OpenSSHShell(cfg *RealConfig, ip string, rows, cols int) (*SSHShell, error) {
	keyBytes, err := os.ReadFile(cfg.SSHKey)
	if err != nil {
		return nil, fmt.Errorf("读取私钥失败: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("解析私钥失败: %w", err)
	}
	port := cfg.SSHPort
	if port == 0 {
		port = 22
	}
	clientCfg := &ssh.ClientConfig{
		User:            cfg.SSHUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", ip, port)
	client, err := ssh.Dial("tcp", addr, clientCfg)
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败: %w", addr, err)
	}
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("建立会话失败: %w", err)
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if rows <= 0 {
		rows = 24
	}
	if cols <= 0 {
		cols = 80
	}
	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("请求 PTY 失败: %w", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, err
	}
	// stdout/stderr 合并到一个管道,供桥接侧顺序读取
	pr, pw := io.Pipe()
	session.Stdout = pw
	session.Stderr = pw
	if err := session.Shell(); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("启动 shell 失败: %w", err)
	}
	// 会话结束后关闭写端,让读端 EOF
	go func() {
		_ = session.Wait()
		_ = pw.Close()
	}()
	return &SSHShell{client: client, session: session, Stdin: stdin, Stdout: pr}, nil
}
