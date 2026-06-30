package executor

import (
	"strings"
	"testing"
)

func TestStreamLines(t *testing.T) {
	var logged []string
	var sb strings.Builder
	streamLines(strings.NewReader("line1\nline2\nline3\n"), func(s string) {
		logged = append(logged, s)
	}, &sb)

	if len(logged) != 3 || logged[0] != "line1" || logged[2] != "line3" {
		t.Errorf("logged = %v, want [line1 line2 line3]", logged)
	}
	if sb.String() != "line1\nline2\nline3\n" {
		t.Errorf("collected = %q", sb.String())
	}
}

func TestStreamLinesNilBuilder(t *testing.T) {
	var n int
	streamLines(strings.NewReader("a\nb\n"), func(string) { n++ }, nil)
	if n != 2 {
		t.Errorf("行数 = %d, want 2", n)
	}
}

func argsContain(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestBuildSSHArgsBasic(t *testing.T) {
	cfg := &RealConfig{SSHPort: 7722, SSHKey: "/k", SSHUser: "root"}
	args := buildSSHArgs(cfg, "1.2.3.4", "hostname")
	j := strings.Join(args, " ")

	for _, want := range []string{"-n", "StrictHostKeyChecking=no", "BatchMode=yes", "root@1.2.3.4", "hostname"} {
		if !argsContain(args, want) {
			t.Errorf("缺少参数 %q, got: %s", want, j)
		}
	}
	if strings.Contains(j, "ControlMaster") {
		t.Errorf("未开复用却出现 ControlMaster: %s", j)
	}
	if args[len(args)-1] != "hostname" {
		t.Errorf("远程命令应为最后一个参数, got: %s", j)
	}
}

func TestBuildSSHArgsMultiplex(t *testing.T) {
	cfg := &RealConfig{SSHPort: 7722, SSHKey: "/k", SSHUser: "root", SSHMultiplex: true}
	args := buildSSHArgs(cfg, "1.2.3.4", "hostname")
	j := strings.Join(args, " ")

	for _, want := range []string{"ControlMaster=auto", "ControlPersist=60s"} {
		if !strings.Contains(j, want) {
			t.Errorf("开复用应包含 %q, got: %s", want, j)
		}
	}
	if !strings.Contains(j, "ControlPath=") || !strings.Contains(j, "cm-%C") {
		t.Errorf("应设置 ControlPath=.../cm-%%C, got: %s", j)
	}
	if args[len(args)-1] != "hostname" {
		t.Errorf("远程命令应为最后一个参数, got: %s", j)
	}
}
