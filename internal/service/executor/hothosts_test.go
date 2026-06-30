package executor

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// 错峰:每台主机下发前 sleep(launchDelay)。
func TestHotHostRunnerStaggers(t *testing.T) {
	var mu sync.Mutex
	var delays []time.Duration
	h := &hotHostRunner{
		runSSH:      func(ip, cmd string, log LogFunc) (string, error) { return "", nil },
		sleep:       func(d time.Duration) { mu.Lock(); delays = append(delays, d); mu.Unlock() },
		launchDelay: 400 * time.Millisecond,
		concurrency: 10,
		scriptsDir:  "/s",
	}
	hostIDs := map[string][]int{"1.1.1.1": {10001}, "1.1.1.2": {10002}}
	failed := h.hotUpdateWaves([]string{"1.1.1.1", "1.1.1.2"}, hostIDs, "cfg.zip", "a.ini", log_noop)
	if len(failed) != 0 {
		t.Fatalf("不应有失败: %v", failed)
	}
	if len(delays) != 2 {
		t.Errorf("应错峰 2 次,实际 %d", len(delays))
	}
}

// 分波重试:首波失败的主机,下一波重试成功 → 最终无失败,且 sleep 了波间隔。
func TestHotHostRunnerWaveRetry(t *testing.T) {
	var mu sync.Mutex
	attempts := map[string]int{}
	var waveSleeps []time.Duration
	h := &hotHostRunner{
		runSSH: func(ip, cmd string, log LogFunc) (string, error) {
			mu.Lock()
			attempts[ip]++
			n := attempts[ip]
			mu.Unlock()
			if ip == "1.1.1.2" && n == 1 { // 第二台首次失败,重试成功
				return "", fmt.Errorf("connection refused")
			}
			return "", nil
		},
		sleep:            func(d time.Duration) { mu.Lock(); waveSleeps = append(waveSleeps, d); mu.Unlock() },
		concurrency:      10,
		scriptsDir:       "/s",
		hotRetryBackoffs: []time.Duration{1, 1, 1},
	}
	hostIDs := map[string][]int{"1.1.1.1": {10001}, "1.1.1.2": {10002}}
	failed := h.hotUpdateWaves([]string{"1.1.1.1", "1.1.1.2"}, hostIDs, "cfg.zip", "a.ini", log_noop)
	if len(failed) != 0 {
		t.Fatalf("重试后应成功,仍失败: %v", failed)
	}
	if attempts["1.1.1.2"] != 2 {
		t.Errorf("第二台应尝试 2 次,实际 %d", attempts["1.1.1.2"])
	}
}

// hot_update.sh 永远 exit 0,单服 reload 失败只体现在输出里。
// SSH 退出码为 0(err=nil)但输出含 reload 失败标记 → 必须判该主机失败。
func TestHotHostRunnerDetectsReloadFailureInOutput(t *testing.T) {
	out := "" +
		"\033[32m --------------Start update server_237--Recharge.txt------------- \033[0m\n" +
		"**********************reload GameServer faild! solar-command(52)...**********************\n"
	h := &hotHostRunner{
		runSSH:           func(ip, cmd string, log LogFunc) (string, error) { return out, nil }, // exit 0
		sleep:            func(time.Duration) {},
		concurrency:      10,
		scriptsDir:       "/s",
		hotRetryBackoffs: nil, // 不重试,直接看首波结果
	}
	hostIDs := map[string][]int{"1.1.1.1": {237}}
	failed := h.hotUpdateWaves([]string{"1.1.1.1"}, hostIDs, "cfg.zip", "Recharge.txt", log_noop)
	if len(failed) != 1 || failed[0] != "1.1.1.1" {
		t.Errorf("输出含 reload 失败标记应判主机失败,实际 %v", failed)
	}
}

// 输出全是成功(reload XXX...,无失败标记)→ 不误判。
func TestHotHostRunnerSuccessOutputNotFlagged(t *testing.T) {
	out := "**********************reload GameServer...**********************\n" +
		"**********************reload DBAgent...**********************\n"
	h := &hotHostRunner{
		runSSH:      func(ip, cmd string, log LogFunc) (string, error) { return out, nil },
		sleep:       func(time.Duration) {},
		concurrency: 10,
		scriptsDir:  "/s",
	}
	hostIDs := map[string][]int{"1.1.1.1": {235}}
	failed := h.hotUpdateWaves([]string{"1.1.1.1"}, hostIDs, "cfg.zip", "Recharge.txt", log_noop)
	if len(failed) != 0 {
		t.Errorf("成功输出不应判失败,实际 %v", failed)
	}
}

// hotFailureLines:命中 reload 失败与 solar-command 错误码;不误伤含 failed 的文件名。
func TestHotFailureLines(t *testing.T) {
	if got := hotFailureLines("reload GameServer faild! solar-command(52)"); len(got) != 1 {
		t.Errorf("应命中 reload faild,got %v", got)
	}
	if got := hotFailureLines("reload HttpAgent failed"); len(got) != 1 {
		t.Errorf("应命中 reload failed,got %v", got)
	}
	if got := hotFailureLines("--------------Start update server_1--failed_quest.txt-------------"); len(got) != 0 {
		t.Errorf("文件名含 failed 不应误判,got %v", got)
	}
	if got := hotFailureLines("reload GameServer..."); len(got) != 0 {
		t.Errorf("成功行不应命中,got %v", got)
	}
}

// 波数用尽仍失败 → 返回失败主机。
func TestHotHostRunnerWaveExhausted(t *testing.T) {
	h := &hotHostRunner{
		runSSH:           func(ip, cmd string, log LogFunc) (string, error) { return "", fmt.Errorf("boom") },
		sleep:            func(time.Duration) {},
		concurrency:      10,
		scriptsDir:       "/s",
		hotRetryBackoffs: []time.Duration{1, 1},
	}
	hostIDs := map[string][]int{"1.1.1.1": {10001}}
	failed := h.hotUpdateWaves([]string{"1.1.1.1"}, hostIDs, "cfg.zip", "a.ini", log_noop)
	if len(failed) != 1 || failed[0] != "1.1.1.1" {
		t.Errorf("应返回失败主机 [1.1.1.1],实际 %v", failed)
	}
}
