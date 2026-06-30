package executor

import "testing"

func TestMatchMergeTool(t *testing.T) {
	pkgs := []string{
		"ProjectT_Release16.0-All_9_1_427_202606201000_release.zip", // 游戏服包,非工具包
		"DBMergeTool_9_1_426_426_202606101200_release_16_0.zip",     // 版本不符
		"DBMergeTool_9_1_427_427_202606201440_release_16_0.zip",     // 匹配,较新
		"DBMergeTool_9_1_427_427_202606150900_release_16_0.zip",     // 匹配,较旧
		"ProjectT_Config_9_1_427_202606201500_release.zip",          // 配置包,非工具包
	}
	got := MatchMergeTool("9_1_427", pkgs)
	want := "DBMergeTool_9_1_427_427_202606201440_release_16_0.zip"
	if got != want {
		t.Errorf("MatchMergeTool = %q, want %q(应取版本匹配且时间戳最新的工具包)", got, want)
	}
}

func TestMatchMergeToolNoMatch(t *testing.T) {
	pkgs := []string{
		"DBMergeTool_9_1_426_426_202606101200_release_16_0.zip",
		"ProjectT_Release16.0-All_9_1_427_202606201000_release.zip",
	}
	if got := MatchMergeTool("9_1_427", pkgs); got != "" {
		t.Errorf("无匹配应返回空, got %q", got)
	}
	if got := MatchMergeTool("", pkgs); got != "" {
		t.Errorf("空版本应返回空, got %q", got)
	}
}

func TestMatchMergeToolBoundedVersion(t *testing.T) {
	// "9_1_427" 不应误匹配 "9_1_4270"(DB版本不同),靠下划线边界区分。
	pkgs := []string{"DBMergeTool_9_1_4270_4270_202606201440_release_16_0.zip"}
	if got := MatchMergeTool("9_1_427", pkgs); got != "" {
		t.Errorf("不应误匹配更长DB版本, got %q", got)
	}
}
