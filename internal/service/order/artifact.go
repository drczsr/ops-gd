package order

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"gongdan/internal/model"
	"gongdan/internal/service/executor"
	"gongdan/internal/service/workflow"

	"gorm.io/gorm"
)

// CreateWithArtifact 在一个事务里创建工单 + 配置快照工件(供 configpush/mergepublish 系统建单)。
func (s *Service) CreateWithArtifact(woType, title, targetSummary, primaryKey string,
	files map[string][]byte, baselineVersionID, createBy string) (*model.WorkOrder, error) {
	enc := make(map[string]string, len(files))
	for k, v := range files {
		enc[k] = base64.StdEncoding.EncodeToString(v)
	}
	blob, err := json.Marshal(enc)
	if err != nil {
		return nil, fmt.Errorf("序列化快照: %w", err)
	}
	now := time.Now()
	wo := &model.WorkOrder{
		OrderNo: s.nextOrderNo(now), Type: woType, Title: title,
		Status: workflow.StatusPendingApprove, TargetSummary: targetSummary,
		CreateBy: createBy, CreateTime: now, UpdateTime: now,
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Create(wo).Error; e != nil {
			return e
		}
		return tx.Create(&model.WorkOrderArtifact{
			OrderID: wo.ID, PrimaryKey: primaryKey,
			SnapshotFiles: string(blob), BaselineVersionID: baselineVersionID,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	s.writeLog(wo.ID, model.LogAction, workflow.StageSubmit, createBy, "提交工单(系统生成)")
	return wo, nil
}

// GetArtifact 读取并解码工件(实现 executor.ArtifactStore)。
func (s *Service) GetArtifact(orderID uint) (executor.ConfigArtifact, error) {
	var row model.WorkOrderArtifact
	if err := s.db.Where("order_id = ?", orderID).First(&row).Error; err != nil {
		return executor.ConfigArtifact{}, err
	}
	var enc map[string]string
	if err := json.Unmarshal([]byte(row.SnapshotFiles), &enc); err != nil {
		return executor.ConfigArtifact{}, fmt.Errorf("反序列化快照: %w", err)
	}
	files := make(map[string][]byte, len(enc))
	for k, v := range enc {
		b, derr := base64.StdEncoding.DecodeString(v)
		if derr != nil {
			return executor.ConfigArtifact{}, fmt.Errorf("解码快照 %s: %w", k, derr)
		}
		files[k] = b
	}
	return executor.ConfigArtifact{
		PrimaryKey: row.PrimaryKey, SnapshotFiles: files,
		BaselineVersionID: row.BaselineVersionID,
	}, nil
}

// SetDeployedVersion 回填部署版本 id(实现 executor.ArtifactStore)。
func (s *Service) SetDeployedVersion(orderID uint, versionID string) error {
	return s.db.Model(&model.WorkOrderArtifact{}).Where("order_id = ?", orderID).
		Update("deployed_version_id", versionID).Error
}
