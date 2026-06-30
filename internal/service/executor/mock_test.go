package executor

import (
	"strings"
	"testing"

	"gongdan/internal/model"
)

func TestMockExecutorSuccess(t *testing.T) {
	exec := NewMock()
	exec.StepDelay = 0
	wo := &model.WorkOrder{ID: 1, Type: model.TypeMerge, Title: "测试合服"}

	var lines []string
	err := exec.Execute(wo, func(line string) { lines = append(lines, line) })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("应产生日志行")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "完成") {
		t.Errorf("日志应包含完成标记, got:\n%s", joined)
	}
}

func TestDispatchByModeMock(t *testing.T) {
	exec := Dispatch("mock", model.TypeRelease, nil)
	if _, ok := exec.(*MockExecutor); !ok {
		t.Errorf("mock 模式应返回 MockExecutor, got %T", exec)
	}
}

func TestMockStepsMergeReflectsNewFlow(t *testing.T) {
	exec := NewMock()
	exec.StepDelay = 0
	var lines []string
	exec.Execute(&model.WorkOrder{ID: 1, Type: model.TypeMerge, Title: "t"}, func(l string) { lines = append(lines, l) })
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "配置") {
		t.Errorf("合服 mock 步骤应反映新流程(含配置相关):\n%s", joined)
	}
}
