package model

import (
	"strings"
	"testing"
)

func TestTypeLabel(t *testing.T) {
	cases := map[string]string{
		TypeMerge:     "合服",
		TypeRelease:   "发版",
		TypeHotupdate: "热更",
		"other":       "other", // 未知类型原样返回
	}
	for in, want := range cases {
		if got := TypeLabel(in); got != want {
			t.Errorf("TypeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTargetBrief(t *testing.T) {
	// 短列表(<=6 个)不折叠,原样显示
	v := TargetBrief("10001,10012,10054,10057")
	if v.Fold || v.Full != "10001,10012,10054,10057" {
		t.Errorf("短列表不应折叠: %+v", v)
	}
	// 非列表摘要(合服)不折叠
	v = TargetBrief("合服 2 组")
	if v.Fold {
		t.Errorf("合服摘要不应折叠: %+v", v)
	}
	// 长列表折叠:预览前 3 个 + 总数
	v = TargetBrief("10001,10012,10054,10057,10142,10198,10302")
	if !v.Fold || v.Count != 7 || v.Preview != "10001,10012,10054" || v.Full == "" {
		t.Errorf("长列表应折叠并预览前3个: %+v", v)
	}
}

func TestTypeBadge(t *testing.T) {
	// 类型用淡色(soft)徽章,避免和状态列一起太花
	b := TypeBadge(TypeRelease)
	if b.Label != "发版" || b.Class != "t-release" || b.Icon != "ic-t-release" {
		t.Errorf("发版徽章错误: %+v", b)
	}
	b = TypeBadge(TypeHotupdate)
	if b.Label != "热更" || b.Class != "t-hotupdate" {
		t.Errorf("热更徽章错误: %+v", b)
	}
	// 未知类型:中性灰、无图标、原样标签
	b = TypeBadge("weird")
	if b.Label != "weird" || b.Class != "t-default" || b.Icon != "" {
		t.Errorf("未知类型徽章应回退: %+v", b)
	}
}

func TestParseMergePairs(t *testing.T) {
	pairs, err := ParseMergePairs("10371,10373\n10381,10382\n")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("len = %d, want 2", len(pairs))
	}
	if pairs[0].Target != 10371 || pairs[0].Source != 10373 {
		t.Errorf("pair0 = %+v", pairs[0])
	}
}

func TestParseMergePairsBad(t *testing.T) {
	if _, err := ParseMergePairs("abc,10373"); err == nil {
		t.Error("非数字应报错")
	}
	if _, err := ParseMergePairs("10371"); err == nil {
		t.Error("缺少源服应报错")
	}
	if _, err := ParseMergePairs("10371,10371"); err == nil {
		t.Error("目标=源应报错")
	}
}

func TestMergeParamsSummary(t *testing.T) {
	p := MergeParams{IncludeMerge: true, Pairs: []MergePair{{Target: 10371, Source: 10373}}}
	if p.Summary() != "合服1组" {
		t.Errorf("summary = %q", p.Summary())
	}
	p2 := MergeParams{IncludePreMerge: true, PreMergeIDs: []int{1, 2, 3}}
	if p2.Summary() != "预合服3个" {
		t.Errorf("summary = %q", p2.Summary())
	}
	p3 := MergeParams{IncludeMerge: true, Pairs: []MergePair{{Target: 1, Source: 2}},
		IncludePreMerge: true, PreMergeIDs: []int{9}}
	if p3.Summary() != "合服1组 + 预合服1个" {
		t.Errorf("summary = %q", p3.Summary())
	}
}

func TestMergeParamsRoundTrip(t *testing.T) {
	in := MergeParams{
		IncludeMerge: true, Pairs: []MergePair{{Target: 10001, Source: 10004}}, ToolPackage: "t.zip",
		IncludePreMerge: true, PreMergeIDs: []int{20001, 20002}, PreMergeDate: "20260605",
	}
	s, err := MarshalParams(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalMergeParams(s)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IncludeMerge || !got.IncludePreMerge || got.ToolPackage != "t.zip" ||
		got.PreMergeDate != "20260605" || len(got.Pairs) != 1 || len(got.PreMergeIDs) != 2 {
		t.Errorf("got %+v", got)
	}
}

func TestParseServerIDsText(t *testing.T) {
	ids, err := ParseServerIDsText("10001, 10002\n10003  10004，10005")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 5 || ids[0] != 10001 || ids[4] != 10005 {
		t.Errorf("ids=%v", ids)
	}
	if _, err := ParseServerIDsText("10001,abc"); err == nil {
		t.Error("非数字应报错")
	}
	if _, err := ParseServerIDsText("  \n "); err == nil {
		t.Error("空应报错")
	}
}

func TestMergeDatePlusOneDay(t *testing.T) {
	cases := map[string]string{"20260605": "20260606", "20260630": "20260701", "20261231": "20270101"}
	for in, want := range cases {
		got, err := MergeDatePlusOneDay(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != want {
			t.Errorf("MergeDatePlusOneDay(%s)=%s want %s", in, got, want)
		}
	}
	if _, err := MergeDatePlusOneDay("2026-06-05"); err == nil {
		t.Error("非 YYYYMMDD 应报错")
	}
}

func TestReleaseParamsConfigFailedRoundTrip(t *testing.T) {
	p := ReleaseParams{ServerIDs: []int{10001, 10002}, VersionPackage: "v.zip", ConfigFailedIDs: []int{10002}}
	s, _ := MarshalParams(p)
	got, _ := UnmarshalReleaseParams(s)
	if len(got.ConfigFailedIDs) != 1 || got.ConfigFailedIDs[0] != 10002 {
		t.Errorf("ConfigFailedIDs=%v", got.ConfigFailedIDs)
	}
	empty, _ := MarshalParams(ReleaseParams{ServerIDs: []int{1}, VersionPackage: "v"})
	if strings.Contains(empty, "config_failed_ids") {
		t.Errorf("空不应写出: %s", empty)
	}
}

func TestReleaseParamsSummary(t *testing.T) {
	p := ReleaseParams{ServerIDs: []int{210, 211, 212}, VersionPackage: "v.zip"}
	if p.Summary() != "210,211,212" {
		t.Errorf("summary = %q", p.Summary())
	}
}

func TestMarshalUnmarshal(t *testing.T) {
	p := ReleaseParams{ServerIDs: []int{1, 2}, VersionPackage: "v.zip"}
	s, err := MarshalParams(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalReleaseParams(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ServerIDs) != 2 || got.VersionPackage != "v.zip" {
		t.Errorf("got %+v", got)
	}
}

func TestReleaseParamsLastFailedRoundTrip(t *testing.T) {
	p := ReleaseParams{
		ServerIDs:      []int{10001, 10002, 10003},
		VersionPackage: "v.zip",
		IncludeBattle:  true,
		LastFailedIDs:  []int{10002},
	}
	s, err := MarshalParams(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalReleaseParams(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.LastFailedIDs) != 1 || got.LastFailedIDs[0] != 10002 {
		t.Errorf("LastFailedIDs = %v, want [10002]", got.LastFailedIDs)
	}
}

// 老工单 JSON(无 last_failed_ids 字段)应反序列化为 nil,且 omitempty 不写出该键。
func TestReleaseParamsLastFailedOmitempty(t *testing.T) {
	old := `{"server_ids":[10001],"version_package":"v.zip"}`
	got, err := UnmarshalReleaseParams(old)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastFailedIDs != nil {
		t.Errorf("老工单 LastFailedIDs 应为 nil, got %v", got.LastFailedIDs)
	}
	s, err := MarshalParams(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, "last_failed_ids") {
		t.Errorf("空 LastFailedIDs 不应写出该键: %s", s)
	}
}

func TestReleaseParamsValidate(t *testing.T) {
	cases := []struct {
		name string
		p    ReleaseParams
		ok   bool
	}{
		{"基本-无热更", ReleaseParams{ServerIDs: []int{10001}, VersionPackage: "v.zip"}, true},
		{"无目标服", ReleaseParams{VersionPackage: "v.zip"}, false},
		{"无版本包", ReleaseParams{ServerIDs: []int{10001}}, false},
		{"勾热更未选范围", ReleaseParams{ServerIDs: []int{10001}, VersionPackage: "v.zip",
			IncludeHotUpdate: true, HotPackage: "c.zip", HotFiles: "a.txt"}, false},
		{"勾热更范围非法", ReleaseParams{ServerIDs: []int{10001}, VersionPackage: "v.zip",
			IncludeHotUpdate: true, HotScope: "xxx", HotPackage: "c.zip", HotFiles: "a.txt"}, false},
		{"勾热更缺包", ReleaseParams{ServerIDs: []int{10001}, VersionPackage: "v.zip",
			IncludeHotUpdate: true, HotScope: "all", HotFiles: "a.txt"}, false},
		{"勾热更缺文件", ReleaseParams{ServerIDs: []int{10001}, VersionPackage: "v.zip",
			IncludeHotUpdate: true, HotScope: "all", HotPackage: "c.zip"}, false},
		{"勾热更齐全", ReleaseParams{ServerIDs: []int{10001}, VersionPackage: "v.zip",
			IncludeHotUpdate: true, HotScope: "selected", HotPackage: "c.zip", HotFiles: "a.txt"}, true},
	}
	for _, c := range cases {
		err := c.p.Validate()
		if c.ok && err != nil {
			t.Errorf("%s: 期望通过,却报错: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: 期望报错,却通过", c.name)
		}
	}
}

func TestHotupdateParamsValidate(t *testing.T) {
	cases := []struct {
		name string
		p    HotupdateParams
		ok   bool
	}{
		{"齐全", HotupdateParams{ServerIDs: []int{10001}, ConfigPackage: "c.zip", HotFiles: "a.txt"}, true},
		{"无目标服", HotupdateParams{ConfigPackage: "c.zip", HotFiles: "a.txt"}, false},
		{"缺配置包", HotupdateParams{ServerIDs: []int{10001}, HotFiles: "a.txt"}, false},
		{"缺文件列表", HotupdateParams{ServerIDs: []int{10001}, ConfigPackage: "c.zip"}, false},
	}
	for _, c := range cases {
		err := c.p.Validate()
		if c.ok && err != nil {
			t.Errorf("%s: 期望通过,却报错: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: 期望报错,却通过", c.name)
		}
	}
}

func TestReleaseParamsHotUpdateFields(t *testing.T) {
	p := ReleaseParams{
		ServerIDs: []int{10001}, VersionPackage: "v.zip",
		IncludeHotUpdate: true, HotPackage: "cfg.zip", HotFiles: "a.ini,b.txt",
		HotFailedHosts: []string{"10.0.0.9"},
	}
	s, err := MarshalParams(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalReleaseParams(s)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IncludeHotUpdate || got.HotPackage != "cfg.zip" || got.HotFiles != "a.ini,b.txt" {
		t.Errorf("热更字段未round-trip: %+v", got)
	}
	if len(got.HotFailedHosts) != 1 || got.HotFailedHosts[0] != "10.0.0.9" {
		t.Errorf("HotFailedHosts = %v", got.HotFailedHosts)
	}
}

func TestReleaseParamsHotScopeRoundTrip(t *testing.T) {
	p := ReleaseParams{ServerIDs: []int{10001}, VersionPackage: "v.zip", IncludeHotUpdate: true, HotScope: "selected"}
	s, err := MarshalParams(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalReleaseParams(s)
	if err != nil {
		t.Fatal(err)
	}
	if got.HotScope != "selected" {
		t.Errorf("HotScope=%q want selected", got.HotScope)
	}
}

func TestConfigPushAndMergePublishBadge(t *testing.T) {
	if got := TypeLabel(TypeConfigPush); got != "全服更新" {
		t.Fatalf("configpush label = %q, want 全服更新", got)
	}
	if got := TypeLabel(TypeMergePublish); got != "合服预告发布" {
		t.Fatalf("mergepublish label = %q, want 合服预告发布", got)
	}
	b := TypeBadge(TypeConfigPush)
	if b.Label != "全服更新" || b.Class != "t-configpush" {
		t.Fatalf("configpush badge = %+v", b)
	}
	if TypeBadge(TypeMergePublish).Class != "t-mergepublish" {
		t.Fatalf("mergepublish badge class wrong")
	}
}

func TestNewServerTypeLabelAndBadge(t *testing.T) {
	if TypeLabel(TypeNewServer) != "创建游戏服" {
		t.Errorf("TypeLabel(newserver) = %q", TypeLabel(TypeNewServer))
	}
	b := TypeBadge(TypeNewServer)
	if b.Label != "创建游戏服" || b.Class == "t-default" {
		t.Errorf("TypeBadge = %+v", b)
	}
}

func TestNewServerCreateParamsRoundTrip(t *testing.T) {
	p := NewServerCreateParams{
		RegionName:     "洛阳",
		VersionPackage: "v1.2.3.zip",
		Rows: []NewServerRow{
			{Kind: "game", ID: 10002, DataBaseUser: "root", DataBasePsw: "pw",
				MySqlIp: "game-rds", MySqlPort: "3306", SelfPublicIp: "10.0.0.1",
				Fields: map[string]string{"Id": "10002", "WorldType": "0"}},
		},
		LastFailedIDs: []int{10002},
	}
	s, err := MarshalParams(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalNewServerCreateParams(s)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.RegionName != "洛阳" || got.VersionPackage != "v1.2.3.zip" || len(got.Rows) != 1 ||
		got.Rows[0].ID != 10002 || got.Rows[0].Fields["WorldType"] != "0" {
		t.Errorf("round trip = %+v", got)
	}
	if got.Summary() == "" {
		t.Errorf("Summary 不应为空")
	}
}

func TestHotupdateParamsRoundTrip(t *testing.T) {
	in := HotupdateParams{
		ServerIDs:     []int{210, 211},
		ConfigPackage: "cfg.zip",
		HotFiles:      "a.ini,b.txt",
		IncludeBattle: true,
		LastFailedIDs: []int{211},
	}
	s, err := MarshalParams(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := UnmarshalHotupdateParams(s)
	if err != nil {
		t.Fatal(err)
	}
	if !out.IncludeBattle {
		t.Error("IncludeBattle 应往返保留")
	}
	if len(out.LastFailedIDs) != 1 || out.LastFailedIDs[0] != 211 {
		t.Errorf("LastFailedIDs=%v, want [211]", out.LastFailedIDs)
	}
	if out.ConfigPackage != "cfg.zip" || out.HotFiles != "a.ini,b.txt" {
		t.Error("配置包/文件列表不应丢失")
	}
}

func TestDeleteServerParamsRoundTrip(t *testing.T) {
	p := DeleteServerParams{
		ID: 10002, Kind: "game", ServerName: "洛阳",
		SelfPublicIp: "10.0.0.1", DataBaseName: "ptdb_10002",
		MySqlIp: "g-rds", MySqlPort: "3306", DataBaseUser: "root", DataBasePsw: "pw",
		Fields: map[string]string{"Id": "10002", "WorldType": "0"},
		RemoveALB: true, RegenServerlist: true,
	}
	s, err := MarshalParams(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalDeleteServerParams(s)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 10002 || got.Kind != "game" || got.DataBaseName != "ptdb_10002" || !got.RemoveALB {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got.Summary() != "洛阳(Id 10002)" {
		t.Errorf("Summary = %q", got.Summary())
	}
}

func TestDeleteServerTypeLabel(t *testing.T) {
	if TypeLabel(TypeDeleteServer) != "删除游戏服" {
		t.Errorf("TypeLabel = %q", TypeLabel(TypeDeleteServer))
	}
	if TypeBadge(TypeDeleteServer).Class != "t-deleteserver" {
		t.Errorf("badge class = %q", TypeBadge(TypeDeleteServer).Class)
	}
}
