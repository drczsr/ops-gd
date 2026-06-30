package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gongdan/internal/model"
	"gongdan/internal/service/executor"
	"gongdan/internal/service/workflow"

	"github.com/gin-gonic/gin"
)

// orderRow 列表页一行:工单 + 当前用户在该状态下可执行的快捷操作。
type orderRow struct {
	WO            model.WorkOrder
	Actions       []button
	Details       []ParamRow
	IsConfigOrder bool // configpush/mergepublish:审批弹窗异步拉 diff 摘要
}

func (h *Handler) OrderList(c *gin.Context) {
	woType := c.Query("type")
	status := c.Query("status")
	rows, err := h.orders.List(woType, status)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	h.renderOrderList(c, "order_list.html", gin.H{
		"Title":   "工单列表",
		"Heading": "工单列表",
		"ShowNew": true,
	}, woType, status, rows)
}

// OrderMine 我的工单:当前用户提交的工单。
func (h *Handler) OrderMine(c *gin.Context) {
	woType := c.Query("type")
	rows, err := h.orders.ListMine(currentUser(c), woType)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	h.renderOrderList(c, "order_list.html", gin.H{
		"Title":   "我的工单",
		"Heading": "我的工单",
		"Empty":   "你还没有提交过工单",
	}, woType, "", rows)
}

// OrderPending 待审批:处于待审批状态的工单(列表页内联可审批/驳回)。
func (h *Handler) OrderPending(c *gin.Context) {
	woType := c.Query("type")
	rows, err := h.orders.List(woType, workflow.StatusPendingApprove)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	h.renderOrderList(c, "order_list.html", gin.H{
		"Title":   "待审批",
		"Heading": "待审批",
		"Empty":   "暂无待审批工单",
	}, woType, workflow.StatusPendingApprove, rows)
}

// renderOrderList 把工单列表统一渲染为列表页(共用 order_list.html)。
func (h *Handler) renderOrderList(c *gin.Context, tmpl string, data gin.H, woType, status string, rows []model.WorkOrder) {
	user := currentUser(c)
	roles := h.members.Roles(user)
	view := make([]orderRow, len(rows))
	for i := range rows {
		view[i] = orderRow{
			WO:            rows[i],
			Actions:       availableButtons(&rows[i], user, roles),
			Details:       paramRows(&rows[i]),
			IsConfigOrder: rows[i].Type == model.TypeConfigPush || rows[i].Type == model.TypeMergePublish,
		}
	}
	data["Orders"] = view
	data["FilterType"] = woType
	data["FilterStat"] = status
	h.render(c, tmpl, data)
}

// OrderExecutions 执行记录:已进入过执行阶段的工单,展示执行人/起止时间/耗时/结果。
func (h *Handler) OrderExecutions(c *gin.Context) {
	woType := c.Query("type")
	rows, err := h.orders.ListExecuted(woType)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	view := make([]execRow, len(rows))
	for i := range rows {
		view[i] = execRow{WO: rows[i], Duration: execDuration(&rows[i])}
	}
	h.render(c, "order_executions.html", gin.H{
		"Title":      "执行记录",
		"Executions": view,
		"FilterType": woType,
	})
}

// OrderAuditLog 操作日志:跨工单的动作审计流。
func (h *Handler) OrderAuditLog(c *gin.Context) {
	entries, err := h.orders.AuditLog(500)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	h.render(c, "order_auditlog.html", gin.H{
		"Title":   "操作日志",
		"Entries": entries,
	})
}

// execRow 执行记录页一行:工单 + 本次执行耗时的友好展示。
type execRow struct {
	WO       model.WorkOrder
	Duration string
}

// execDuration 计算执行耗时的友好串:已结束=起止时间差;执行中/开放中="进行中";否则="—"。
func execDuration(wo *model.WorkOrder) string {
	if wo.Status == workflow.StatusExecuting || wo.Status == workflow.StatusOpening {
		return "进行中"
	}
	if wo.ExecuteTime == nil || wo.ExecuteEndTime == nil {
		return "—"
	}
	d := wo.ExecuteEndTime.Sub(*wo.ExecuteTime)
	if d < 0 {
		return "—"
	}
	return formatDuration(d)
}

// formatDuration 把时长格式化成 "1h2m3s" 之类的短中文串(秒级精度)。
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	switch {
	case h > 0:
		return fmt.Sprintf("%d时%d分%d秒", h, m, s)
	case m > 0:
		return fmt.Sprintf("%d分%d秒", m, s)
	default:
		return fmt.Sprintf("%d秒", s)
	}
}

// listPackages 列出目录下的 .zip 包名(按名排序);目录不存在/读不到返回 nil。
func listPackages(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".zip") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// sshExecConfig 用 config 里的 SSH 配置拼一个轻量 RealConfig,供建单时现场读服包版本。
func (h *Handler) sshExecConfig() *executor.RealConfig {
	return &executor.RealConfig{
		SSHKey:  expandHomePath(h.cfg.Executor.SSHKey),
		SSHPort: h.cfg.Executor.SSHPort,
		SSHUser: h.cfg.Executor.SSHUser,
	}
}

// expandHomePath 展开路径开头的 ~(SSH 私钥常写 ~/.ssh/id_rsa)。
func expandHomePath(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}

// OrderMergeMatchTool 合服建单用:据被合服的实际包版本匹配最新合服工具包。
// 入参 form id=服号(合服列表第一个目标服);返回 JSON {ok,server,version,tool} 或 {error}。
// 出错统一回 200+{error},便于前端在提交确认弹窗里直接展示。
func (h *Handler) OrderMergeMatchTool(c *gin.Context) {
	if !h.members.HasRole(currentUser(c), workflow.RoleSubmit) {
		c.JSON(http.StatusForbidden, gin.H{"error": "无提交权限"})
		return
	}
	id, err := strconv.Atoi(strings.TrimSpace(c.PostForm("id")))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "服号非法"})
		return
	}
	if h.gsconfig == nil {
		c.JSON(http.StatusOK, gin.H{"error": "配置库不可用,无法自动匹配"})
		return
	}
	row, err := h.gsconfig.Store().GetServer(strconv.Itoa(id))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": fmt.Sprintf("服 %d 在配置库不存在", id)})
		return
	}
	ip := strings.TrimSpace(row["SelfPublicIp"])
	if ip == "" {
		c.JSON(http.StatusOK, gin.H{"error": fmt.Sprintf("服 %d 无内网IP", id)})
		return
	}
	ver, err := executor.ServerPacketVersion(h.sshExecConfig(), ip, id, func(string) {})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": fmt.Sprintf("读服 %d 包版本失败: %v", id, err)})
		return
	}
	tool := executor.MatchMergeTool(ver, listPackages(h.cfg.Executor.PackagesDir))
	if tool == "" {
		c.JSON(http.StatusOK, gin.H{"error": fmt.Sprintf("未找到版本 %s 对应的合服工具包(含 dbmerge)", ver)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "server": id, "version": ver, "tool": tool})
}

func (h *Handler) OrderNewPage(c *gin.Context) {
	h.render(c, "order_new.html", gin.H{
		"Title":       "新建工单",
		"Packages":    listPackages(h.cfg.Executor.PackagesDir),
		"ValidIDsCSV": h.existingServerIDsCSV(), // 供前端校验手输服号是否真实存在
	})
}

// existingServerIDset 返回配置库全部服 Id 集合;gsconfig 不可用或出错时返回 nil(调用方据此跳过存在性校验)。
func (h *Handler) existingServerIDset() map[int]bool {
	if h.gsconfig == nil {
		return nil
	}
	servers, err := h.gsconfig.ServerSource().Servers()
	if err != nil {
		return nil
	}
	set := make(map[int]bool, len(servers))
	for _, s := range servers {
		set[s.ID] = true
	}
	return set
}

// existingServerIDsCSV 把全部服 Id 拼成逗号分隔串(给前端做存在性校验);不可用时返回空串。
func (h *Handler) existingServerIDsCSV() string {
	set := h.existingServerIDset()
	if len(set) == 0 {
		return ""
	}
	ids := make([]int, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return joinIntsCSV(ids)
}

// missingIDs 返回 ids 中不在 set 里的(去重保序);set 为 nil 时返回 nil(跳过校验)。
func missingIDs(set map[int]bool, ids []int) []int {
	if set == nil {
		return nil
	}
	seen := map[int]bool{}
	var miss []int
	for _, id := range ids {
		if !set[id] && !seen[id] {
			seen[id] = true
			miss = append(miss, id)
		}
	}
	return miss
}

func joinIntsCSV(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

func (h *Handler) OrderCreate(c *gin.Context) {
	user := currentUser(c)
	if !h.members.HasRole(user, workflow.RoleSubmit) {
		c.String(http.StatusForbidden, "无提交权限")
		return
	}
	woType := c.PostForm("type")
	title := c.PostForm("title")

	idSet := h.existingServerIDset() // 手输/提交的服号须真实存在(nil 表示配置库不可用,跳过)

	var params, summary string
	switch woType {
	case model.TypeMerge:
		includeMerge := c.PostForm("include_merge") == "on"
		includePreMerge := c.PostForm("include_premerge") == "on"
		if !includeMerge && !includePreMerge {
			c.String(http.StatusBadRequest, "请至少勾选 合服 或 预合服")
			return
		}
		mp := model.MergeParams{IncludeMerge: includeMerge, IncludePreMerge: includePreMerge}
		if includeMerge {
			pairs, err := model.ParseMergePairs(c.PostForm("merge_pairs"))
			if err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			mp.Pairs = pairs
			mp.ToolPackage = c.PostForm("tool_package")
			var pairIDs []int
			for _, pr := range pairs {
				pairIDs = append(pairIDs, pr.Target, pr.Source)
			}
			if miss := missingIDs(idSet, pairIDs); len(miss) > 0 {
				c.String(http.StatusBadRequest, "合服列表中以下服号在配置库不存在: "+joinIntsCSV(miss))
				return
			}
		}
		if includePreMerge {
			ids, err := model.ParseServerIDsText(c.PostForm("premerge_ids"))
			if err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			if miss := missingIDs(idSet, ids); len(miss) > 0 {
				c.String(http.StatusBadRequest, "预合服服号中以下在配置库不存在: "+joinIntsCSV(miss))
				return
			}
			date, err := normalizeMergeDate(c.PostForm("premerge_date"))
			if err != nil {
				c.String(http.StatusBadRequest, err.Error())
				return
			}
			mp.PreMergeIDs = ids
			mp.PreMergeDate = date
		}
		params, _ = model.MarshalParams(mp)
		summary = mp.Summary()
	case model.TypeRelease:
		ids, err := model.ParseServerIDs(c.PostFormArray("server_ids"))
		if err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		if miss := missingIDs(idSet, ids); len(miss) > 0 {
			c.String(http.StatusBadRequest, "目标服中以下服号在配置库不存在: "+joinIntsCSV(miss))
			return
		}
		rp := model.ReleaseParams{
			ServerIDs:        ids,
			VersionPackage:   c.PostForm("version_package"),
			IncludeBattle:    c.PostForm("include_battle") == "on",
			IncludeHotUpdate: c.PostForm("include_hotupdate") == "on",
			HotScope:         c.PostForm("hot_scope"),
			HotPackage:       c.PostForm("hot_package"),
			HotFiles:         c.PostForm("hot_files"),
		}
		if err := rp.Validate(); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		params, _ = model.MarshalParams(rp)
		summary = rp.Summary()
	case model.TypeHotupdate:
		ids, err := model.ParseServerIDs(c.PostFormArray("server_ids"))
		if err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		if miss := missingIDs(idSet, ids); len(miss) > 0 {
			c.String(http.StatusBadRequest, "目标服中以下服号在配置库不存在: "+joinIntsCSV(miss))
			return
		}
		hp := model.HotupdateParams{
			ServerIDs:     ids,
			ConfigPackage: c.PostForm("config_package_hot"),
			HotFiles:      c.PostForm("hot_files_hot"),
			IncludeBattle: c.PostForm("include_battle_hot") == "on",
		}
		if err := hp.Validate(); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		params, _ = model.MarshalParams(hp)
		summary = hp.Summary()
	default:
		c.String(http.StatusBadRequest, "未知工单类型")
		return
	}

	wo, err := h.orders.Create(woType, title, summary, params, user)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	if st := parseScheduledTime(c.PostForm("scheduled_time")); st != nil {
		if err := h.orders.SetScheduledTime(wo.ID, st); err != nil {
			log.Printf("警告: 工单 %d 设置计划执行时间失败: %v", wo.ID, err)
		}
	}
	c.Redirect(http.StatusFound, "/orders/"+strconv.Itoa(int(wo.ID)))
}

func (h *Handler) OrderDetail(c *gin.Context) {
	id := parseID(c)
	wo, err := h.orders.Get(id)
	if err != nil {
		c.String(http.StatusNotFound, "工单不存在")
		return
	}
	user := currentUser(c)
	roles := h.members.Roles(user)
	h.render(c, "order_detail.html", gin.H{
		"Title":          "工单 " + wo.OrderNo,
		"O":              wo,
		"Stage":          workflow.StageOf(wo.Status),
		"Buttons":        availableButtons(wo, user, roles),
		"IsExecuting":    wo.Status == workflow.StatusExecuting || wo.Status == workflow.StatusOpening,
		"RetryFailedIDs": retryFailedIDs(wo),
		"Details":        paramRows(wo),
		"TargetSummary":  detailTargetSummary(wo),
		"ShowRepackage":  wo.Type == model.TypeNewServer && wo.Status == workflow.StatusExecFailed && roles.CanExecute,
		"RepackagePkgs":  listPackages(h.cfg.Executor.PackagesDir),
		"IsConfigOrder":  wo.Type == model.TypeConfigPush || wo.Type == model.TypeMergePublish,
	})
}

func (h *Handler) OrderAct(c *gin.Context) {
	id := parseID(c)
	action := c.PostForm("action")
	remark := c.PostForm("remark")
	user := currentUser(c)

	// 空 action(如备注框误触回车隐式提交)不是合法动作,直接回详情页,避免抛裸错误页
	if action == "" {
		c.Redirect(http.StatusFound, "/orders/"+strconv.Itoa(int(id)))
		return
	}

	// 权限校验:动作所需角色
	need := workflow.RequiredRole(action)
	if need != workflow.RoleSystem && !h.members.HasRole(user, need) {
		c.String(http.StatusForbidden, "无权执行该动作")
		return
	}
	if err := h.orders.Act(id, action, user, remark); err != nil {
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	// 列表页发起的作废带 redirect=/orders,作废后回列表;其余动作回详情页查看进度/日志。
	// 仅接受白名单值,避免开放重定向。
	dest := "/orders/" + strconv.Itoa(int(id))
	if c.PostForm("redirect") == "/orders" {
		dest = "/orders"
	}
	c.Redirect(http.StatusFound, dest)
}

// OrderRepackage 改新建服失败工单的版本包(需「执行」角色)。
func (h *Handler) OrderRepackage(c *gin.Context) {
	id := parseID(c)
	user := currentUser(c)
	if !h.members.HasRole(user, workflow.RoleExecute) {
		c.String(http.StatusForbidden, "无权换包(需「执行」角色)")
		return
	}
	pkg := c.PostForm("version_package")
	if err := h.orders.UpdateNewServerPackage(id, pkg, user); err != nil {
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/orders/"+strconv.Itoa(int(id)))
}

// OrderLogs htmx 轮询的日志片段。
func (h *Handler) OrderLogs(c *gin.Context) {
	id := parseID(c)
	wo, err := h.orders.Get(id)
	if err != nil {
		c.String(http.StatusNotFound, "")
		return
	}
	executing := wo.Status == workflow.StatusExecuting || wo.Status == workflow.StatusOpening

	// 轮询期间(poll=1)一旦执行结束,触发整页刷新,
	// 让最新状态与新阶段的操作按钮(如"测试通过")显示出来。
	if c.Query("poll") == "1" && !executing {
		c.Header("HX-Refresh", "true")
		c.String(http.StatusOK, "")
		return
	}

	after, _ := strconv.Atoi(c.Query("after"))
	var logs []model.WorkOrderLog
	if after > 0 {
		logs, _ = h.orders.LogsAfter(id, uint(after))
	} else {
		logs, _ = h.orders.Logs(id)
	}
	lastID := uint(after)
	if n := len(logs); n > 0 {
		lastID = logs[n-1].ID
	}
	c.Header("X-Log-Last-ID", strconv.FormatUint(uint64(lastID), 10))
	c.HTML(http.StatusOK, "logs_lines", gin.H{"Logs": logs})
}

func parseID(c *gin.Context) uint {
	n, _ := strconv.Atoi(c.Param("id"))
	return uint(n)
}

// button 表示详情页可点的一个操作按钮。
type button struct {
	Action string
	Label  string
	Class  string
}

// ParamRow 详情页一行友好参数。
type ParamRow struct {
	Label     string // 中文标签,如 "目标服"
	Value     string // 文本值(目标服行为 "<n> 个" 或 "<n> 个：id,id")
	Highlight string // "" / "yes" / "no" —— 控制 是/否 徽章配色
	Fold      string // 非空时作为可折叠内容(目标服超过阈值时的完整 ID 列表)
}

// targetFoldThreshold 目标服 ID 数量超过此值时折叠完整列表。
const targetFoldThreshold = 12

// yesNo 把布尔转成 是/否 行(带 yes/no 徽章高亮)。
func yesNo(label string, v bool) ParamRow {
	if v {
		return ParamRow{Label: label, Value: "是", Highlight: "yes"}
	}
	return ParamRow{Label: label, Value: "否", Highlight: "no"}
}

// targetRow 构造「目标服」行:少量内联展示,超过阈值则只显示数量、完整列表进 Fold。
func targetRow(ids []int) ParamRow {
	list := joinIntList(ids)
	r := ParamRow{Label: "目标服", Value: fmt.Sprintf("%d 个", len(ids))}
	if len(ids) > targetFoldThreshold {
		r.Fold = list
	} else if len(ids) > 0 {
		r.Value += "：" + list
	}
	return r
}

// targetRowLabeled 同 targetRow,但用自定义标签(合服工单的"预合服服号"等)。
func targetRowLabeled(label string, ids []int) ParamRow {
	r := targetRow(ids)
	r.Label = label
	return r
}

func joinIntList(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

// addRow 仅当 value 非空时追加一行普通文本。
func addRow(rows []ParamRow, label, value string) []ParamRow {
	if value == "" {
		return rows
	}
	return append(rows, ParamRow{Label: label, Value: value})
}

func mergePairsText(pairs []model.MergePair) string {
	if len(pairs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, fmt.Sprintf("%d <- %d", pair.Target, pair.Source))
	}
	return strings.Join(parts, "；")
}

func fallbackTargetSummaryFromJSON(raw string) string {
	var payload struct {
		RegionName  string `json:"region_name"`
		ServerIDs   []int  `json:"server_ids"`
		PreMergeIDs []int  `json:"premerge_ids"`
		Rows        []struct {
			ID   int    `json:"id"`
			Kind string `json:"kind"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	if len(payload.ServerIDs) > 0 {
		return fmt.Sprintf("%d 个目标服", len(payload.ServerIDs))
	}
	if len(payload.PreMergeIDs) > 0 {
		return fmt.Sprintf("预合服 %d 个", len(payload.PreMergeIDs))
	}
	var gameCount int
	var allCount int
	for _, row := range payload.Rows {
		if row.ID <= 0 {
			continue
		}
		allCount++
		if row.Kind == "game" {
			gameCount++
		}
	}
	if gameCount > 0 {
		if strings.TrimSpace(payload.RegionName) != "" {
			return fmt.Sprintf("%s · 新游戏服 %d 个", strings.TrimSpace(payload.RegionName), gameCount)
		}
		return fmt.Sprintf("新游戏服 %d 个", gameCount)
	}
	if allCount > 0 {
		return fmt.Sprintf("%d 个目标项", allCount)
	}
	return ""
}

func detailTargetSummary(wo *model.WorkOrder) string {
	switch wo.Type {
	case model.TypeRelease:
		p, err := model.UnmarshalReleaseParams(wo.Params)
		if err == nil && len(p.ServerIDs) > 0 {
			return fmt.Sprintf("%d 个目标服", len(p.ServerIDs))
		}
	case model.TypeHotupdate:
		p, err := model.UnmarshalHotupdateParams(wo.Params)
		if err == nil && len(p.ServerIDs) > 0 {
			return fmt.Sprintf("%d 个目标服", len(p.ServerIDs))
		}
	case model.TypeMerge:
		p, err := model.UnmarshalMergeParams(wo.Params)
		if err == nil {
			var parts []string
			if p.IncludeMerge && len(p.Pairs) > 0 {
				parts = append(parts, fmt.Sprintf("合服 %d 组", len(p.Pairs)))
			}
			if p.IncludePreMerge && len(p.PreMergeIDs) > 0 {
				parts = append(parts, fmt.Sprintf("预合服 %d 个", len(p.PreMergeIDs)))
			}
			if len(parts) > 0 {
				return strings.Join(parts, "；")
			}
		}
	case model.TypeDeleteServer:
		p, err := model.UnmarshalDeleteServerParams(wo.Params)
		if err == nil {
			return fmt.Sprintf("删除 %s(Id %d)", p.ServerName, p.ID)
		}
	}
	if s := fallbackTargetSummaryFromJSON(wo.Params); s != "" {
		return s
	}
	return strings.TrimSpace(wo.TargetSummary)
}

// paramRows 按工单类型把 Params(JSON) 解析成详情页的友好行列表;
// 解析失败或未知类型返回 nil(模板回退展示原始 JSON)。
func paramRows(wo *model.WorkOrder) []ParamRow {
	switch wo.Type {
	case model.TypeRelease:
		p, err := model.UnmarshalReleaseParams(wo.Params)
		if err != nil {
			return nil
		}
		rows := []ParamRow{
			targetRow(p.ServerIDs),
			{Label: "版本包", Value: p.VersionPackage},
			yesNo("连带战斗/副本服", p.IncludeBattle),
			yesNo("发版后热更", p.IncludeHotUpdate),
		}
		if p.IncludeHotUpdate {
			scope := map[string]string{"selected": "当前选中服", "all": "全服"}[p.HotScope]
			if scope == "" {
				scope = p.HotScope
			}
			rows = addRow(rows, "热更范围", scope)
			rows = addRow(rows, "热更配置包", p.HotPackage)
			rows = addRow(rows, "热更文件", p.HotFiles)
		}
		return rows
	case model.TypeHotupdate:
		p, err := model.UnmarshalHotupdateParams(wo.Params)
		if err != nil {
			return nil
		}
		return []ParamRow{
			targetRow(p.ServerIDs),
			{Label: "配置包", Value: p.ConfigPackage},
			{Label: "热更文件", Value: p.HotFiles},
			yesNo("连带战斗/副本服", p.IncludeBattle),
		}
	case model.TypeMerge:
		p, err := model.UnmarshalMergeParams(wo.Params)
		if err != nil {
			return nil
		}
		var rows []ParamRow
		rows = append(rows, yesNo("含合服", p.IncludeMerge))
		rows = append(rows, yesNo("含预合服", p.IncludePreMerge))
		if p.IncludeMerge {
			rows = append(rows, ParamRow{Label: "合服组数", Value: fmt.Sprintf("%d 组", len(p.Pairs))})
			rows = addRow(rows, "合服配对ID", mergePairsText(p.Pairs))
			rows = addRow(rows, "合服工具包", p.ToolPackage)
		}
		if p.IncludePreMerge {
			rows = append(rows, targetRowLabeled("预合服服号", p.PreMergeIDs))
			rows = addRow(rows, "预合服时间", formatMergeDate(p.PreMergeDate))
		}
		return rows
	case model.TypeDeleteServer:
		p, err := model.UnmarshalDeleteServerParams(wo.Params)
		if err != nil {
			return nil
		}
		rows := []ParamRow{
			{Label: "服号", Value: strconv.Itoa(p.ID)},
			{Label: "服务器名", Value: p.ServerName},
			{Label: "类型", Value: gsKindLabel(p.Kind)},
			{Label: "落点IP", Value: p.SelfPublicIp},
			{Label: "数据库", Value: p.DataBaseName + "@" + p.MySqlIp},
		}
		rows = append(rows, yesNo("删除ALB转发", p.RemoveALB))
		rows = append(rows, yesNo("重生serverlist", p.RegenServerlist))
		return rows
	case model.TypeNewServer:
		p, err := model.UnmarshalNewServerCreateParams(wo.Params)
		if err != nil {
			return nil
		}
		var ids []int
		var lines []string
		for _, r := range p.Rows {
			if r.Kind == "game" {
				ids = append(ids, r.ID)
			}
			lines = append(lines, fmt.Sprintf("%s %d", gsKindLabel(r.Kind), r.ID))
		}
		rows := []ParamRow{
			{Label: "区服名", Value: p.RegionName},
			{Label: "版本包", Value: p.VersionPackage},
		}
		rows = addRow(rows, "新建游戏服", joinIntList(ids))
		rows = addRow(rows, "全部服", strings.Join(lines, "、"))
		return rows
	default:
		return nil
	}
}

// gsKindLabel 把冻结参数里的 Kind 转中文(详情页展示用)。
func gsKindLabel(kind string) string {
	switch kind {
	case "game":
		return "游戏服"
	case "battle":
		return "战斗服"
	case "copy":
		return "副本服"
	case "center":
		return "中心服"
	default:
		return kind
	}
}

// retryFailedIDs 当工单为「发版/热更 + 执行失败 + 记录了失败服」时,
// 返回上次失败的服ID(用于详情页提示"重试将只重发这几台");否则返回 nil。
func retryFailedIDs(wo *model.WorkOrder) []int {
	if wo.Status != workflow.StatusExecFailed {
		return nil
	}
	switch wo.Type {
	case model.TypeRelease:
		p, err := model.UnmarshalReleaseParams(wo.Params)
		if err != nil {
			return nil
		}
		return p.LastFailedIDs
	case model.TypeHotupdate:
		p, err := model.UnmarshalHotupdateParams(wo.Params)
		if err != nil {
			return nil
		}
		return p.LastFailedIDs
	default:
		return nil
	}
}

// availableButtons 依据当前状态 + 用户角色,返回可见操作按钮。
func availableButtons(wo *model.WorkOrder, user string, roles model.WorkOrderMember) []button {
	var btns []button
	add := func(action, label, class string) {
		btns = append(btns, button{action, label, class})
	}
	switch wo.Status {
	case workflow.StatusPendingApprove:
		// 管理员可审批任何工单(含自己提交的);其他人不能审批自己的单
		if roles.CanApprove && (user != wo.CreateBy || roles.CanAdmin || user == model.AdminUsername) {
			add(workflow.ActionApprove, "审批通过", "btn-success")
			add(workflow.ActionReject, "驳回", "btn-warning")
		}
	case workflow.StatusRejected:
		if roles.CanSubmit && (user == wo.CreateBy || roles.CanAdmin || user == model.AdminUsername) {
			add(workflow.ActionResubmit, "重新提交", "btn-primary")
		}
	case workflow.StatusPendingExecute:
		if roles.CanExecute {
			add(workflow.ActionExecute, "开始执行", "btn-danger")
		}
	case workflow.StatusExecFailed:
		if roles.CanExecute {
			add(workflow.ActionExecute, "重试执行", "btn-danger")
			if wo.Type == model.TypeNewServer {
				add(workflow.ActionTeardown, "回滚清理", "btn-outline-danger")
			}
		}
	case workflow.StatusPendingTest:
		if roles.CanTest {
			add(workflow.ActionTestPass, "测试通过", "btn-success")
			add(workflow.ActionTestFail, "测试不通过(退回执行)", "btn-warning")
		}
	case workflow.StatusPendingOpen:
		if roles.CanExecute {
			add(workflow.ActionOpen, "开放", "btn-success")
		}
	case workflow.StatusOpenFailed:
		if roles.CanExecute {
			add(workflow.ActionOpen, "重试开放", "btn-success")
		}
	}
	// 非终态都可作废(具备提交角色者)
	if wo.Status != workflow.StatusOpened &&
		wo.Status != workflow.StatusCancelled &&
		roles.CanSubmit &&
		(user == wo.CreateBy || roles.CanAdmin || user == model.AdminUsername) {
		add(workflow.ActionCancel, "作废", "btn-outline-secondary")
	}
	return btns
}

// parseScheduledTime 解析 <input type=datetime-local> 的值(本地时间);空串=nil(立即)。
func parseScheduledTime(v string) *time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", v, time.Local)
	if err != nil {
		return nil
	}
	return &t
}

// normalizeMergeDate 把 <input type=date> 的 "2006-01-02"(或已是 "20060102")归一化为 "20060102"。
func normalizeMergeDate(v string) (string, error) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t.Format("20060102"), nil
	}
	if t, err := time.Parse("20060102", v); err == nil {
		return t.Format("20060102"), nil
	}
	return "", fmt.Errorf("预合服时间非法(应为日期): %q", v)
}

// formatMergeDate 把 "20060102" 显示成 "2006-01-02";解析失败原样返回。
func formatMergeDate(v string) string {
	if t, err := time.Parse("20060102", v); err == nil {
		return t.Format("2006-01-02")
	}
	return v
}
