package db

import (
	"testing"

	"gongdan/internal/model"
)

func TestInitCreatesTablesAndSeedsAdmin(t *testing.T) {
	gdb, err := Init("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("Init error: %v", err)
	}

	// 表已建立:能插入一条工单
	wo := model.WorkOrder{OrderNo: "WO1", Type: model.TypeMerge, Status: "pending_approve"}
	if err := gdb.Create(&wo).Error; err != nil {
		t.Fatalf("create work order: %v", err)
	}

	// 种子管理员存在
	var count int64
	gdb.Model(&model.User{}).Where("username = ?", "admin").Count(&count)
	if count != 1 {
		t.Errorf("admin user count = %d, want 1", count)
	}
}
