package executor

import (
	"fmt"
	"time"

	"gongdan/internal/model"
)

// MockExecutor 模拟执行:按工单类型打印若干步骤日志,不连真实服务器。
type MockExecutor struct {
	// StepDelay 每步之间的等待,便于在页面上观察日志逐步刷新。
	StepDelay time.Duration
}

func NewMock() *MockExecutor {
	return &MockExecutor{StepDelay: 800 * time.Millisecond}
}

func (m *MockExecutor) Execute(wo *model.WorkOrder, log LogFunc) error {
	steps := mockSteps(wo.Type)
	log(fmt.Sprintf("[模拟执行] 工单 #%d %s 开始", wo.ID, wo.Title))
	for i, s := range steps {
		time.Sleep(m.StepDelay)
		log(fmt.Sprintf("第 %d/%d 步: %s ... OK", i+1, len(steps), s))
	}
	log("[模拟执行] 全部步骤完成")
	return nil
}

func mockSteps(woType string) []string {
	switch woType {
	case model.TypeMerge:
		return []string{"改库(预合服/合服字段)", "生成配置并提交COS", "执行合服", "全服配置更新", "GM热更"}
	case model.TypeRelease:
		return []string{"停服", "更新完整版本包", "重启服务器"}
	case model.TypeHotupdate:
		return []string{"下发热更文件", "重载配置"}
	case model.TypeNewServer:
		return []string{"写配置库", "生成serverlist并发布COS", "铺包", "初始化数据库", "拉配置", "开服并校验状态"}
	default:
		return []string{"执行"}
	}
}
