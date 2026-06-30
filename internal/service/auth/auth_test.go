package auth

import (
	"testing"

	"gongdan/internal/db"
)

func TestUpsertAccountCreateThenLogin(t *testing.T) {
	gdb, err := db.Init("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("db.Init: %v", err)
	}

	// 新账号必须带密码
	if err := UpsertAccount(gdb, "bob", "", "Bob"); err == nil {
		t.Fatalf("新账号无密码应报错")
	}

	// 创建账号
	if err := UpsertAccount(gdb, "bob", "bob123", "Bob"); err != nil {
		t.Fatalf("UpsertAccount create: %v", err)
	}

	// 能用新密码登录
	u, err := Verify(gdb, "bob", "bob123")
	if err != nil {
		t.Fatalf("Verify after create: %v", err)
	}
	if u.DisplayName != "Bob" {
		t.Errorf("DisplayName = %q, want Bob", u.DisplayName)
	}

	// 留空密码更新:保留原密码,仅改显示名
	if err := UpsertAccount(gdb, "bob", "", "Bobby"); err != nil {
		t.Fatalf("UpsertAccount update no-pass: %v", err)
	}
	u, err = Verify(gdb, "bob", "bob123")
	if err != nil {
		t.Fatalf("旧密码应仍有效: %v", err)
	}
	if u.DisplayName != "Bobby" {
		t.Errorf("DisplayName = %q, want Bobby", u.DisplayName)
	}

	// 重置密码
	if err := UpsertAccount(gdb, "bob", "newpass", "Bobby"); err != nil {
		t.Fatalf("UpsertAccount reset: %v", err)
	}
	if _, err := Verify(gdb, "bob", "newpass"); err != nil {
		t.Fatalf("新密码应有效: %v", err)
	}
	if _, err := Verify(gdb, "bob", "bob123"); err == nil {
		t.Errorf("旧密码应失效")
	}
}
