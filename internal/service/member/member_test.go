package member

import (
	"testing"

	"gongdan/internal/db"
	"gongdan/internal/model"
	"gongdan/internal/service/workflow"
)

func TestCanMenu(t *testing.T) {
	gdb, _ := db.Init("file::memory:?cache=shared")
	svc := New(gdb)

	// admin override:所有菜单都可
	for _, m := range []string{MenuServers, MenuGSConfig, MenuMergePreview} {
		if !svc.CanMenu("admin", m) {
			t.Errorf("admin 应可访问菜单 %q", m)
		}
	}
	// 策划:只勾了合服预告
	if err := svc.Save(&model.WorkOrderMember{UserID: "planner", CanMergePreview: true}); err != nil {
		t.Fatal(err)
	}
	if !svc.CanMenu("planner", MenuMergePreview) {
		t.Error("planner 勾了合服预告应可访问")
	}
	if svc.CanMenu("planner", MenuServers) || svc.CanMenu("planner", MenuGSConfig) {
		t.Error("planner 未勾的菜单不应可访问")
	}
	// 未知用户:全不可
	if svc.CanMenu("nobody", MenuServers) {
		t.Error("未知用户不应可访问任何菜单")
	}
	// 未知菜单名:false
	if svc.CanMenu("planner", "whatever") {
		t.Error("未知菜单名应返回 false")
	}
}

func TestRolesForAdmin(t *testing.T) {
	gdb, err := db.Init("file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(gdb)

	roles := svc.Roles("admin")
	if !roles.CanSubmit || !roles.CanApprove || !roles.CanExecute || !roles.CanTest {
		t.Errorf("admin 应拥有全部角色, got %+v", roles)
	}

	// 未知用户:无任何角色
	none := svc.Roles("nobody")
	if none.CanSubmit || none.CanApprove || none.CanExecute || none.CanTest {
		t.Errorf("未知用户不应有角色, got %+v", none)
	}
}

func TestHasRole(t *testing.T) {
	gdb, _ := db.Init("file::memory:?cache=shared")
	svc := New(gdb)
	if !svc.HasRole("admin", "approve") {
		t.Error("admin 应有 approve 角色")
	}
	if svc.HasRole("admin", "system") {
		t.Error("system 不是人工角色")
	}
}

func TestHasRole_AdminOverrideWhenMemberMissing(t *testing.T) {
	gdb, _ := db.Init("file::memory:?cache=shared")
	if err := gdb.Where("user_id = ?", "admin").Delete(&model.WorkOrderMember{}).Error; err != nil {
		t.Fatal(err)
	}
	svc := New(gdb)
	if !svc.IsAdmin("admin") {
		t.Fatal("admin should still be admin by built-in override")
	}
	for _, role := range []string{
		workflow.RoleSubmit,
		workflow.RoleApprove,
		workflow.RoleExecute,
		workflow.RoleTest,
	} {
		if !svc.HasRole("admin", role) {
			t.Fatalf("admin should keep role %q even without member row", role)
		}
	}
}
