package auth

import (
	"errors"

	"gongdan/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var ErrInvalid = errors.New("用户名或密码错误")

// UpsertAccount 创建或更新登录账号(以 username 为唯一键)。
// password 非空时设置/重置为该密码(bcrypt 哈希);password 为空时仅更新显示名、保留原密码。
// 新账号要求 password 必填。
func UpsertAccount(gdb *gorm.DB, username, password, displayName string) error {
	if username == "" {
		return errors.New("用户名不能为空")
	}

	var u model.User
	err := gdb.Where("username = ?", username).First(&u).Error
	notFound := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !notFound {
		return err
	}

	if notFound && password == "" {
		return errors.New("新账号必须设置密码")
	}

	if password != "" {
		hash, herr := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if herr != nil {
			return herr
		}
		u.Password = string(hash)
	}
	u.Username = username
	u.DisplayName = displayName

	if notFound {
		return gdb.Create(&u).Error
	}
	return gdb.Save(&u).Error
}

// Verify 校验账号密码,成功返回用户。
func Verify(gdb *gorm.DB, username, password string) (*model.User, error) {
	var u model.User
	if err := gdb.Where("username = ?", username).First(&u).Error; err != nil {
		return nil, ErrInvalid
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)) != nil {
		return nil, ErrInvalid
	}
	return &u, nil
}
