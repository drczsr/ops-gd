package executor

import (
	"fmt"
	"strings"
	"testing"

	"gongdan/internal/gameserver"
)

func TestBuildDropDBCmd(t *testing.T) {
	c := gameserver.DBConn{User: "root", Pwd: "pw", IP: "g-rds", Port: "3306"}
	got := buildDropDBCmd(c, "ptdb_10002")
	for _, want := range []string{"mysql ", "-hg-rds", "-P3306", "-uroot", "-ppw", "DROP DATABASE IF EXISTS ptdb_10002"} {
		if !strings.Contains(got, want) {
			t.Errorf("buildDropDBCmd 缺片段 %q: %s", want, got)
		}
	}
}

func TestBuildRemovePackageCmd(t *testing.T) {
	if got := buildRemovePackageCmd(10002); got != "rm -rf /export/server/server_10002" {
		t.Errorf("buildRemovePackageCmd = %q", got)
	}
}

func TestBuildStopCmd(t *testing.T) {
	got := buildStopCmd(10001)
	want := "cd /export/server/server_10001/OperationalTools && ./gameserver_stop.py -worldid=10001"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestBuildKillCmd(t *testing.T) {
	got := buildKillCmd("/export/packages/scripts", 10001)
	if got != "cd /export/packages/scripts && ./kill.sh 10001" {
		t.Errorf("got %q", got)
	}
}

func TestBuildRepairCmd(t *testing.T) {
	got := buildRepairCmd("/export/packages/scripts", 10001)
	if got != "cd /export/packages/scripts && ./repair.sh 10001 -worldid=10001 > /tmp/start_10001.log 2>&1" {
		t.Errorf("got %q", got)
	}
}

func TestBuildMaintenanceCmd(t *testing.T) {
	name, args := buildMaintenanceCmd("/export/op/hefu", []int{10001, 10002})
	if name != "bash" {
		t.Errorf("name=%q", name)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "cd /export/op/hefu") ||
		!strings.Contains(joined, "./update_serverlist_stat.sh 0 10001,10002") {
		t.Errorf("args=%v", args)
	}
}

func TestIsStopOK(t *testing.T) {
	if !isStopOK(nil) {
		t.Error("exit0(nil) 应为成功")
	}
	if !isStopOK(fmt.Errorf("exit status 104")) {
		t.Error("104 应为成功")
	}
	if isStopOK(fmt.Errorf("exit status 1")) {
		t.Error("其它退出码应为失败")
	}
}

func TestParsePacketVersion(t *testing.T) {
	// 真实格式
	big, tgt, err := parsePacketVersion("ProjectT_Release16.0-All_9_1_426_202605281817_6618_76255")
	if err != nil {
		t.Fatal(err)
	}
	if big != "9_1" || tgt != 426 {
		t.Errorf("真实格式: big=%q tgt=%d, want 9_1 426", big, tgt)
	}

	// 前缀段数变化:仍靠时间戳锚定到 9_1_426
	big, tgt, err = parsePacketVersion("Foo_Bar_Baz_Qux_9_1_426_202601020304")
	if err != nil {
		t.Fatal(err)
	}
	if big != "9_1" || tgt != 426 {
		t.Errorf("前缀变化: big=%q tgt=%d, want 9_1 426", big, tgt)
	}

	// fail-closed:无 ≥8 位时间戳
	if _, _, err := parsePacketVersion("ProjectT_All_9_1_426"); err == nil {
		t.Error("无长时间戳应报错")
	}
	// fail-closed:时间戳被分隔不连续(2026 仅4位)
	if _, _, err := parsePacketVersion("ProjectT_All_9_1_426_2026_01_02"); err == nil {
		t.Error("不连续时间戳应报错")
	}
	// fail-closed:完全不符
	if _, _, err := parsePacketVersion("weird-name.zip"); err == nil {
		t.Error("不符格式应报错")
	}
}

func TestDBUpgradeSteps(t *testing.T) {
	steps := dbUpgradeSteps("9_1", 424, 426)
	want := []string{"9_1_424_to_9_1_425", "9_1_425_to_9_1_426"}
	if len(steps) != 2 || steps[0] != want[0] || steps[1] != want[1] {
		t.Errorf("steps = %v, want %v", steps, want)
	}
	if len(dbUpgradeSteps("9_1", 426, 426)) != 0 {
		t.Error("相等应无步骤")
	}
}

func TestBuildUpdateDBCmd(t *testing.T) {
	c := gameserver.DBConn{IP: "10.0.0.9", Port: "3306", User: "u1", Pwd: "p1"}
	got := buildUpdateDBCmd(10001, c, "9_1_424_to_9_1_425")
	want := "cd /export/server/server_10001/SqlScript/UpdateDB && bash ./Update_DB.sh -worldid=10001 -dbuser=u1 -dbpwd=p1 -dbip=10.0.0.9 -dbport=3306 -version=9_1_424_to_9_1_425"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestBuildConfigDownloadCmd(t *testing.T) {
	got := buildConfigDownloadCmd("/export/packages/scripts", 10001)
	want := "cd /export/packages/scripts && ./gd_download.sh 10001"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestBuildPullConfigCmd(t *testing.T) {
	got := buildPullConfigCmd("/export/packages/scripts", 10001)
	want := "cd /export/packages/scripts && ./gd_pull_cnf.sh 10001"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestBuildGMHotUpdateCmd(t *testing.T) {
	name, args := buildGMHotUpdateCmd("/export/packages/scripts/gd_gmhot.sh")
	if name != "bash" || len(args) != 1 || args[0] != "/export/packages/scripts/gd_gmhot.sh" {
		t.Errorf("name=%q args=%v", name, args)
	}
}

func TestBuildLoginLimitCmd(t *testing.T) {
	got := buildLoginLimitCmd("/export/packages/scripts", 235, 0)
	want := "cd /export/packages/scripts && ./limit.sh 235 -worldid=235 -limit=0"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestBuildRestoreStatusCmd(t *testing.T) {
	name, args := buildRestoreStatusCmd("/export/op/hefu", []int{10001, 10002})
	if name != "bash" {
		t.Errorf("name=%q", name)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "cd /export/op/hefu") ||
		!strings.Contains(joined, "./update_serverlist_stat.sh 1 10001,10002") {
		t.Errorf("args=%v", args)
	}
}

func TestBuildCreateDBCmds(t *testing.T) {
	probe := buildCreateDBVersionProbeCmd(10947)
	if !strings.Contains(probe, "server_10947/SqlScript/CreateDB/PTDBInit_") {
		t.Errorf("probe = %q", probe)
	}
	c := gameserver.DBConn{User: "root", Pwd: "pw", IP: "10.0.0.9", Port: "3306"}
	cmd := buildCreateDBCmd(10947, c, "9_1_426")
	for _, want := range []string{"CreateDB", "Create_DB.sh", "-worldid=10947",
		"-dbuser=root", "-dbpwd=pw", "-dbip=10.0.0.9", "-dbport=3306", "-version=9_1_426"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("cmd 缺 %q: %s", want, cmd)
		}
	}
}

func TestPickCreateDBVersion(t *testing.T) {
	// 多版本目录:必须按包版本精确选,不取第一个
	out := "/export/server/server_10947/SqlScript/CreateDB/PTDBInit_10_0_1/\n" +
		"/export/server/server_10947/SqlScript/CreateDB/PTDBInit_9_1_426/\n"
	v, err := pickCreateDBVersion(out, "9_1_426")
	if err != nil || v != "9_1_426" {
		t.Fatalf("应精确选中包版本 9_1_426, got %q err=%v", v, err)
	}
	// 包版本在目录里不存在 -> fail-closed
	if _, err := pickCreateDBVersion(out, "9_1_999"); err == nil {
		t.Errorf("包内无一致版本应报错,而不是猜一个")
	}
	if _, err := pickCreateDBVersion("no match", "9_1_426"); err == nil {
		t.Errorf("无任何 PTDBInit 目录应报错")
	}
}
