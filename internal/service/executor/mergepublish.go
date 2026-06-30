package executor

import (
	"fmt"

	"gongdan/internal/model"
)

// MergePublishExecutor 合服预告发布:上传冻结快照(MergeServerFunction.txt)→全服 gd_download→GM热更。
// 与 ConfigPushExecutor 同尾(pushSnapshotAndReload),仅快照来源不同。
type MergePublishExecutor struct {
	cos     COSClient
	store   ArtifactStore
	pushAll func(log LogFunc) ([]int, error)
	gm      func(log LogFunc)
}

func NewMergePublishExecutor(cfg *RealConfig) *MergePublishExecutor {
	rel := NewReleaseExecutor(cfg)
	return &MergePublishExecutor{
		cos:     cfg.COS,
		store:   cfg.Artifacts,
		pushAll: func(log LogFunc) ([]int, error) { return rel.PushConfigAll(log) },
		gm:      func(log LogFunc) { rel.hotRunner().gmHotUpdate(log) },
	}
}

func (e *MergePublishExecutor) Execute(wo *model.WorkOrder, log LogFunc) error {
	art, err := e.store.GetArtifact(wo.ID)
	if err != nil {
		return fmt.Errorf("读取工单工件: %w", err)
	}
	return pushSnapshotAndReload(wo.ID, art, e.cos, e.store, e.pushAll, e.gm, log)
}
