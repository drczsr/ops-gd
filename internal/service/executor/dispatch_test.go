package executor

import (
	"testing"

	"gongdan/internal/model"
)

func TestDispatchReal(t *testing.T) {
	cfg := &RealConfig{}
	if _, ok := Dispatch("real", model.TypeMerge, cfg).(*MergeExecutor); !ok {
		t.Error("real+merge 应返回 *MergeExecutor")
	}
	if _, ok := Dispatch("real", model.TypeRelease, cfg).(*ReleaseExecutor); !ok {
		t.Error("real+release 应返回 *ReleaseExecutor")
	}
	if _, ok := Dispatch("real", model.TypeHotupdate, cfg).(*HotUpdateExecutor); !ok {
		t.Error("real+hotupdate 应返回 *HotUpdateExecutor")
	}
}

func TestDispatchMockUnchanged(t *testing.T) {
	if _, ok := Dispatch("mock", model.TypeRelease, nil).(*MockExecutor); !ok {
		t.Error("mock 模式应返回 *MockExecutor")
	}
}
