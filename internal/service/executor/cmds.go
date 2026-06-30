package executor

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"gongdan/internal/gameserver"
	"gongdan/internal/model"
)

// buildUpdateCmd 远程换包命令(kill + 全量换包)。
func buildUpdateCmd(scriptsDir string, id int, pkg string) string {
	return fmt.Sprintf("cd %s && ./update_server.sh %d %s", scriptsDir, id, pkg)
}

// buildStartCmd 远程开服命令。
// start.sh 前台启动会把游戏服 boot 输出(protobuf/assert 等噪音)打到 stdout/stderr,
// 这里重定向到远程 /tmp/start_<id>.log:成功路径静默(成败由 status.sh 判),
// 失败时再由 dumpStartLog 拉回该文件定位。参考 merge.sh 第8步启动处理。
func buildStartCmd(scriptsDir string, id int) string {
	return fmt.Sprintf("cd %s && ./start.sh %d -worldid=%d > /tmp/start_%d.log 2>&1", scriptsDir, id, id, id)
}

// buildStartLogCmd 拉取远程启动日志(start.sh/repair.sh 重定向的输出),仅失败时调用。
func buildStartLogCmd(id int) string {
	return fmt.Sprintf("cat /tmp/start_%d.log 2>/dev/null", id)
}

// buildStatusCmd 远程状态查询命令(返回位掩码,7=DB+HttpAgent+GameServer 全在)。
func buildStatusCmd(scriptsDir string, id int) string {
	return fmt.Sprintf("cd %s && ./status.sh %d", scriptsDir, id)
}

// probeVerMarker 分隔状态位掩码与包版本文件内容的标记(列表页一次SSH取状态+版本)。
const probeVerMarker = "__GD_VER__"

// buildStatusVersionCmd 列表页探测命令:一次SSH既取状态位掩码、又读包版本文件,
// 以 probeVerMarker 分隔两段。cat 失败(未部署/无文件)被 2>/dev/null 吞掉、版本段为空,
// 不影响前面的状态段;故仍含 "status.sh",对其按子串打桩的测试照常匹配。
func buildStatusVersionCmd(scriptsDir string, id int) string {
	return fmt.Sprintf("%s; echo %s; cat /export/server/server_%d/packetName.txt 2>/dev/null",
		buildStatusCmd(scriptsDir, id), probeVerMarker, id)
}

// buildHotUpdateCmd 远程热更命令(同主机多服一次执行)。
func buildHotUpdateCmd(scriptsDir, pkg, files string, ids []int) string {
	return fmt.Sprintf("cd %s && ./hot_update.sh %s %s %s", scriptsDir, pkg, files, joinIDs(ids))
}

// statusOK 判定 status.sh 的输出是否为成功(全部三组件就绪 = 7)。
func statusOK(output string) bool {
	return strings.TrimSpace(output) == "7"
}

// groupByHost 把服务器ID按所在主机IP分组;在映射中找不到IP的归入 missing。
func groupByHost(ids []int, ipMap map[int]string) (map[string][]int, []int) {
	groups := make(map[string][]int)
	var missing []int
	for _, id := range ids {
		ip, ok := ipMap[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		groups[ip] = append(groups[ip], id)
	}
	return groups, missing
}

// mergeListContent 把合服配对生成 merge.sh 需要的列表文件内容(每行 "target,source")。
func mergeListContent(pairs []model.MergePair) string {
	var sb strings.Builder
	for _, p := range pairs {
		fmt.Fprintf(&sb, "%d,%d\n", p.Target, p.Source)
	}
	return sb.String()
}

// joinIDs 把ID列表拼成逗号分隔字符串。
func joinIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

// buildDropDBCmd 远程丢库命令:在游戏服主机上用 mysql 客户端 DROP DATABASE。
// IF EXISTS 保证重试幂等。
func buildDropDBCmd(c gameserver.DBConn, dbName string) string {
	return fmt.Sprintf("mysql -h%s -P%s -u%s -p%s -e \"DROP DATABASE IF EXISTS %s\"",
		c.IP, c.Port, c.User, c.Pwd, dbName)
}

// buildRemovePackageCmd 远程删包命令:删除该服部署目录(天然幂等)。
func buildRemovePackageCmd(id int) string {
	return fmt.Sprintf("rm -rf /export/server/server_%d", id)
}

// buildStopCmd 远程优雅停服命令(踢人+关闭各组件)。
func buildStopCmd(id int) string {
	return fmt.Sprintf("cd /export/server/server_%d/OperationalTools && ./gameserver_stop.py -worldid=%d", id, id)
}

// buildKillCmd 远程强制关闭命令(kill -9 三个进程)。
func buildKillCmd(scriptsDir string, id int) string {
	return fmt.Sprintf("cd %s && ./kill.sh %d", scriptsDir, id)
}

// buildRepairCmd 远程修复启动命令(用于被强杀的服)。
// 同 start.sh,boot 输出重定向到远程 /tmp/start_<id>.log,失败时再拉回。
func buildRepairCmd(scriptsDir string, id int) string {
	return fmt.Sprintf("cd %s && ./repair.sh %d -worldid=%d > /tmp/start_%d.log 2>&1", scriptsDir, id, id, id)
}

// buildLoadSMCmd 远程内存启动命令(加载共享内存恢复,用于被强杀的服)。
func buildLoadSMCmd(id int) string {
	return fmt.Sprintf("cd /export/server/server_%d/OperationalTools && ./gameserver_repair_loadsm.py -worldid=%d", id, id)
}

// buildMaintenanceCmd 本地设维护命令:update_serverlist_stat.sh 0 <ids>。
func buildMaintenanceCmd(hefuDir string, ids []int) (name string, args []string) {
	script := fmt.Sprintf("cd %s && ./update_serverlist_stat.sh 0 %s", hefuDir, joinIDs(ids))
	return "bash", []string{"-c", script}
}

// packetVerRe 锚定版本三元组:大版本_大版本_DB版本,其后必紧跟 ≥8 位数字时间戳。
// 例 ProjectT_Release16.0-All_9_1_426_202605281817_.. → 捕获 9 / 1 / 426。
var packetVerRe = regexp.MustCompile(`_(\d+)_(\d+)_(\d+)_\d{8,}`)

// parsePacketVersion 从 packetName 解析大版本与期望DB版本。
// 用正则锚定"三个数字段 + 长时间戳"定位版本块,不受前缀段数变化影响;
// 格式不符则报错(fail-closed),由调用方据此让该服失败、不开服,而非猜出错误版本。
func parsePacketVersion(name string) (big string, tgt int, err error) {
	m := packetVerRe.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return "", 0, fmt.Errorf("无法从包名解析版本(应形如 ..._9_1_426_<时间戳>...): %q", name)
	}
	big = m[1] + "_" + m[2]
	tgt, err = strconv.Atoi(m[3])
	if err != nil {
		return "", 0, fmt.Errorf("包DB版本号非数字: %q", m[3])
	}
	return big, tgt, nil
}

// dbUpgradeSteps 生成从 cur 升到 tgt 的逐级 version 串(cur>=tgt 时为空)。
// 例:big=9_1, cur=424, tgt=426 → ["9_1_424_to_9_1_425","9_1_425_to_9_1_426"]。
func dbUpgradeSteps(big string, cur, tgt int) []string {
	var steps []string
	for v := cur + 1; v <= tgt; v++ {
		steps = append(steps, fmt.Sprintf("%s_%d_to_%s_%d", big, v-1, big, v))
	}
	return steps
}

// buildUpdateDBCmd 远程升级命令:进包内 UpdateDB 目录调 Update_DB.sh(凭据作参数)。
func buildUpdateDBCmd(id int, c gameserver.DBConn, version string) string {
	return fmt.Sprintf("cd /export/server/server_%d/SqlScript/UpdateDB && bash ./Update_DB.sh -worldid=%d -dbuser=%s -dbpwd=%s -dbip=%s -dbport=%s -version=%s",
		id, id, c.User, c.Pwd, c.IP, c.Port, version)
}

// ptdbInitRe 匹配 PTDBInit_<版本>/ 目录名,版本形如 9_1_426。
var ptdbInitRe = regexp.MustCompile(`PTDBInit_([0-9]+(?:_[0-9]+)+)/`)

// buildCreateDBVersionProbeCmd 探测包内 CreateDB 的 PTDBInit 版本目录(取版本号用)。
func buildCreateDBVersionProbeCmd(id int) string {
	return fmt.Sprintf("ls -d /export/server/server_%d/SqlScript/CreateDB/PTDBInit_*/ 2>/dev/null", id)
}

// buildCreateDBCmd 远程初始化建库命令:进包内 CreateDB 目录调 Create_DB.sh(凭据+版本作参数)。
func buildCreateDBCmd(id int, c gameserver.DBConn, version string) string {
	return fmt.Sprintf("cd /export/server/server_%d/SqlScript/CreateDB && bash ./Create_DB.sh -worldid=%d -dbuser=%s -dbpwd=%s -dbip=%s -dbport=%s -version=%s",
		id, id, c.User, c.Pwd, c.IP, c.Port, version)
}

// allCreateDBVersions 从 probe(ls)输出解析出所有 PTDBInit_<ver> 版本号(如 9_1_426)。
func allCreateDBVersions(probeOut string) []string {
	ms := ptdbInitRe.FindAllStringSubmatch(probeOut, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m[1])
	}
	return out
}

// pickCreateDBVersion 在包内 CreateDB 的 PTDBInit_* 目录中,选与包版本 want(如 9_1_426)一致的那个。
// 包内有多个版本目录时绝不"取第一个"猜,找不到一致的就 fail-closed 报错(避免按错误结构建库)。
func pickCreateDBVersion(probeOut, want string) (string, error) {
	vers := allCreateDBVersions(probeOut)
	for _, v := range vers {
		if v == want {
			return want, nil
		}
	}
	return "", fmt.Errorf("包内未找到与包版本一致的建库目录 PTDBInit_%s(实有: %v)", want, vers)
}

// buildConfigDownloadCmd 远程拉取最新配置:gd_download.sh <sid>(进各服 Config 目录 wget ServerConfigList.txt)。
func buildConfigDownloadCmd(scriptsDir string, id int) string {
	return fmt.Sprintf("cd %s && ./gd_download.sh %d", scriptsDir, id)
}

// buildPullConfigCmd 换包后、开服前拉最新配置:gd_pull_cnf.sh <sid>
// (进各服 Config 目录 wget ServerConfigList.txt + MergeServerFunction.txt),让服带新配置启动。
func buildPullConfigCmd(scriptsDir string, id int) string {
	return fmt.Sprintf("cd %s && ./gd_pull_cnf.sh %d", scriptsDir, id)
}

// buildGMHotUpdateCmd 本地 GM 热更命令:bash <脚本>(新脚本 gd_gmhot.sh 无需参数)。
func buildGMHotUpdateCmd(script string) (name string, args []string) {
	return "bash", []string{script}
}

// buildLoginLimitCmd 放开/设置登录限制:limit.sh 把 $2 $3 透传给
// gameserver_change_login_limit.py;limit=0 表示开放。
func buildLoginLimitCmd(scriptsDir string, id, limit int) string {
	return fmt.Sprintf("cd %s && ./limit.sh %d -worldid=%d -limit=%d", scriptsDir, id, id, limit)
}

// buildChangeLoginLimitCmd 直连该服 OperationalTools 下的 gameserver_change_login_limit.py
// 修改登录限制(limit∈{0,1,2,3,4,5,99};0=开放)。与 stop/loadsm 同款直调 python。
func buildChangeLoginLimitCmd(id, limit int) string {
	return fmt.Sprintf("cd /export/server/server_%d/OperationalTools && ./gameserver_change_login_limit.py -worldid=%d -limit=%d", id, id, limit)
}

// buildRestoreStatusCmd 本地恢复服为正常状态:update_serverlist_stat.sh 1 <ids>。
func buildRestoreStatusCmd(hefuDir string, ids []int) (name string, args []string) {
	script := fmt.Sprintf("cd %s && ./update_serverlist_stat.sh 1 %s", hefuDir, joinIDs(ids))
	return "bash", []string{"-c", script}
}

// isStopOK 停服是否成功:退出码 0(err==nil)或 104 都算已停。
func isStopOK(err error) bool {
	if err == nil {
		return true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 104 {
		return true
	}
	// 兜底(主要给测试用 fmt.Errorf("exit status 104")):用后缀匹配,避免误匹配 1040 等
	return strings.HasSuffix(err.Error(), "exit status 104")
}
