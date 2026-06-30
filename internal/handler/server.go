package handler

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gongdan/internal/gameserver"
	"gongdan/internal/service/executor"
	"gongdan/internal/service/workflow"

	"github.com/gin-gonic/gin"
)

// visibleServers 套用系统设置「显示全部服务器」(与游戏服配置页 ListServers 同规则):
// 关 = 仅 Id>10000 且 WorldType∈{0,1,2,3};开 = 全部。
// 合服废弃服(WorldType==-1)永不作为独立行展示——它已合入目标服,改由目标服行上的
// 「已合入」徽章呈现(见 ServerList 的 MergedSources),无视开关。
func visibleServers(servers []gameserver.Server, showAll bool) []gameserver.Server {
	out := make([]gameserver.Server, 0, len(servers))
	for _, s := range servers {
		if s.WorldType == -1 {
			continue
		}
		if showAll || (s.ID > 10000 && s.WorldType >= 0 && s.WorldType <= 3) {
			out = append(out, s)
		}
	}
	return out
}

// ServerList 渲染「服务器管理」页:从配置库列出服(受「显示全部服务器」开关约束)。
// 只展示 ID/区服名/内外网IP/世界类型/所属战斗服 —— 绝不输出库账号密码。
func (h *Handler) ServerList(c *gin.Context) {
	var errMsg string
	servers, err := h.gsconfig.ServerSource().Servers()
	if err != nil {
		errMsg = err.Error()
		servers = nil
	}
	// 在过滤前用全量服建「目标服 -> 已合入源服」映射(废弃服随后被剔除,但其 RealWorldID 仍要归并)。
	mergedInto := gameserver.MergedSources(servers)
	servers = visibleServers(servers, h.settings.ShowAllServers())
	sort.Slice(servers, func(i, j int) bool { return servers[i].ID < servers[j].ID })

	// 顶部分组筛选 chip:按"区"归并(与游戏服配置页一致,WoRegion 优先、回退 Desc 前缀)。
	woRegion := map[int]string{}
	if rows, e := h.gsconfig.Store().AllRows(); e == nil {
		for _, r := range rows {
			if id, ok := strconv.Atoi(strings.TrimSpace(r["Id"])); ok == nil {
				woRegion[id] = gsRegion(r)
			}
		}
	}
	groups, regionOf := serverRegionGroups(servers, woRegion)

	h.render(c, "server_list.html", gin.H{
		"Title":      "服务器管理",
		"Servers":    servers,
		"MergedInto": mergedInto,
		"Groups":     groups,
		"RegionOf":   regionOf,
		"Err":        errMsg,
		"CanControl": h.canControlServers(c),
	})
}

// regionGroup 服务器管理页顶部一个分组 chip(区名 + 该区服数)。
type regionGroup struct {
	Region string
	Count  int
}

// serverRegionGroups 给每台展示中的服算"区"(WoRegion 优先,回退 Desc 前缀 GroupName,再回退「其他」),
// 返回按首次出现顺序排列的分组(含计数)与 ID->区 映射(供行 data-region 筛选)。
func serverRegionGroups(servers []gameserver.Server, woRegion map[int]string) ([]regionGroup, map[int]string) {
	regionOf := make(map[int]string, len(servers))
	idx := map[string]int{}
	var groups []regionGroup
	for _, s := range servers {
		region := strings.TrimSpace(woRegion[s.ID])
		if region == "" {
			region = strings.TrimSpace(gameserver.GroupName(s.Desc))
		}
		if region == "" {
			region = "其他"
		}
		regionOf[s.ID] = region
		i, ok := idx[region]
		if !ok {
			i = len(groups)
			idx[region] = i
			groups = append(groups, regionGroup{Region: region})
		}
		groups[i].Count++
	}
	return groups, regionOf
}

// canControlServers 是否可启停服务器:拥有「执行」角色即可(与工单执行权限一致)。
func (h *Handler) canControlServers(c *gin.Context) bool {
	return h.members.HasRole(currentUser(c), workflow.RoleExecute)
}

// findServer 按 ID 在配置库中查一台服(用于详情/启停)。
func (h *Handler) findServer(id int) (gameserver.Server, bool, error) {
	servers, err := h.gsconfig.ServerSource().Servers()
	if err != nil {
		return gameserver.Server{}, false, err
	}
	for _, s := range servers {
		if s.ID == id {
			return s, true, nil
		}
	}
	return gameserver.Server{}, false, nil
}

// srvStatusView 详情页当前状态的友好视图。
type srvStatusView struct {
	Label string
	Class string
	Mask  int
	Err   string
	Mock  bool
}

// statusView 把探测掩码转成详情页展示(与列表页同语义:7=在线,-1=离线,其余=异常)。
func statusView(r executor.ProbeResult, mock bool) srvStatusView {
	v := srvStatusView{Mask: r.Mask, Err: r.Err, Mock: mock}
	switch {
	case r.Mask == 7:
		v.Label, v.Class = "运行中", "st-on"
	case r.Mask == -1:
		v.Label, v.Class = "离线", "st-off"
	default:
		v.Label, v.Class = "异常", "st-warn"
	}
	return v
}

// detailField 详情页一行字段。
type detailField struct {
	Label string
	Value string
	Mono  bool // 等宽数字对齐(IP/端口/ID)
}

// detailGroup 详情页一个分组卡片。
type detailGroup struct {
	Title  string
	Fields []detailField
}

// serverSecretCols 详情页绝不展示的凭据列(遵循"不输出库账号密码")。
var serverSecretCols = map[string]bool{
	"DataBasePsw": true, "DataBaseUser": true, "RedisPsw": true, "RedisPswEx": true,
}

// ServerDetail 渲染单服详情页:分组配置信息(不含库凭据)+ 当前进程状态 + 启停操作。
func (h *Handler) ServerDetail(c *gin.Context) {
	id := int(parseID(c))
	s, ok, err := h.findServer(id)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		c.String(http.StatusNotFound, "服务器不存在")
		return
	}
	idStr := strconv.Itoa(id)
	row, _ := h.gsconfig.Store().GetServer(idStr) // 全列;失败则为 nil,builder 用零值兜底

	mode := h.settings.Mode()
	prober := executor.NewStatusProber(h.probeConfig())
	res := prober.Probe(mode, []gameserver.Server{s})[s.ID]

	// 「已合入的服」分组:本服作为合服目标时,列出合并进来的原服。
	configGroups := configGroups(row)
	if all, e := h.gsconfig.ServerSource().Servers(); e == nil {
		if g, ok := mergedSourcesGroup(gameserver.MergedSources(all)[s.ID]); ok {
			configGroups = append(configGroups, g)
		}
	}

	h.render(c, "server_detail.html", gin.H{
		"Title":      "服务器详情",
		"S":          s,
		"Status":     statusView(res, mode != "real"),
		"CanControl": h.canControlServers(c),
		"OverviewGroups": overviewGroups(s, row),
		"ConfigGroups":   configGroups,
		"DBName":     dashVal(gv(row, "DataBaseName")),
		"DBConn":     ipPort(gv(row, "MySqlIp"), gv(row, "MySqlPort")),
		"RedisMain":  ipPort(gv(row, "RedisIp"), gv(row, "RedisPort")),
		"RedisEx":    ipPort(gv(row, "RedisIpEx"), gv(row, "RedisPortEx")),
		"AllFields":   h.buildAllFields(row),
		"EditURL":     "/gsconfig/" + idStr,
		"ForceKilled": h.isForceKilled(s.ID), // 曾被强制停止 → 启动须走修复/内存
	}) //
}

// gv 取行中某列的去空白值(行为 nil 时返回空串)。
func gv(row map[string]string, k string) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row[k])
}

// dashVal 空值显示为长破折号。
func dashVal(v string) string {
	if strings.TrimSpace(v) == "" {
		return "—"
	}
	return v
}

// optID "-1"/空 视为"无"(战斗服、标签、合服等用 -1 作哨兵)。
func optID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "-1" {
		return "—"
	}
	return v
}

// boolCN 1/0 -> 开启/关闭。
func boolCN(v string) string {
	switch strings.TrimSpace(v) {
	case "1":
		return "开启"
	case "0":
		return "关闭"
	default:
		return optID(v)
	}
}

// ipPort 拼 "ip:port";ip 缺失返回破折号,port 缺失只返回 ip。
func ipPort(ip, port string) string {
	ip = strings.TrimSpace(ip)
	port = strings.TrimSpace(port)
	if ip == "" || ip == "-1" {
		return "—"
	}
	if port == "" || port == "-1" {
		return ip
	}
	return ip + ":" + port
}

func fld(label, value string) detailField  { return detailField{Label: label, Value: dashVal(value)} }
func fldM(label, value string) detailField { return detailField{Label: label, Value: dashVal(value), Mono: true} }

// mergedSourcesGroup 目标服详情页的「已合入的服」分组:列出合并进本服的原服。
// 无源服时返回 ok=false(不展示空分组)。
func mergedSourcesGroup(sources []gameserver.MergedSource) (detailGroup, bool) {
	if len(sources) == 0 {
		return detailGroup{}, false
	}
	fields := make([]detailField, 0, len(sources))
	for _, s := range sources {
		name := s.Desc
		if name == "" {
			name = "服" + strconv.Itoa(s.ID)
		}
		fields = append(fields, detailField{Label: "原服", Value: name + "(ID " + strconv.Itoa(s.ID) + ")"})
	}
	return detailGroup{Title: "已合入的服", Fields: fields}, true
}

// overviewGroups 「概览」Tab 的分组卡片:最常看的基本信息 + 网络信息。
func overviewGroups(s gameserver.Server, row map[string]string) []detailGroup {
	return []detailGroup{
		{Title: "基本信息", Fields: []detailField{
			fldM("服 ID", strconv.Itoa(s.ID)),
			fld("区服名", s.Desc),
			fld("服务器类型", gameserver.WorldTypeName(s.WorldType)),
			fld("标签 Tag", optID(gv(row, "Tag"))),
			fldM("真实世界ID", optID(gv(row, "RealWorldID"))),
			fld("数据库名", gv(row, "DataBaseName")),
		}},
		{Title: "网络信息", Fields: []detailField{
			fldM("公网IP", gv(row, "RealSelfPublicIp")),
			fldM("内网IP", gv(row, "SelfPublicIp")),
			fldM("公网IPv6", gv(row, "RealSelfPublicIpv6")),
			fldM("内网IPv6", gv(row, "SelfPublicIpv6")),
			fld("公网域名", gv(row, "RealSelfPublicUrl")),
			fldM("客户端端口", gv(row, "PortForClient")),
			fldM("GM端口", gv(row, "PortForGMServer")),
		}},
	}
}

// configGroups 「配置详情」Tab 的分组卡片:拓扑/进程/容量(+合服,仅在确有合服时)。
func configGroups(row map[string]string) []detailGroup {
	groups := []detailGroup{
		{Title: "拓扑关系", Fields: []detailField{
			fldM("所属战场服", optID(gv(row, "BattleWorldID"))),
			fldM("大世界ID", optID(gv(row, "BigWorldID"))),
			fldM("全局中心服", optID(gv(row, "GlobalCenterWorldID"))),
			fldM("大世界端口", gv(row, "PortForBigWorld")),
			fldM("战场服端口", gv(row, "PortForBattleWorld")),
			fldM("战场副本端口", gv(row, "PortForBattleCopySceneWorld")),
			fldM("全局中心端口", gv(row, "PortForGlobalCenter")),
		}},
		{Title: "进程与 Agent", Fields: []detailField{
			fldM("服务器线程", gv(row, "ServerThreadCount")),
			fldM("DB线程", gv(row, "DBThreadCount")),
			fldM("Http线程", gv(row, "HttpThreadCount")),
			fldM("DB Agent", ipPort(gv(row, "DBIP"), gv(row, "DBPort"))),
			fldM("Http Agent", ipPort(gv(row, "HttpIP"), gv(row, "HttpPort"))),
			fldM("Http公共端口", gv(row, "HttpServerCommonPort")),
		}},
		{Title: "容量与开关", Fields: []detailField{
			fldM("最大在线", gv(row, "MaxOnlinePlayingPlayer")),
			fldM("最大排队", gv(row, "MaxOnlineQueuingPlayer")),
			fld("CDKey开放", boolCN(gv(row, "CDKeyOpen"))),
			fld("账号兑换", boolCN(gv(row, "AccountRedeemOpen"))),
			fldM("ConnectCot", gv(row, "ConnectCot")),
		}},
	}
	// 合服信息:仅在确有合服时展示(MergeStatus≠0 或 合服数量>0)。
	if ms := gv(row, "MergeStatus"); (ms != "" && ms != "0") || (gv(row, "MergeServerCount") != "" && gv(row, "MergeServerCount") != "0") {
		mergeDT := func(dateK, timeK string) string {
			var parts []string
			if d := optID(gv(row, dateK)); d != "—" {
				parts = append(parts, d)
			}
			if t := optID(gv(row, timeK)); t != "—" {
				parts = append(parts, t)
			}
			return strings.Join(parts, " ")
		}
		groups = append(groups, detailGroup{Title: "合服信息", Fields: []detailField{
			fldM("合服状态", gv(row, "MergeStatus")),
			fldM("合服数量", gv(row, "MergeServerCount")),
			fldM("合服公会数", gv(row, "MergeGuildCount")),
			fld("合服开始", mergeDT("MergeStartDate", "MergeStartTime")),
			fld("合服结束", mergeDT("MergeEndDate", "MergeEndTime")),
		}})
	}
	return groups
}

// buildAllFields 按配置列序输出全部字段(凭据除外),标签优先用列中文描述。
func (h *Handler) buildAllFields(row map[string]string) []detailField {
	store := h.gsconfig.Store()
	cols, err := store.Columns()
	if err != nil {
		return nil
	}
	desc, _ := store.ColumnDescriptions()
	out := make([]detailField, 0, len(cols))
	for _, c := range cols {
		if serverSecretCols[c.Name] {
			continue
		}
		label := c.Name
		if d := desc[c.Name]; d != "" {
			label = d + " (" + c.Name + ")"
		}
		out = append(out, detailField{Label: label, Value: dashVal(gv(row, c.Name)), Mono: true})
	}
	return out
}

// serverAction 启停共用:鉴权 → 查服 → 执行 → 返回 JSON {ok, output, error}。
// 强杀集合维护:被「强制停止(kill)」的服需用修复/内存启动,普通启动会被拦截。
func (h *Handler) markForceKilled(id int)  { h.killedMu.Lock(); h.killed[id] = true; h.killedMu.Unlock() }
func (h *Handler) clearForceKilled(id int) { h.killedMu.Lock(); delete(h.killed, id); h.killedMu.Unlock() }
func (h *Handler) isForceKilled(id int) bool {
	h.killedMu.Lock()
	defer h.killedMu.Unlock()
	return h.killed[id]
}
func (h *Handler) forceKilledSnapshot() map[int]bool {
	h.killedMu.Lock()
	defer h.killedMu.Unlock()
	out := make(map[int]bool, len(h.killed))
	for k, v := range h.killed {
		if v {
			out[k] = true
		}
	}
	return out
}

func (h *Handler) serverAction(c *gin.Context, run func(*executor.ServerControl, gameserver.Server, executor.LogFunc) error, after func(s gameserver.Server, ok bool)) {
	if !h.canControlServers(c) {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "无操作权限(需「执行」角色)"})
		return
	}
	s, ok, err := h.findServer(int(parseID(c)))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "服务器不存在"})
		return
	}
	var sb strings.Builder
	logFn := func(line string) { sb.WriteString(line); sb.WriteString("\n") }
	sc := executor.NewServerControl(h.settings.Mode(), h.probeConfig())
	aerr := run(sc, s, logFn)
	if after != nil {
		after(s, aerr == nil)
	}
	resp := gin.H{"ok": aerr == nil, "output": sb.String()}
	if aerr != nil {
		resp["error"] = aerr.Error()
	}
	c.JSON(http.StatusOK, resp)
}

// ServerStart 启动单服。mode= repair(修复启动)/ loadsm(内存启动)/ 空(普通启动)。
// 被强制停止过的服,普通启动会被拦截(返回 needRepair),提示改用修复/内存启动。
func (h *Handler) ServerStart(c *gin.Context) {
	if !h.canControlServers(c) {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "无操作权限(需「执行」角色)"})
		return
	}
	s, ok, err := h.findServer(int(parseID(c)))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "服务器不存在"})
		return
	}
	mode := c.Query("mode")
	if mode == "" {
		mode = c.PostForm("mode")
	}
	if mode == "" && h.isForceKilled(s.ID) {
		c.JSON(http.StatusOK, gin.H{"ok": false, "needRepair": true,
			"error": "该服曾被强制停止,请用「修复启动」或「内存启动」"})
		return
	}
	var sb strings.Builder
	logFn := func(line string) { sb.WriteString(line); sb.WriteString("\n") }
	sc := executor.NewServerControl(h.settings.Mode(), h.probeConfig())
	var aerr error
	switch mode {
	case "repair":
		aerr = sc.Repair(s, logFn)
	case "loadsm":
		aerr = sc.LoadSM(s, logFn)
	default:
		aerr = sc.StartAutoRecover(s, logFn) // 普通启动;遇组件残留自动强杀+修复启动
	}
	if aerr == nil {
		h.clearForceKilled(s.ID) // 启动成功 → 解除强杀标记
	}
	resp := gin.H{"ok": aerr == nil, "output": sb.String()}
	if aerr != nil {
		resp["error"] = aerr.Error()
	}
	c.JSON(http.StatusOK, resp)
}

// ServerStop 停单服。mode=force 强制停服(kill,记入强杀集合),否则优雅停服。
func (h *Handler) ServerStop(c *gin.Context) {
	force := c.Query("mode") == "force" || c.PostForm("mode") == "force"
	h.serverAction(c, func(sc *executor.ServerControl, s gameserver.Server, log executor.LogFunc) error {
		if force {
			return sc.Kill(s, log)
		}
		return sc.Stop(s, log)
	}, func(s gameserver.Server, ok bool) {
		if force && ok {
			h.markForceKilled(s.ID)
		}
	})
}

// parseIDList 解析逗号分隔的服ID串(忽略空白/非法项)。
func parseIDList(s string) []int {
	var ids []int
	for _, part := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			ids = append(ids, n)
		}
	}
	return ids
}

// batchResult 批量操作单服结果。
type batchResult struct {
	ID    int    `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// ServerBatch 批量启停(直接动作)。表单:op=start|stop,ids=逗号分隔服ID。
// 各服并发执行(有界),返回逐服结果供前端汇总展示。
func (h *Handler) ServerBatch(c *gin.Context) {
	if !h.canControlServers(c) {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "无操作权限(需「执行」角色)"})
		return
	}
	op := c.PostForm("op")
	if op != "start" && op != "stop" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "未知操作"})
		return
	}
	ids := parseIDList(c.PostForm("ids"))
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "未选择服务器"})
		return
	}
	servers, err := h.gsconfig.ServerSource().Servers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	byID := make(map[int]gameserver.Server, len(servers))
	for _, s := range servers {
		byID[s.ID] = s
	}

	force := c.PostForm("mode") == "force"
	sc := executor.NewServerControl(h.settings.Mode(), h.probeConfig())
	results := make([]batchResult, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // 有界并发,护住 sshd
	for i, id := range ids {
		s, ok := byID[id]
		if !ok {
			results[i] = batchResult{ID: id, OK: false, Error: "服务器不存在"}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i, id int, s gameserver.Server) {
			defer wg.Done()
			defer func() { <-sem }()
			var aerr error
			switch {
			case op == "start":
				aerr = sc.StartAutoRecover(s, func(string) {}) // 批量启动同样自动恢复残留
			case force:
				aerr = sc.Kill(s, func(string) {})
			default:
				aerr = sc.Stop(s, func(string) {})
			}
			if aerr == nil && op == "start" {
				h.clearForceKilled(id) // 批量启动成功 → 解除强杀标记
			} else if aerr == nil && op == "stop" && force {
				h.markForceKilled(id) // 批量强制停止 → 记入强杀集合
			}
			r := batchResult{ID: id, OK: aerr == nil}
			if aerr != nil {
				r.Error = aerr.Error()
			}
			results[i] = r
		}(i, id, s)
	}
	wg.Wait()
	c.JSON(http.StatusOK, gin.H{"ok": true, "results": results})
}

// validLoginLimit 允许的登录限制值(0=开放,99=最严)。
func validLoginLimit(v int) bool {
	switch v {
	case 0, 1, 2, 3, 4, 5, 99:
		return true
	}
	return false
}

// ServerBatchLoginLimit 批量修改登录限制。表单:ids=逗号分隔服ID,limit∈{0,1,2,3,4,5,99}。
// 各服并发执行(有界),返回逐服结果供前端汇总。
func (h *Handler) ServerBatchLoginLimit(c *gin.Context) {
	if !h.canControlServers(c) {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "无操作权限(需「执行」角色)"})
		return
	}
	limit, err := strconv.Atoi(strings.TrimSpace(c.PostForm("limit")))
	if err != nil || !validLoginLimit(limit) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "登录限制值非法(允许 0/1/2/3/4/5/99)"})
		return
	}
	ids := parseIDList(c.PostForm("ids"))
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "未选择服务器"})
		return
	}
	servers, err := h.gsconfig.ServerSource().Servers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	byID := make(map[int]gameserver.Server, len(servers))
	for _, s := range servers {
		byID[s.ID] = s
	}
	sc := executor.NewServerControl(h.settings.Mode(), h.probeConfig())
	results := make([]batchResult, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // 有界并发,护住 sshd
	for i, id := range ids {
		s, ok := byID[id]
		if !ok {
			results[i] = batchResult{ID: id, OK: false, Error: "服务器不存在"}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i, id int, s gameserver.Server) {
			defer wg.Done()
			defer func() { <-sem }()
			aerr := sc.ChangeLoginLimit(s, limit, func(string) {})
			r := batchResult{ID: id, OK: aerr == nil}
			if aerr != nil {
				r.Error = aerr.Error()
			}
			results[i] = r
		}(i, id, s)
	}
	wg.Wait()
	c.JSON(http.StatusOK, gin.H{"ok": true, "results": results})
}

// ServerStatus 探测服进程状态(status.sh 位掩码,7=三组件全在),
// 范围与列表页一致(受「显示全部服务器」开关约束,不探隐藏的服)。
// mock 模式不连网全部按在线返回;real 模式 SSH 并发探测(只读)。
func (h *Handler) ServerStatus(c *gin.Context) {
	servers, err := h.gsconfig.ServerSource().Servers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	servers = visibleServers(servers, h.settings.ShowAllServers())
	mode := h.settings.Mode()
	prober := executor.NewStatusProber(h.probeConfig())
	c.JSON(http.StatusOK, gin.H{
		"mock":     mode != "real",
		"statuses": prober.Probe(mode, servers),
		"killed":   h.forceKilledSnapshot(), // 被强制停止过的服(前端启动时拦截)
	})
}

// ServerStatusOne 探测单台服状态(供启停后前端轮询到目标态,避免每次全量探测全部服)。
func (h *Handler) ServerStatusOne(c *gin.Context) {
	s, ok, err := h.findServer(int(parseID(c)))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "服务器不存在"})
		return
	}
	mode := h.settings.Mode()
	prober := executor.NewStatusProber(h.probeConfig())
	c.JSON(http.StatusOK, gin.H{
		"mock":   mode != "real",
		"status": prober.Probe(mode, []gameserver.Server{s})[s.ID],
		"killed": h.isForceKilled(s.ID),
	})
}

// probeConfig 从全局配置抽出状态探测所需的 SSH 子集(不含任何工单执行依赖)。
func (h *Handler) probeConfig() *executor.RealConfig {
	e := h.cfg.Executor
	return &executor.RealConfig{
		SSHKey:           e.SSHKey,
		SSHPort:          e.SSHPort,
		SSHUser:          e.SSHUser,
		RemoteScriptsDir: e.RemoteScriptsDir,
		Concurrency:      e.Concurrency,
		SSHMultiplex:     e.SSHMultiplex,
	}
}
