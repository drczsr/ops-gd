package executor

import "testing"

func TestValidateArg(t *testing.T) {
	good := []string{"v.zip", "ProjectT_Release_Config_9_1.zip", "Boss/Jizhi/Jishu.xml", "a-b_c.1"}
	for _, g := range good {
		if err := validateArg("包名", g); err != nil {
			t.Errorf("%q 应合法, got %v", g, err)
		}
	}
	bad := []string{"", "v.zip; rm -rf /", "a b", "$(whoami)", "a|b", "a&&b", "`x`"}
	for _, b := range bad {
		if err := validateArg("包名", b); err == nil {
			t.Errorf("%q 应判为非法", b)
		}
	}
}

func TestValidateFileList(t *testing.T) {
	if err := validateFileList("a.ini,b.txt,Boss/c.xml"); err != nil {
		t.Errorf("合法列表报错: %v", err)
	}
	if err := validateFileList(""); err == nil {
		t.Error("空列表应判为非法")
	}
	if err := validateFileList("a.ini,b c.txt"); err == nil {
		t.Error("含空格的项应判为非法")
	}
	if err := validateFileList("a.ini,x;rm -rf /"); err == nil {
		t.Error("含分号的项应判为非法")
	}
	for _, f := range []string{"ServerConfigList.txt", "MergeServerFunction.txt", "serverconfiglist.txt", "Boss/ServerConfigList.txt", "a.ini,MergeServerFunction.txt"} {
		if err := validateFileList(f); err == nil {
			t.Errorf("%q 含受管文件应判为非法", f)
		}
	}
}
