package executor

import (
	"fmt"
	"regexp"
	"strings"
)

// tsRunRe 匹配包名里的长数字串(≥8位),用于定位时间戳(如 202606201440)取"最新"。
var tsRunRe = regexp.MustCompile(`\d{8,}`)

// pkgTimestamp 返回包名里最长(同长取最大)的数字串作时间戳;无则空串。
func pkgTimestamp(name string) string {
	best := ""
	for _, m := range tsRunRe.FindAllString(name, -1) {
		if len(m) > len(best) || (len(m) == len(best) && m > best) {
			best = m
		}
	}
	return best
}

// tsNewer 报告时间戳 a 是否比 b 新(长度优先,同长按字典序;时间戳定长时即按时间)。
func tsNewer(a, b string) bool {
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a > b
}

// MatchMergeTool 在 pkgs 中挑"名含 dbmerge 且含版本段 ver(如 9_1_427)"的、时间戳最新的包名。
// 版本段以下划线包界(_9_1_427_)匹配,避免把 9_1_427 误配到 9_1_4270。无匹配返回 ""。
func MatchMergeTool(ver string, pkgs []string) string {
	if ver == "" {
		return ""
	}
	seg := "_" + ver + "_"
	best, bestTS := "", ""
	for _, p := range pkgs {
		if !strings.Contains(strings.ToLower(p), "dbmerge") {
			continue
		}
		if !strings.Contains(p, seg) {
			continue
		}
		ts := pkgTimestamp(p)
		if best == "" || tsNewer(ts, bestTS) || (ts == bestTS && p > best) {
			best, bestTS = p, ts
		}
	}
	return best
}

// ServerPacketVersion SSH 读服 id 的 packetName.txt,解析出版本段(形如 "9_1_427")。
// 供建单时据"被合服的实际版本"匹配合服工具包。
func ServerPacketVersion(cfg *RealConfig, ip string, id int, log LogFunc) (string, error) {
	out, err := RunSSH(cfg, ip, buildPacketNameCmd(id), log)
	if err != nil {
		return "", err
	}
	big, tgt, err := parsePacketVersion(out)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%d", big, tgt), nil
}
