package order

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gongdan/internal/db"
	"gongdan/internal/model"
	"gongdan/internal/service/workflow"

	"gorm.io/gorm"
)

// notifyTestDB 按测试名隔离的内存库,避免 cache=shared 跨测试累积成员/用户行。
func notifyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:notify_%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"))
	gdb, err := db.Init(dsn)
	if err != nil {
		t.Fatalf("db init: %v", err)
	}
	return gdb
}

// fakeNotifier 记录每次 Send 的内容与 @列表。
type fakeNotifier struct {
	mu    sync.Mutex
	texts []string
	ments [][]string
}

func (f *fakeNotifier) Send(text string, mentions []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	f.ments = append(f.ments, mentions)
	return nil
}

func (f *fakeNotifier) calls() ([]string, [][]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...), append([][]string(nil), f.ments...)
}

func seedMemberUser(t *testing.T, s *Service, userID, wework string, approve, execute, test bool) {
	t.Helper()
	if err := s.db.Create(&model.WorkOrderMember{
		UserID: userID, CanApprove: approve, CanExecute: execute, CanTest: test,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Create(&model.User{Username: userID, WeworkUserID: wework}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestResolveByRole_PicksFlagAndNonEmptyWework(t *testing.T) {
	s := New(notifyTestDB(t), mockStore(t), nil)
	seedMemberUser(t, s, "appr", "WW_appr", true, false, false)
	seedMemberUser(t, s, "exec", "WW_exec", false, true, false)
	seedMemberUser(t, s, "noww", "", true, false, false) // 审批人但无 wework → 应被剔除

	got := s.resolveByRole("can_approve")
	if len(got) != 1 || got[0] != "WW_appr" {
		t.Fatalf("resolveByRole(can_approve) = %v, want [WW_appr]", got)
	}
}

func TestResolveByUsername(t *testing.T) {
	s := New(notifyTestDB(t), mockStore(t), nil)
	seedMemberUser(t, s, "sub", "WW_sub", false, false, false)
	got := s.resolveByUsername("sub")
	if len(got) != 1 || got[0] != "WW_sub" {
		t.Fatalf("resolveByUsername(sub) = %v, want [WW_sub]", got)
	}
	if g := s.resolveByUsername("missing"); len(g) != 0 {
		t.Fatalf("resolveByUsername(missing) = %v, want empty", g)
	}
}

func TestNotifyEvent_DisabledIsNoop(t *testing.T) {
	s := New(notifyTestDB(t), mockStore(t), nil) // 未注入 notifier
	s.notifyEvent(&model.WorkOrder{OrderNo: "WO1"}, evSubmit, "")
	// 不 panic 即通过(notifier 为 nil)
}

func TestActApprove_NotifiesExecutor(t *testing.T) {
	s := New(notifyTestDB(t), mockStore(t), nil)
	fn := &fakeNotifier{}
	s.WithNotify(fn, "")
	seedMemberUser(t, s, "exec", "WW_exec", false, true, false)

	wo, err := s.Create(model.TypeMerge, "标题", "目标", "{}", "submitter")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Act(wo.ID, workflow.ActionApprove, "approver", "ok"); err != nil {
		t.Fatal(err)
	}

	// 异步发送:轮询等待最多 ~1s,直到出现 @WW_exec(提交事件@审批人为空,需等审批事件)
	var ments [][]string
	found := false
	for i := 0; i < 50 && !found; i++ {
		_, ments = fn.calls()
		for _, m := range ments {
			for _, u := range m {
				if u == "WW_exec" {
					found = true
				}
			}
		}
		if !found {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !found {
		t.Fatalf("approve 未 @ 到执行人 WW_exec;ments=%v", ments)
	}
}
