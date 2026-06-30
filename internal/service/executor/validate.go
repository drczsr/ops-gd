package executor

import (
	"fmt"
	"regexp"
	"strings"
)

// safeArgRe 允许拼入 shell 命令的字符:字母数字与 . _ / -(防止命令注入)。
var safeArgRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// hotForbiddenFiles 为禁止通过热更下发的文件(按文件名小写匹配,忽略路径)。
// 这两个文件由专门的受管流程维护,热更直接覆盖会破坏在线数据。
var hotForbiddenFiles = map[string]bool{
	"serverconfiglist.txt":    true,
	"mergeserverfunction.txt": true,
}

// validateArg 校验单个将拼入 shell 命令的参数(包名等)。
func validateArg(kind, v string) error {
	if v == "" {
		return fmt.Errorf("%s 不能为空", kind)
	}
	if !safeArgRe.MatchString(v) {
		return fmt.Errorf("%s 含非法字符(仅允许字母数字及 . _ / -): %q", kind, v)
	}
	return nil
}

// validateFileList 校验逗号分隔的文件列表中的每一项。
func validateFileList(files string) error {
	if strings.TrimSpace(files) == "" {
		return fmt.Errorf("热更文件列表不能为空")
	}
	for _, f := range strings.Split(files, ",") {
		f = strings.TrimSpace(f)
		if err := validateArg("热更文件", f); err != nil {
			return err
		}
		base := f
		if i := strings.LastIndexAny(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		if hotForbiddenFiles[strings.ToLower(base)] {
			return fmt.Errorf("热更不允许下发受管文件: %s", base)
		}
	}
	return nil
}
