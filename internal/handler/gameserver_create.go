package handler

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"gongdan/internal/gameserver"
	"gongdan/internal/model"

	"github.com/gin-gonic/gin"
)

// GSCreatePage 渲染「创建游戏服」向导第一步(空表单)。
func (h *Handler) GSCreatePage(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	rows, err := h.gsconfig.Store().AllRows()
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	h.renderCreateForm(c, rows, "")
}

// renderCreateForm 渲染向导第一步表单(可带错误信息 + 回填)。
func (h *Handler) renderCreateForm(c *gin.Context, rows []map[string]string, errMsg string) {
	form := flattenCreateForm(c)
	if strings.TrimSpace(form["manual_game_id"]) == "" {
		if sid := suggestedGameID(rows); sid > 0 {
			form["manual_game_id"] = strconv.Itoa(sid)
		}
	}
	h.render(c, "gameserver_create.html", gin.H{
		"Title":    "创建游戏服",
		"Slots":    gameserver.AvailableGameSlots(rows),
		"Battles":  gameserver.BattleServersWithCapacity(rows),
		"Centers":  gameserver.CenterServers(rows),
		"BattleMs": gameserver.FreeBattleMachinesFor(rows),
		"MySqlIps": collectMySqlIps(rows),
		"Packages": listPackages(h.cfg.Executor.PackagesDir),
		"Err":      errMsg,
		"Form":     form,
	})
}

// parseCreateForm 把表单解析成 gameserver.CreateGameParams。
// 已有战斗服/中心服 id 必须确实存在于 rows 对应类型(表单可伪造,做存在性校验)。
func parseCreateForm(c *gin.Context, rows []map[string]string) (gameserver.CreateGameParams, error) {
	p := gameserver.CreateGameParams{
		ServerName:    c.PostForm("server_name"),
		WorldName:     c.PostForm("world_name"),
		GameMySqlIp:   c.PostForm("game_mysqlip"),
		BattleMySqlIp: c.PostForm("battle_mysqlip"),
		AddPublicUrl:  c.PostForm("add_public_url") == "1",
	}
	manualGameIDRaw := strings.TrimSpace(c.PostForm("manual_game_id"))
	if manualGameIDRaw != "" {
		mid, err := strconv.Atoi(manualGameIDRaw)
		if err != nil || mid <= 0 {
			return p, fmt.Errorf("服务器ID 必须是正整数")
		}
		if idOccupied(rows, mid) {
			return p, fmt.Errorf("服务器ID %d 已被占用", mid)
		}
		p.ManualGameID = mid
	}
	// 落点:手填IP(slot_mode=manual)→ 取内网IP+外网IP(外网留空则同内网),PortBase=0 让 planner 自动取空闲档;
	// 否则用下拉选定的落点(含具体端口档)。
	if c.PostForm("slot_mode") == "manual" {
		selfIp := strings.TrimSpace(c.PostForm("game_self_ip"))
		realIp := strings.TrimSpace(c.PostForm("game_real_ip"))
		if realIp == "" {
			realIp = selfIp
		}
		p.Slot = gameserver.GameSlot{SelfPublicIp: selfIp, RealSelfPublicIp: realIp, PortBase: 0}
	} else if parts := splitN(c.PostForm("slot"), 3); parts != nil {
		base, _ := strconv.Atoi(parts[2])
		p.Slot = gameserver.GameSlot{SelfPublicIp: parts[0], RealSelfPublicIp: parts[1], PortBase: base}
	}
	// 战斗服:仅在勾选「配置战斗服」时挂/建;不勾选则本服不挂战斗服(BattleWorldID=-1)。
	if c.PostForm("battle_enabled") == "1" {
		p.BattleEnabled = true
		if c.PostForm("battle_mode") == "new" {
			if m := splitN(c.PostForm("battle_machine"), 2); m != nil {
				p.NewBattleMachine = gameserver.MachineFrom(m[0], m[1])
			}
		} else {
			id, _ := strconv.Atoi(c.PostForm("battle_exist"))
			if id > 0 && !battleHasCapacity(rows, id) {
				return p, fmt.Errorf("所选战斗服 %d 不存在或已无容量", id)
			}
			p.ExistingBattleID = id
		}
	}
	// 中心服:仅在勾选「配置中心服」时处理;不勾选则本服不挂中心服。
	if c.PostForm("center_enabled") == "1" {
		if c.PostForm("center_mode") == "new" {
			if m := splitN(c.PostForm("center_machine"), 2); m != nil {
				p.NewCenterMachine = gameserver.MachineFrom(m[0], m[1])
			}
			p.CenterMySqlIp = c.PostForm("center_mysqlip")
		} else {
			id, _ := strconv.Atoi(c.PostForm("center_exist"))
			if id > 0 && !centerExists(rows, id) {
				return p, fmt.Errorf("所选中心服 %d 不存在", id)
			}
			p.ExistingCenterID = id
		}
	}
	return p, nil
}

// battleHasCapacity 校验 id 是 rows 里 WT2 且仍有容量的战斗服之一。
func battleHasCapacity(rows []map[string]string, id int) bool {
	for _, b := range gameserver.BattleServersWithCapacity(rows) {
		if b.ID == id {
			return true
		}
	}
	return false
}

// centerExists 校验 id 是 rows 里 WT4 中心服之一。
func centerExists(rows []map[string]string, id int) bool {
	for _, ce := range gameserver.CenterServers(rows) {
		if ce.ID == id {
			return true
		}
	}
	return false
}

// GSCreatePreview 计算创建方案并渲染预览(不落库)。出错回表单页带错误。
func (h *Handler) GSCreatePreview(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	rows, err := h.gsconfig.Store().AllRows()
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	p, perr := parseCreateForm(c, rows)
	if perr != nil {
		h.renderCreateForm(c, rows, perr.Error())
		return
	}
	plan, err := gameserver.PlanCreateGameServer(rows, p)
	if err != nil {
		h.renderCreateForm(c, rows, err.Error())
		return
	}
	h.render(c, "gameserver_create.html", gin.H{
		"Title":   "创建游戏服",
		"Preview": plan.Rows,
		"Form":    flattenCreateForm(c),
		"Pkg":     c.PostForm("version_package"),
	})
}

// GSCreateCommit 用表单参数重算方案 → 冻结为工单参数 → 建单(免审批,立即执行)。
func (h *Handler) GSCreateCommit(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	rows, err := h.gsconfig.Store().AllRows()
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	p, perr := parseCreateForm(c, rows)
	if perr != nil {
		h.renderCreateForm(c, rows, perr.Error())
		return
	}
	plan, err := gameserver.PlanCreateGameServer(rows, p)
	if err != nil {
		h.renderCreateForm(c, rows, err.Error())
		return
	}
	cp := model.NewServerCreateParams{
		RegionName:     p.ServerName, // 单服无大区,用服务器名做工单摘要标签
		VersionPackage: c.PostForm("version_package"),
		Rows:           toNewServerRows(plan.Rows),
	}
	params, _ := model.MarshalParams(cp)
	wo, err := h.orders.CreateAndExecute(model.TypeNewServer, "创建游戏服:"+cp.Summary(), cp.Summary(), params, currentUser(c))
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/orders/"+strconv.FormatUint(uint64(wo.ID), 10))
}

// toNewServerRows 把方案行转成冻结的工单行(从 Fields 取部署/建库所需字段)。
func toNewServerRows(rows []gameserver.PlannedRow) []model.NewServerRow {
	out := make([]model.NewServerRow, len(rows))
	for i, r := range rows {
		f := r.Fields
		out[i] = model.NewServerRow{
			Kind: r.Kind, ID: r.ID,
			DataBaseUser: f["DataBaseUser"], DataBasePsw: f["DataBasePsw"],
			MySqlIp: f["MySqlIp"], MySqlPort: f["MySqlPort"],
			SelfPublicIp: f["SelfPublicIp"], Fields: f,
		}
	}
	return out
}

// collectMySqlIps 收集配置库中出现过的 MySqlIp(去重升序),供 MySqlIp 下拉。
func collectMySqlIps(rows []map[string]string) []string {
	set := map[string]bool{}
	for _, r := range rows {
		if v := r["MySqlIp"]; v != "" {
			set[v] = true
		}
	}
	var out []string
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// splitN 按 | 切并校验段数;不满足(或首段为空)返回 nil。
func splitN(s string, n int) []string {
	parts := strings.Split(s, "|")
	if len(parts) != n || parts[0] == "" {
		return nil
	}
	return parts
}

// flattenCreateForm 收集向导所有表单字段成 map(用于预览页隐藏域回填)。
func flattenCreateForm(c *gin.Context) map[string]string {
	keys := []string{"server_name", "world_name", "manual_game_id",
		"slot_mode", "slot", "game_self_ip", "game_real_ip", "game_mysqlip",
		"battle_enabled", "battle_mode", "battle_exist", "battle_machine",
		"battle_mysqlip", "center_enabled", "center_mode", "center_exist", "center_machine", "center_mysqlip",
		"add_public_url", "version_package"}
	out := map[string]string{}
	for _, k := range keys {
		out[k] = c.PostForm(k)
	}
	return out
}

func suggestedGameID(rows []map[string]string) int {
	maxID := -1
	used := map[int]bool{}
	for _, r := range rows {
		id, _ := strconv.Atoi(strings.TrimSpace(r["Id"]))
		if id <= 0 {
			continue
		}
		used[id] = true
		wt, _ := strconv.Atoi(strings.TrimSpace(r["WorldType"]))
		if wt == 0 && id > maxID {
			maxID = id
		}
	}
	if maxID < 0 {
		return 0
	}
	next := maxID + 1
	for used[next] {
		next++
	}
	return next
}

func idOccupied(rows []map[string]string, id int) bool {
	if id <= 0 {
		return false
	}
	for _, r := range rows {
		rid, _ := strconv.Atoi(strings.TrimSpace(r["Id"]))
		if rid == id {
			return true
		}
	}
	return false
}
