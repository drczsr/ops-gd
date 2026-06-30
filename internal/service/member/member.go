package member

import (
	"errors"

	"gongdan/internal/model"
	"gongdan/internal/service/workflow"

	"gorm.io/gorm"
)

type Service struct{ db *gorm.DB }

func New(gdb *gorm.DB) *Service { return &Service{db: gdb} }

// Roles 返回某用户的角色标签;未知用户返回全 false。
func (s *Service) Roles(userID string) model.WorkOrderMember {
	var m model.WorkOrderMember
	if err := s.db.Where("user_id = ?", userID).First(&m).Error; err != nil {
		return model.WorkOrderMember{UserID: userID}
	}
	return m
}

// HasRole 判断用户是否具备某角色标签。
func (s *Service) HasRole(userID, role string) bool {
	if s.IsAdmin(userID) {
		switch role {
		case workflow.RoleSubmit, workflow.RoleApprove, workflow.RoleExecute, workflow.RoleTest:
			return true
		}
	}
	r := s.Roles(userID)
	switch role {
	case workflow.RoleSubmit:
		return r.CanSubmit
	case workflow.RoleApprove:
		return r.CanApprove
	case workflow.RoleExecute:
		return r.CanExecute
	case workflow.RoleTest:
		return r.CanTest
	default:
		return false
	}
}

// IsAdmin 判断用户是否为管理员:内置 admin 账号永远是管理员(防锁死),
// 或被显式授予管理员角色标签的成员。
func (s *Service) IsAdmin(userID string) bool {
	if userID == model.AdminUsername {
		return true
	}
	return s.Roles(userID).CanAdmin
}

// 菜单访问标识(供 CanMenu 用)。
const (
	MenuServers      = "servers"
	MenuGSConfig     = "gsconfig"
	MenuMergePreview = "mergepreview"
)

// CanMenu 是否可访问某菜单:管理员永远可;否则看对应菜单标志。未知菜单返回 false。
func (s *Service) CanMenu(userID, menu string) bool {
	if s.IsAdmin(userID) {
		return true
	}
	r := s.Roles(userID)
	switch menu {
	case MenuServers:
		return r.CanServers
	case MenuGSConfig:
		return r.CanGSConfig
	case MenuMergePreview:
		return r.CanMergePreview
	default:
		return false
	}
}

// All 列出全部成员。
func (s *Service) All() ([]model.WorkOrderMember, error) {
	var rows []model.WorkOrderMember
	err := s.db.Order("id asc").Find(&rows).Error
	return rows, err
}

// Save 新增或更新一个成员的角色标签(按 user_id upsert)。
func (s *Service) Save(m *model.WorkOrderMember) error {
	var existing model.WorkOrderMember
	err := s.db.Where("user_id = ?", m.UserID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.db.Create(m).Error
	}
	if err != nil {
		return err
	}
	existing.DisplayName = m.DisplayName
	existing.CanSubmit = m.CanSubmit
	existing.CanApprove = m.CanApprove
	existing.CanExecute = m.CanExecute
	existing.CanTest = m.CanTest
	existing.CanAdmin = m.CanAdmin
	existing.CanServers = m.CanServers
	existing.CanGSConfig = m.CanGSConfig
	existing.CanMergePreview = m.CanMergePreview
	return s.db.Save(&existing).Error
}
