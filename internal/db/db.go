package db

import (
	"gongdan/internal/model"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Init 打开 SQLite 连接,自动建表,并确保存在 admin 种子账号。
func Init(dsn string) (*gorm.DB, error) {
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)

	if err := gdb.AutoMigrate(
		&model.WorkOrder{},
		&model.WorkOrderLog{},
		&model.WorkOrderArtifact{},
		&model.WorkOrderMember{},
		&model.User{},
	); err != nil {
		return nil, err
	}

	if err := seedAdmin(gdb); err != nil {
		return nil, err
	}
	return gdb, nil
}

// seedAdmin 若无 admin 账号则创建,默认密码 admin123,且赋予全部角色标签。
func seedAdmin(gdb *gorm.DB) error {
	var count int64
	gdb.Model(&model.User{}).Where("username = ?", "admin").Count(&count)
	if count > 0 {
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := gdb.Create(&model.User{
		Username:    "admin",
		Password:    string(hash),
		DisplayName: "管理员",
	}).Error; err != nil {
		return err
	}
	return gdb.Create(&model.WorkOrderMember{
		UserID:      "admin",
		DisplayName: "管理员",
		CanSubmit:   true,
		CanApprove:  true,
		CanExecute:  true,
		CanTest:     true,
		CanAdmin:    true,
	}).Error
}
