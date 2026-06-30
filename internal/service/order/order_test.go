package order

import (
	"testing"
	"time"

	"gongdan/internal/db"
	"gongdan/internal/model"
	"gongdan/internal/service/executor"
	"gongdan/internal/service/workflow"
	"gongdan/internal/settings"

	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := db.Init("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("db init: %v", err)
	}
	// 清掉可能复用的内存库残留
	gdb.Exec("DELETE FROM work_orders")
	gdb.Exec("DELETE FROM work_order_logs")
	return gdb
}

// waitStatus 轮询等待工单到达期望终态(沿用本文件其它异步测试的轮询写法)。
func waitStatus(t *testing.T, svc *Service, id uint, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		got, _ := svc.Get(id)
		if got.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := svc.Get(id)
	t.Fatalf("工单未在 %s 内到达 %q,实际 %q", timeout, want, got.Status)
}

func mockStore(t *testing.T) *settings.Store {
	t.Helper()
	st, err := settings.NewStore(settings.Settings{Mode: "mock"}, "")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestCreateAndApproveFlow(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{210}, VersionPackage: "v.zip"})
	wo, err := svc.Create(model.TypeRelease, "发版210", "210", params, "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if wo.Status != workflow.StatusPendingApprove {
		t.Errorf("status = %q, want pending_approve", wo.Status)
	}
	if wo.OrderNo == "" {
		t.Error("应生成工单编号")
	}

	// 提交人不能审批自己的单
	if err := svc.Act(wo.ID, workflow.ActionApprove, "alice", ""); err == nil {
		t.Error("提交人审批自己的单应失败")
	}

	// 他人审批通过
	if err := svc.Act(wo.ID, workflow.ActionApprove, "bob", "同意"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	got, _ := svc.Get(wo.ID)
	if got.Status != workflow.StatusPendingExecute {
		t.Errorf("status = %q, want pending_execute", got.Status)
	}
	if got.ApproveBy != "bob" {
		t.Errorf("approveBy = %q", got.ApproveBy)
	}

	// 审计日志:至少有 create + approve 两条 action
	var n int64
	gdb.Model(&model.WorkOrderLog{}).
		Where("order_id = ? AND log_type = ?", wo.ID, model.LogAction).Count(&n)
	if n < 2 {
		t.Errorf("action 日志数 = %d, want >=2", n)
	}
}

// 管理员豁免"提交人不能审批自己工单":admin 可审批自己提交的单,
// 普通用户(alice)仍不能审批自己的单。
func TestAdminCanApproveOwnOrder(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{210}, VersionPackage: "v.zip"})

	// admin 自审自己的单 -> 允许
	woAdmin, _ := svc.Create(model.TypeRelease, "admin的单", "210", params, model.AdminUsername)
	if err := svc.Act(woAdmin.ID, workflow.ActionApprove, model.AdminUsername, ""); err != nil {
		t.Fatalf("管理员应能审批自己的工单: %v", err)
	}
	got, _ := svc.Get(woAdmin.ID)
	if got.Status != workflow.StatusPendingExecute {
		t.Errorf("status = %q, want pending_execute", got.Status)
	}

	// 普通用户自审自己的单 -> 拒绝
	woAlice, _ := svc.Create(model.TypeRelease, "alice的单", "210", params, "alice")
	if err := svc.Act(woAlice.ID, workflow.ActionApprove, "alice", ""); err == nil {
		t.Error("普通用户审批自己的工单应失败")
	}
}

func TestLogsAfterReturnsIncrementalRows(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)

	wo := &model.WorkOrder{
		OrderNo:    "WO-LOGS-001",
		Type:       model.TypeRelease,
		Title:      "logs",
		Status:     workflow.StatusExecuting,
		CreateBy:   "alice",
		CreateTime: time.Now(),
		UpdateTime: time.Now(),
	}
	if err := gdb.Create(wo).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	rows := []model.WorkOrderLog{
		{OrderID: wo.ID, LogType: model.LogExec, Stage: workflow.StageExecute, Operator: "system", Content: "line-1", CreateTime: time.Now()},
		{OrderID: wo.ID, LogType: model.LogExec, Stage: workflow.StageExecute, Operator: "system", Content: "line-2", CreateTime: time.Now()},
		{OrderID: wo.ID, LogType: model.LogExec, Stage: workflow.StageExecute, Operator: "system", Content: "line-3", CreateTime: time.Now()},
	}
	for i := range rows {
		if err := gdb.Create(&rows[i]).Error; err != nil {
			t.Fatalf("create log %d: %v", i, err)
		}
	}

	got, err := svc.LogsAfter(wo.ID, rows[0].ID)
	if err != nil {
		t.Fatalf("LogsAfter: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("LogsAfter len=%d, want 2", len(got))
	}
	if got[0].ID != rows[1].ID || got[1].ID != rows[2].ID {
		t.Fatalf("LogsAfter ids=%v,%v want %v,%v", got[0].ID, got[1].ID, rows[1].ID, rows[2].ID)
	}
}

func TestCancelOnlyOwnerOrAdmin(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{210}, VersionPackage: "v.zip"})

	// bob 不是创建人也不是管理员 -> 不能作废
	wo1, _ := svc.Create(model.TypeRelease, "alice的工单", "210", params, "alice")
	_ = svc.Act(wo1.ID, workflow.ActionApprove, "bob", "")
	if err := svc.Act(wo1.ID, workflow.ActionCancel, "bob", ""); err == nil {
		t.Fatal("非创建人且非管理员作废应失败")
	}
	got1, _ := svc.Get(wo1.ID)
	if got1.Status != workflow.StatusPendingExecute {
		t.Fatalf("作废失败后状态不应变化, got %s", got1.Status)
	}

	// 创建人本人 -> 可以作废
	if err := svc.Act(wo1.ID, workflow.ActionCancel, "alice", ""); err != nil {
		t.Fatalf("创建人作废应成功: %v", err)
	}
	got2, _ := svc.Get(wo1.ID)
	if got2.Status != workflow.StatusCancelled {
		t.Fatalf("创建人作废后状态应为 cancelled, got %s", got2.Status)
	}

	// admin -> 可以作废他人工单
	wo2, _ := svc.Create(model.TypeRelease, "alice第二单", "210", params, "alice")
	_ = svc.Act(wo2.ID, workflow.ActionApprove, "bob", "")
	if err := svc.Act(wo2.ID, workflow.ActionCancel, model.AdminUsername, ""); err != nil {
		t.Fatalf("管理员作废他人工单应成功: %v", err)
	}
	got3, _ := svc.Get(wo2.ID)
	if got3.Status != workflow.StatusCancelled {
		t.Fatalf("管理员作废后状态应为 cancelled, got %s", got3.Status)
	}
}

func TestResubmitOnlyOwnerOrAdmin(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{210}, VersionPackage: "v.zip"})

	// 先造一个已驳回工单
	wo1, _ := svc.Create(model.TypeRelease, "alice的驳回单", "210", params, "alice")
	_ = svc.Act(wo1.ID, workflow.ActionReject, "bob", "")

	// bob 不是创建人也不是管理员 -> 不能重新提交
	if err := svc.Act(wo1.ID, workflow.ActionResubmit, "bob", ""); err == nil {
		t.Fatal("非创建人且非管理员重新提交应失败")
	}
	got1, _ := svc.Get(wo1.ID)
	if got1.Status != workflow.StatusRejected {
		t.Fatalf("重新提交失败后状态不应变化, got %s", got1.Status)
	}

	// 创建人本人 -> 可以重新提交
	if err := svc.Act(wo1.ID, workflow.ActionResubmit, "alice", ""); err != nil {
		t.Fatalf("创建人重新提交应成功: %v", err)
	}
	got2, _ := svc.Get(wo1.ID)
	if got2.Status != workflow.StatusPendingApprove {
		t.Fatalf("创建人重新提交后状态应为 pending_approve, got %s", got2.Status)
	}

	// admin -> 可以代为重新提交
	wo2, _ := svc.Create(model.TypeRelease, "alice第二个驳回单", "210", params, "alice")
	_ = svc.Act(wo2.ID, workflow.ActionReject, "bob", "")
	if err := svc.Act(wo2.ID, workflow.ActionResubmit, model.AdminUsername, ""); err != nil {
		t.Fatalf("管理员代为重新提交应成功: %v", err)
	}
	got3, _ := svc.Get(wo2.ID)
	if got3.Status != workflow.StatusPendingApprove {
		t.Fatalf("管理员重新提交后状态应为 pending_approve, got %s", got3.Status)
	}
}

// 模拟服务重启:同一数据库换一个新的 Service 实例(内存计数器归零),
// 当天已有工单时不应再撞号。
func TestOrderNoSurvivesRestart(t *testing.T) {
	gdb := newTestDB(t)
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{210}, VersionPackage: "v.zip"})

	svc1 := New(gdb, mockStore(t), nil)
	wo1, err := svc1.Create(model.TypeRelease, "发版1", "210", params, "alice")
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}

	// 新建 Service 模拟进程重启(seq 回到 0)
	svc2 := New(gdb, mockStore(t), nil)
	wo2, err := svc2.Create(model.TypeRelease, "发版2", "210", params, "alice")
	if err != nil {
		t.Fatalf("create after restart: %v", err)
	}

	if wo1.OrderNo == wo2.OrderNo {
		t.Errorf("重启后工单号撞号: %s == %s", wo1.OrderNo, wo2.OrderNo)
	}
}

func TestExecuteAsyncMockSuccess(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	svc.execDelay = 0 // 加速

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{1}, VersionPackage: "v.zip"})
	wo, _ := svc.Create(model.TypeRelease, "t", "1", params, "alice")
	svc.Act(wo.ID, workflow.ActionApprove, "bob", "")

	// 触发执行(异步)
	if err := svc.Act(wo.ID, workflow.ActionExecute, "carol", ""); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// 轮询等待异步完成(最多 3 秒)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := svc.Get(wo.ID)
		if got.Status == workflow.StatusPendingTest {
			// 应有 exec 日志
			var n int64
			gdb.Model(&model.WorkOrderLog{}).
				Where("order_id = ? AND log_type = ?", wo.ID, model.LogExec).Count(&n)
			if n == 0 {
				t.Error("应写入 exec 日志")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("异步执行未在超时内进入 pending_test")
}

// 假执行器:实现 Execute(no-op)+ FailedTargets,用于测试写回逻辑。
type fakeFailExec struct {
	failed    []int
	hotFailed []string
}

func (f *fakeFailExec) Execute(wo *model.WorkOrder, log executor.LogFunc) error { return nil }
func (f *fakeFailExec) FailedTargets() []int                                    { return f.failed }
func (f *fakeFailExec) HotFailedHosts() []string                                { return f.hotFailed }

// 不实现 FailedTargets 的执行器(模拟合服/热更/mock)。
type fakePlainExec struct{}

func (f *fakePlainExec) Execute(wo *model.WorkOrder, log executor.LogFunc) error { return nil }

func TestApplyLastFailedWritesSubset(t *testing.T) {
	params, _ := model.MarshalParams(model.ReleaseParams{
		ServerIDs: []int{10001, 10002, 10003}, VersionPackage: "v.zip",
	})
	out := applyLastFailed(model.TypeRelease, params, &fakeFailExec{failed: []int{10002, 10003}})
	got, _ := model.UnmarshalReleaseParams(out)
	if len(got.LastFailedIDs) != 2 || got.LastFailedIDs[0] != 10002 || got.LastFailedIDs[1] != 10003 {
		t.Errorf("LastFailedIDs = %v, want [10002 10003]", got.LastFailedIDs)
	}
	// 原始 ServerIDs 不被改动
	if len(got.ServerIDs) != 3 {
		t.Errorf("ServerIDs 不应被改动, got %v", got.ServerIDs)
	}
}

func TestApplyLastFailedClearsOnSuccess(t *testing.T) {
	params, _ := model.MarshalParams(model.ReleaseParams{
		ServerIDs: []int{10001}, VersionPackage: "v.zip", LastFailedIDs: []int{10001},
	})
	out := applyLastFailed(model.TypeRelease, params, &fakeFailExec{failed: nil}) // 全成功
	got, _ := model.UnmarshalReleaseParams(out)
	if got.LastFailedIDs != nil {
		t.Errorf("全成功应清空 LastFailedIDs, got %v", got.LastFailedIDs)
	}
}

func TestApplyLastFailedIgnoresNonReporter(t *testing.T) {
	params := `{"some":"merge-params"}`
	out := applyLastFailed(model.TypeRelease, params, &fakePlainExec{})
	if out != params {
		t.Errorf("非发版执行器应原样返回, got %q", out)
	}
}

func TestApplyLastFailedWritesHotHosts(t *testing.T) {
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{10001}, VersionPackage: "v.zip"})
	out := applyLastFailed(model.TypeRelease, params, &fakeFailExec{failed: nil, hotFailed: []string{"10.0.0.9"}})
	got, _ := model.UnmarshalReleaseParams(out)
	if len(got.HotFailedHosts) != 1 || got.HotFailedHosts[0] != "10.0.0.9" {
		t.Errorf("HotFailedHosts = %v, want [10.0.0.9]", got.HotFailedHosts)
	}
	if got.LastFailedIDs != nil {
		t.Errorf("LastFailedIDs 应为空, got %v", got.LastFailedIDs)
	}
}

// fakeHotExec 仅实现 FailedTargets,模拟热更执行器报告失败服。
type fakeHotExec struct{ failed []int }

func (f *fakeHotExec) Execute(*model.WorkOrder, executor.LogFunc) error { return nil }
func (f *fakeHotExec) FailedTargets() []int                             { return f.failed }

func TestApplyLastFailedHotupdate(t *testing.T) {
	in := model.HotupdateParams{
		ServerIDs: []int{210, 211}, ConfigPackage: "cfg.zip", HotFiles: "a.ini", IncludeBattle: true,
	}
	params, _ := model.MarshalParams(in)

	out := applyLastFailed(model.TypeHotupdate, params, &fakeHotExec{failed: []int{211}})

	got, err := model.UnmarshalHotupdateParams(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.LastFailedIDs) != 1 || got.LastFailedIDs[0] != 211 {
		t.Errorf("LastFailedIDs=%v, want [211]", got.LastFailedIDs)
	}
	// 关键:热更专有字段不能丢
	if got.ConfigPackage != "cfg.zip" || got.HotFiles != "a.ini" || !got.IncludeBattle {
		t.Errorf("热更参数字段丢失: %+v", got)
	}
}

func TestRunOpenMockSucceeds(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	svc.execDelay = 0
	wo, _ := svc.Create(model.TypeRelease, "t", "", `{}`, "alice")
	gdb.Model(wo).Update("status", workflow.StatusOpening)
	svc.runOpen(wo.ID)
	got, _ := svc.Get(wo.ID)
	if got.Status != workflow.StatusOpened {
		t.Errorf("mock 开放应成功置 opened, 实际 %s", got.Status)
	}
	if got.OpenTime == nil {
		t.Error("应记录 OpenTime")
	}
}

func TestHotupdateExecSuccessGoesOpened(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	svc.execDelay = 0
	wo, _ := svc.Create(model.TypeHotupdate, "t", "", `{}`, "alice")
	gdb.Model(wo).Update("status", workflow.StatusExecuting)
	svc.runExecutor(wo.ID)
	got, _ := svc.Get(wo.ID)
	if got.Status != workflow.StatusOpened {
		t.Errorf("热更执行成功应直接 opened, 实际 %s", got.Status)
	}
}

func TestReleaseExecSuccessGoesPendingTest(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	svc.execDelay = 0
	wo, _ := svc.Create(model.TypeRelease, "t", "", `{}`, "alice")
	gdb.Model(wo).Update("status", workflow.StatusExecuting)
	svc.runExecutor(wo.ID)
	got, _ := svc.Get(wo.ID)
	if got.Status != workflow.StatusPendingTest {
		t.Errorf("发版执行成功应进 pending_test, 实际 %s", got.Status)
	}
}

func TestAutoExecuteAfterApprove(t *testing.T) {
	gdb := newTestDB(t)
	stAuto, _ := settings.NewStore(settings.Settings{Mode: "mock", AutoExecute: true}, "")
	svc := New(gdb, stAuto, nil)
	svc.execDelay = 0
	wo, _ := svc.Create(model.TypeRelease, "t", "", `{}`, "alice")
	if err := svc.Act(wo.ID, workflow.ActionApprove, "bob", ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := svc.Get(wo.ID)
		if got.Status == workflow.StatusPendingTest {
			return // 审批后自动执行,mock 成功 → pending_test
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("auto_execute 开,审批后应在超时内自动执行至 pending_test")
}

func TestManualExecuteStaysPending(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil) // auto 默认 false
	wo, _ := svc.Create(model.TypeRelease, "t", "", `{}`, "alice")
	_ = svc.Act(wo.ID, workflow.ActionApprove, "bob", "")
	got, _ := svc.Get(wo.ID)
	if got.Status != workflow.StatusPendingExecute {
		t.Errorf("默认手动,审批后应停在 pending_execute, 实际 %s", got.Status)
	}
}

func TestScheduledNotAutoRunOnApprove(t *testing.T) {
	gdb := newTestDB(t)
	stAuto, _ := settings.NewStore(settings.Settings{Mode: "mock", AutoExecute: true}, "")
	svc := New(gdb, stAuto, nil)
	wo, _ := svc.Create(model.TypeRelease, "t", "", `{}`, "alice")
	future := time.Now().Add(time.Hour)
	gdb.Model(wo).Update("scheduled_time", future)
	_ = svc.Act(wo.ID, workflow.ActionApprove, "bob", "")
	got, _ := svc.Get(wo.ID)
	if got.Status != workflow.StatusPendingExecute {
		t.Errorf("有未来计划时间,审批后应等调度器,不立即跑, 实际 %s", got.Status)
	}
}

func TestManualOpenTriggersOpening(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	svc.execDelay = 0
	wo, _ := svc.Create(model.TypeRelease, "t", "", `{}`, "alice")
	gdb.Model(wo).Update("status", workflow.StatusPendingOpen)
	if err := svc.Act(wo.ID, workflow.ActionOpen, "carol", ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := svc.Get(wo.ID)
		if got.Status == workflow.StatusOpened { // mock 开放瞬时成功
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("开放应在超时内完成至 opened")
}

func TestSchedulerFiresDueOrder(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	svc.execDelay = 0
	wo, _ := svc.Create(model.TypeRelease, "t", "", `{}`, "alice")
	past := time.Now().Add(-time.Minute)
	gdb.Model(wo).Updates(map[string]any{"status": workflow.StatusPendingExecute, "scheduled_time": past})
	svc.scanDue()
	got, _ := svc.Get(wo.ID)
	if got.Status == workflow.StatusPendingExecute {
		t.Error("到点的待执行单应被调度器触发,离开 pending_execute")
	}
}

func TestSchedulerSkipsFutureAndUnapproved(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	svc.execDelay = 0
	w1, _ := svc.Create(model.TypeRelease, "t", "", `{}`, "alice")
	gdb.Model(w1).Updates(map[string]any{"status": workflow.StatusPendingExecute, "scheduled_time": time.Now().Add(time.Hour)})
	w2, _ := svc.Create(model.TypeRelease, "t", "", `{}`, "alice")
	gdb.Model(w2).Update("scheduled_time", time.Now().Add(-time.Hour))
	svc.scanDue()
	g1, _ := svc.Get(w1.ID)
	g2, _ := svc.Get(w2.ID)
	if g1.Status != workflow.StatusPendingExecute {
		t.Error("未来计划不应触发")
	}
	if g2.Status != workflow.StatusPendingApprove {
		t.Error("未审批不应触发")
	}
}

// 在线把 store 的 auto_execute 打开后,审批通过应触发自动执行(证明 Service 实时读 store)。
func TestStoreAutoExecuteAppliesLive(t *testing.T) {
	gdb := newTestDB(t)
	st, _ := settings.NewStore(settings.Settings{Mode: "mock"}, "")
	svc := New(gdb, st, nil)
	svc.execDelay = 0

	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{210}, VersionPackage: "v.zip"})
	wo, _ := svc.Create(model.TypeRelease, "t", "210", params, "alice")

	if err := st.Update(settings.Settings{Mode: "mock", AutoExecute: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Act(wo.ID, workflow.ActionApprove, "bob", ""); err != nil {
		t.Fatalf("approve: %v", err)
	}

	var n int64
	gdb.Model(&model.WorkOrderLog{}).
		Where("order_id = ? AND content LIKE ?", wo.ID, "%自动执行%").Count(&n)
	if n == 0 {
		t.Error("store 开启 auto_execute 后审批应写入自动执行日志")
	}
}

type fakeReleaseExec struct{ cfgFailed []int }

func (f fakeReleaseExec) Execute(*model.WorkOrder, executor.LogFunc) error { return nil }
func (f fakeReleaseExec) FailedTargets() []int                             { return nil }
func (f fakeReleaseExec) HotFailedHosts() []string                         { return nil }
func (f fakeReleaseExec) ConfigFailedTargets() []int                       { return f.cfgFailed }

func TestApplyLastFailedWritesConfigFailed(t *testing.T) {
	params, _ := model.MarshalParams(model.ReleaseParams{ServerIDs: []int{10001, 10002}, VersionPackage: "v.zip"})
	out := applyLastFailed(model.TypeRelease, params, fakeReleaseExec{cfgFailed: []int{10002}})
	got, _ := model.UnmarshalReleaseParams(out)
	if len(got.ConfigFailedIDs) != 1 || got.ConfigFailedIDs[0] != 10002 {
		t.Errorf("ConfigFailedIDs=%v want [10002]", got.ConfigFailedIDs)
	}
}

func TestCreateAndExecuteNewServerCompletes(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	svc.execDelay = 0 // 加速 mock 步进

	p := model.NewServerCreateParams{RegionName: "洛阳",
		Rows: []model.NewServerRow{{Kind: "game", ID: 10002, Fields: map[string]string{"Id": "10002"}}}}
	params, _ := model.MarshalParams(p)
	wo, err := svc.CreateAndExecute(model.TypeNewServer, "创建洛阳2服", p.Summary(), params, "admin")
	if err != nil {
		t.Fatalf("CreateAndExecute: %v", err)
	}
	// 免审批,建单即异步执行;newserver 执行完直接完成(opened)
	waitStatus(t, svc, wo.ID, workflow.StatusOpened, 3*time.Second)
}

func TestCreateAndExecuteDeleteServerCompletes(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	svc.execDelay = 0

	p := model.DeleteServerParams{ID: 10002, Kind: "game", ServerName: "x",
		RemoveALB: true, RegenServerlist: true}
	params, _ := model.MarshalParams(p)
	wo, err := svc.CreateAndExecute(model.TypeDeleteServer, "删除游戏服:x(Id 10002)", p.Summary(), params, "admin")
	if err != nil {
		t.Fatalf("CreateAndExecute: %v", err)
	}
	waitStatus(t, svc, wo.ID, workflow.StatusOpened, 3*time.Second)
}

func TestConfigPushCompletesLikeHotupdate(t *testing.T) {
	gdb := newTestDB(t)
	svc := New(gdb, mockStore(t), nil)
	svc.execDelay = 0

	wo, err := svc.CreateWithArtifact(model.TypeConfigPush, "全服更新", "全服",
		"k", map[string][]byte{"k": []byte("x")}, "", "admin")
	if err != nil {
		t.Fatal(err)
	}
	// 直接置为执行中,再同步跑执行器(与 TestHotupdateExecSuccessGoesOpened 相同模式)
	gdb.Model(wo).Update("status", workflow.StatusExecuting)
	svc.runExecutor(wo.ID)
	got, _ := svc.Get(wo.ID)
	if got.Status != workflow.StatusOpened {
		t.Fatalf("status = %s, want opened(执行完即完成)", got.Status)
	}
}

// 把工单直接造成 TypeNewServer + ExecFailed,便于测换包/回滚。
func newFailedNewServerWO(t *testing.T, svc *Service, pkg string) *model.WorkOrder {
	t.Helper()
	p := model.NewServerCreateParams{RegionName: "洛阳", VersionPackage: pkg,
		Rows: []model.NewServerRow{{Kind: "game", ID: 10002, Fields: map[string]string{"Id": "10002"}}}}
	params, _ := model.MarshalParams(p)
	wo, err := svc.Create(model.TypeNewServer, "创建游戏服", "洛阳·10002", params, "alice")
	if err != nil {
		t.Fatal(err)
	}
	wo.Status = workflow.StatusExecFailed
	svc.db.Save(wo)
	return wo
}

func TestUpdateNewServerPackage(t *testing.T) {
	svc := New(newTestDB(t), mockStore(t), nil)
	wo := newFailedNewServerWO(t, svc, "old.zip")

	if err := svc.UpdateNewServerPackage(wo.ID, "new.zip", "carol"); err != nil {
		t.Fatalf("换包: %v", err)
	}
	got, _ := svc.Get(wo.ID)
	p, _ := model.UnmarshalNewServerCreateParams(got.Params)
	if p.VersionPackage != "new.zip" {
		t.Errorf("版本包 = %q, want new.zip", p.VersionPackage)
	}
	if got.Status != workflow.StatusExecFailed {
		t.Errorf("换包后状态应仍为执行失败, got %q", got.Status)
	}

	// 空包被拒
	if err := svc.UpdateNewServerPackage(wo.ID, "  ", "carol"); err == nil {
		t.Error("空包应被拒")
	}
	// 非执行失败态被拒
	got.Status = workflow.StatusOpened
	svc.db.Save(got)
	if err := svc.UpdateNewServerPackage(wo.ID, "x.zip", "carol"); err == nil {
		t.Error("非执行失败态应被拒")
	}
}

func TestTeardownCancelsOrder(t *testing.T) {
	svc := New(newTestDB(t), mockStore(t), nil)
	wo := newFailedNewServerWO(t, svc, "old.zip")

	if err := svc.Act(wo.ID, workflow.ActionTeardown, "carol", ""); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	waitStatus(t, svc, wo.ID, workflow.StatusCancelled, 2*time.Second)
}

func TestTeardownOnlyFromExecFailed(t *testing.T) {
	svc := New(newTestDB(t), mockStore(t), nil)
	wo := newFailedNewServerWO(t, svc, "old.zip")
	wo.Status = workflow.StatusOpened
	svc.db.Save(wo)
	if err := svc.Act(wo.ID, workflow.ActionTeardown, "carol", ""); err == nil {
		t.Error("非执行失败态 teardown 应被拒")
	}
}
