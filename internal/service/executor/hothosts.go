package executor

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// hotHostRunner 主机级热更的公共逻辑:错峰并发下发 + 失败主机分波重试 + GM热更收尾。
// 由 ReleaseExecutor 与 HotUpdateExecutor 共用,保证两条热更路径行为一致。
// 由各执行器在调用点按当前字段现建(见各自的 hotRunner()),以便测试注入的桩函数生效。
type hotHostRunner struct {
	runSSH           sshRunner
	runLocal         func(name string, args, env []string, log LogFunc) (string, error)
	sleep            func(time.Duration)
	launchDelay      time.Duration
	concurrency      int
	scriptsDir       string
	hotRetryBackoffs []time.Duration
	gmScript         string
}

// runHostsConcurrent 按 全局并发limit + 启动错峰launchDelay 对每台主机执行 fn,返回 fn 报错的主机IP。
func (h *hotHostRunner) runHostsConcurrent(hosts []string, limit int, fn func(ip string) error) []string {
	type res struct {
		ip  string
		err error
	}
	results := make(chan res, len(hosts))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, ip := range hosts {
		if h.launchDelay > 0 {
			h.sleep(h.launchDelay)
		}
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results <- res{ip, fn(ip)}
		}(ip)
	}
	wg.Wait()
	close(results)
	var failed []string
	for r := range results {
		if r.err != nil {
			failed = append(failed, r.ip)
		}
	}
	return failed
}

// hotUpdateWaves 分波执行:每波只跑上一波失败的主机,波间隔取 hotRetryBackoffs。
func (h *hotHostRunner) hotUpdateWaves(hosts []string, hostIDs map[string][]int, pkg, files string, log LogFunc) []string {
	limit := h.concurrency
	if limit < 1 {
		limit = 1
	}
	remaining := hosts
	for wave := 0; ; wave++ {
		failed := h.runHostsConcurrent(remaining, limit, func(ip string) error {
			cmd := buildHotUpdateCmd(h.scriptsDir, pkg, files, hostIDs[ip])
			out, err := h.runSSH(ip, cmd, log)
			if err != nil {
				log(fmt.Sprintf("  [热更失败] 主机 %s: %v", ip, err))
				return err
			}
			// hot_update.sh 始终 exit 0,单服 reload 失败只在输出里(reload ... faild!/solar-command),
			// 故退出码为 0 也要扫输出兜底,避免漏判把失败当成功。
			if bad := hotFailureLines(out); len(bad) > 0 {
				log(fmt.Sprintf("  [热更失败] 主机 %s:检测到 %d 处 reload 报错:", ip, len(bad)))
				for _, ln := range bad {
					log("    " + ln)
				}
				return fmt.Errorf("主机 %s 热更失败(%d 处 reload 报错)", ip, len(bad))
			}
			log(fmt.Sprintf("  [热更] 主机 %s 完成", ip))
			return nil
		})
		if len(failed) == 0 {
			return nil
		}
		if wave >= len(h.hotRetryBackoffs) {
			return failed
		}
		d := h.hotRetryBackoffs[wave]
		log(fmt.Sprintf("热更失败 %d 台主机,%v 后重试(第 %d 波)", len(failed), d, wave+2))
		h.sleep(d)
		remaining = failed
	}
}

// hotFailureLines 扫描 hot_update.sh 输出里表示热更失败的行。
// 远程脚本对单服 reload 失败不改退出码(始终 exit 0),只看退出码会漏判,故按关键字兜底:
//   - "reload ... fail"(gameserver_reload.py 失败,含其错拼 "faild"; "fail" 是 "faild/failed" 的子串)
//   - "solar-command(" (其错误子系统标记,如 solar-command(52),失败时才出现)
// 只在含 "reload" 时匹配 "fail",避免把含 "failed" 的文件名/包名误判。
func hotFailureLines(out string) []string {
	var bad []string
	for _, ln := range strings.Split(out, "\n") {
		low := strings.ToLower(ln)
		if strings.Contains(low, "solar-command(") ||
			(strings.Contains(low, "reload") && strings.Contains(low, "fail")) {
			bad = append(bad, strings.TrimSpace(ln))
		}
	}
	return bad
}

// gmHotUpdate 执行一次本地 GM 热更(gd_gmhot.sh,无参)。尽力而为:失败仅记日志,不影响工单成败。
func (h *hotHostRunner) gmHotUpdate(log LogFunc) {
	name, args := buildGMHotUpdateCmd(h.gmScript)
	log(fmt.Sprintf("=== GM热更: %s %s ===", name, strings.Join(args, " ")))
	if out, err := h.runLocal(name, args, nil, log); err != nil {
		log(fmt.Sprintf("  [告警] GM热更失败(不影响发版结果): %v", err))
	} else if out != "" {
		log("  GM热更输出: " + out)
	}
}
