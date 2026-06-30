package handler

import (
	"strings"
	"testing"

	"gongdan/internal/model"
)

func TestDetailTargetSummaryReleaseShowsCountOnly(t *testing.T) {
	ids := make([]int, 0, 13)
	for i := 0; i < 13; i++ {
		ids = append(ids, 1000+i)
	}
	got := detailTargetSummary(&model.WorkOrder{
		Type: model.TypeRelease,
		Params: mustParams(t, model.ReleaseParams{
			ServerIDs:      ids,
			VersionPackage: "v.zip",
		}),
	})
	if got != "13 个目标服" {
		t.Fatalf("target summary should stay concise, got %q", got)
	}
}

func TestParamRowsMergeShowsPairIDs(t *testing.T) {
	params, err := model.MarshalParams(model.MergeParams{
		IncludeMerge:    true,
		IncludePreMerge: true,
		Pairs:           []model.MergePair{{Target: 10001, Source: 10002}, {Target: 10003, Source: 10004}},
		ToolPackage:     "tool.zip",
		PreMergeIDs:     []int{20001, 20002},
		PreMergeDate:    "20260617",
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	rows := paramRows(&model.WorkOrder{Type: model.TypeMerge, Params: params})
	if r := findRow(rows, "合服配对ID"); r == nil || !strings.Contains(r.Value, "10001 <- 10002") || !strings.Contains(r.Value, "10003 <- 10004") {
		t.Fatalf("merge detail should show pair ids directly: %+v", r)
	}
	got := detailTargetSummary(&model.WorkOrder{Type: model.TypeMerge, Params: params})
	if got != "合服 2 组；预合服 2 个" {
		t.Fatalf("target summary should show counts only, got %q", got)
	}
}

func TestDetailTargetSummaryFallsBackToCompactRowSummary(t *testing.T) {
	got := detailTargetSummary(&model.WorkOrder{
		Type:   model.TypeNewServer,
		Params: `{"region_name":"华北","rows":[{"kind":"game","id":11022},{"kind":"battle","id":12801}]}`,
	})
	if got != "华北 · 新游戏服 1 个" {
		t.Fatalf("target summary should stay compact, got %q", got)
	}
}
