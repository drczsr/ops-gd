package workflow

import "testing"

func TestStatusLabel(t *testing.T) {
	if got := StatusLabel(StatusPendingApprove); got != "待审批" {
		t.Errorf("pending_approve => %q, want 待审批", got)
	}
	if got := StatusLabel(StatusOpened); got != "已开放" {
		t.Errorf("opened => %q, want 已开放", got)
	}
	// 未知状态原样返回,不丢信息
	if got := StatusLabel("weird_status"); got != "weird_status" {
		t.Errorf("未知状态应原样返回, got %q", got)
	}
}

func TestStatusStep(t *testing.T) {
	cases := map[string]int{
		StatusPendingApprove: 1, // 审批
		StatusExecuting:      2, // 执行
		StatusExecFailed:     2,
		StatusPendingTest:    3, // 测试
		StatusOpening:        4, // 开放
		StatusOpened:         5, // 全部完成
		StatusRejected:       -1, // 驳回:不显示进度条
		StatusCancelled:      -1, // 作废:不显示进度条
		"weird":              -1,
	}
	for st, want := range cases {
		if got := StatusStep(st); got != want {
			t.Errorf("StatusStep(%q) = %d, want %d", st, got, want)
		}
	}
}

func TestMainStages(t *testing.T) {
	s := MainStages()
	if len(s) != 5 {
		t.Fatalf("应有 5 个主阶段, got %d", len(s))
	}
	if s[0].Label != "提交" || s[4].Label != "开放" {
		t.Errorf("阶段标签错误: %+v", s)
	}
}

func TestStatusClass(t *testing.T) {
	cases := map[string]string{
		StatusOpened:         "text-bg-success",
		StatusExecuting:      "text-bg-primary",
		StatusPendingApprove: "text-bg-warning",
		StatusRejected:       "text-bg-danger",
		StatusExecFailed:     "text-bg-danger",
		"weird":              "text-bg-secondary", // 未知状态灰底
	}
	for in, want := range cases {
		if got := StatusClass(in); got != want {
			t.Errorf("StatusClass(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNextStatus(t *testing.T) {
	cases := []struct {
		from   string
		action string
		want   string
		ok     bool
	}{
		{StatusPendingApprove, ActionApprove, StatusPendingExecute, true},
		{StatusPendingApprove, ActionReject, StatusRejected, true},
		{StatusRejected, ActionResubmit, StatusPendingApprove, true},
		{StatusPendingExecute, ActionExecute, StatusExecuting, true},
		{StatusExecuting, ActionExecSuccess, StatusPendingTest, true},
		{StatusExecuting, ActionExecFail, StatusExecFailed, true},
		{StatusExecFailed, ActionExecute, StatusExecuting, true}, // 重试
		{StatusPendingTest, ActionTestPass, StatusPendingOpen, true},
		{StatusPendingTest, ActionTestFail, StatusPendingExecute, true},
		{StatusPendingOpen, ActionOpen, StatusOpening, true},
		{StatusPendingApprove, ActionCancel, StatusCancelled, true},
		// 非法流转
		{StatusOpened, ActionApprove, "", false},
		{StatusPendingApprove, ActionExecute, "", false},
	}
	for _, c := range cases {
		got, ok := NextStatus(c.from, c.action)
		if ok != c.ok || got != c.want {
			t.Errorf("NextStatus(%q,%q) = (%q,%v), want (%q,%v)",
				c.from, c.action, got, ok, c.want, c.ok)
		}
	}
}

func TestRequiredRole(t *testing.T) {
	if RequiredRole(ActionApprove) != RoleApprove {
		t.Error("approve 应需要 approve 角色")
	}
	if RequiredRole(ActionExecute) != RoleExecute {
		t.Error("execute 应需要 execute 角色")
	}
	if RequiredRole(ActionTestPass) != RoleTest {
		t.Error("testPass 应需要 test 角色")
	}
	if RequiredRole(ActionResubmit) != RoleSubmit {
		t.Error("resubmit 应需要 submit 角色")
	}
}

func TestTeardownTransitionAndRole(t *testing.T) {
	to, ok := NextStatus(StatusExecFailed, ActionTeardown)
	if !ok || to != StatusExecuting {
		t.Fatalf("ExecFailed + teardown 应允许并进中间态, got %q ok=%v", to, ok)
	}
	if _, ok := NextStatus(StatusOpened, ActionTeardown); ok {
		t.Error("已完成单不应允许 teardown")
	}
	if RequiredRole(ActionTeardown) != RoleExecute {
		t.Errorf("teardown 角色应为 execute, got %q", RequiredRole(ActionTeardown))
	}
}

func TestStageOf(t *testing.T) {
	if StageOf(StatusPendingApprove) != StageApprove {
		t.Error("pending_approve 应属审批阶段")
	}
	if StageOf(StatusExecuting) != StageExecute {
		t.Error("executing 应属执行阶段")
	}
}

func TestOpenStageTransitions(t *testing.T) {
	cases := []struct {
		from, action, want string
		ok                 bool
	}{
		{StatusPendingOpen, ActionOpen, StatusOpening, true},
		{StatusOpening, ActionOpenSuccess, StatusOpened, true},
		{StatusOpening, ActionOpenFail, StatusOpenFailed, true},
		{StatusOpenFailed, ActionOpen, StatusOpening, true}, // 重试
		{StatusOpening, ActionOpen, "", false},              // 非法
	}
	for _, c := range cases {
		got, ok := NextStatus(c.from, c.action)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("NextStatus(%s,%s)=(%q,%v) want (%q,%v)", c.from, c.action, got, ok, c.want, c.ok)
		}
	}
	if StageOf(StatusOpening) != StageOpen || StageOf(StatusOpenFailed) != StageOpen {
		t.Error("opening/open_failed 应属 open 阶段")
	}
	if RequiredRole(ActionOpenSuccess) != RoleSystem {
		t.Error("openSuccess 应为系统角色")
	}
}
