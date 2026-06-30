package executor

import (
	"fmt"

	"gongdan/internal/model"
)

// ConfigPushExecutor 全服更新:上传冻结快照(server+client)→全服 gd_download→GM热更。
// 内嵌 release 能力通过函数注入(pushAll/gm),便于测试。
type ConfigPushExecutor struct {
	cos     COSClient
	store   ArtifactStore
	pushAll func(log LogFunc) ([]int, error)
	gm      func(log LogFunc)
}

func NewConfigPushExecutor(cfg *RealConfig) *ConfigPushExecutor {
	rel := NewReleaseExecutor(cfg)
	return &ConfigPushExecutor{
		cos:     cfg.COS,
		store:   cfg.Artifacts,
		pushAll: func(log LogFunc) ([]int, error) { return rel.PushConfigAll(log) },
		gm:      func(log LogFunc) { rel.hotRunner().gmHotUpdate(log) },
	}
}

func (e *ConfigPushExecutor) Execute(wo *model.WorkOrder, log LogFunc) error {
	art, err := e.store.GetArtifact(wo.ID)
	if err != nil {
		return fmt.Errorf("读取工单工件: %w", err)
	}
	return pushSnapshotAndReload(wo.ID, art, e.cos, e.store, e.pushAll, e.gm, log)
}
