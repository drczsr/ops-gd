package handler

import (
	"net/http"
	"strconv"

	"gongdan/internal/gameserver"
	"gongdan/internal/model"

	"github.com/gin-gonic/gin"
)

// newGameView 组装表单页所需的下拉数据(大区/中心服/空闲机器/MySqlIp/版本包)。
func (h *Handler) newGameView(rows []map[string]string, extra gin.H) gin.H {
	v := gin.H{
		"Title":    "批量创建游戏服",
		"Regions":  gameserver.ExistingRegions(rows),
		"Centers":  gameserver.CenterServers(rows),
		"BattleMs": gameserver.FreeBattleMachinesFor(rows),
		"MySqlIps": collectMySqlIps(rows),
		"Packages": listPackages(h.cfg.Executor.PackagesDir),
		"Form":     map[string]string{}, // 默认空表单;extra 带 Form 时覆盖。模板用 index .Form,nil 会渲染报错截断页面
	}
	for k, val := range extra {
		v[k] = val
	}
	return v
}

// GSNewGamePage 渲染「批量创建游戏服」表单。
func (h *Handler) GSNewGamePage(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	rows, err := h.gsconfig.Store().AllRows()
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	h.render(c, "gameserver_new.html", h.newGameView(rows, nil))
}

// parseNewGameForm 从表单解析参数(大区二选一 + 可选中心服)。
func parseNewGameForm(c *gin.Context) gameserver.NewServerParams {
	region := c.PostForm("region_exist")
	if c.PostForm("mode") == "new" {
		region = c.PostForm("region_new")
	}
	count, _ := strconv.Atoi(c.PostForm("count"))
	p := gameserver.NewServerParams{
		RegionName:    region,
		LetterPrefix:  c.PostForm("letter_prefix"),
		Count:         count,
		GameMySqlIp:   c.PostForm("game_mysqlip"),
		BattleMySqlIp: c.PostForm("battle_mysqlip"),
		AddPublicUrl:  c.PostForm("add_public_url") == "1",
	}
	// 中心服:仅在勾选「配置中心服」时处理。
	if c.PostForm("center_enabled") == "1" {
		if c.PostForm("center_mode") == "new" {
			if m := splitN(c.PostForm("center_machine"), 2); m != nil {
				p.NewCenterMachine = gameserver.MachineFrom(m[0], m[1])
			}
			p.CenterMySqlIp = c.PostForm("center_mysqlip")
		} else {
			id, _ := strconv.Atoi(c.PostForm("center_exist"))
			p.ExistingCenterID = id
		}
	}
	return p
}

// flattenNewGameForm 收集批量表单字段(供预览页隐藏域回填到 commit)。
func flattenNewGameForm(c *gin.Context) map[string]string {
	keys := []string{"mode", "region_exist", "region_new", "letter_prefix", "count",
		"game_mysqlip", "battle_mysqlip", "add_public_url", "version_package",
		"center_enabled", "center_mode", "center_exist", "center_machine", "center_mysqlip"}
	out := map[string]string{}
	for _, k := range keys {
		out[k] = c.PostForm(k)
	}
	return out
}

// GSNewGamePreview 计算分配并渲染预览(不落库)。失败回表单页显示错误。
func (h *Handler) GSNewGamePreview(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	rows, err := h.gsconfig.Store().AllRows()
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	p := parseNewGameForm(c)
	plan, err := gameserver.PlanNewServers(rows, p)
	if err != nil {
		h.render(c, "gameserver_new.html", h.newGameView(rows, gin.H{"Err": err.Error()}))
		return
	}
	h.render(c, "gameserver_new.html", h.newGameView(rows, gin.H{
		"Preview": plan.Rows, "Form": flattenNewGameForm(c), "Pkg": c.PostForm("version_package"),
	}))
}

// GSNewGameCommit 用表单参数重算 plan → 冻结为工单参数 → 建单(免审批,立即执行)。
// 与单服「创建游戏服」同一执行器:写库 → 发 COS → 逐服并发部署 → 建 ALB。
func (h *Handler) GSNewGameCommit(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	rows, err := h.gsconfig.Store().AllRows()
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	p := parseNewGameForm(c)
	plan, err := gameserver.PlanNewServers(rows, p)
	if err != nil {
		h.render(c, "gameserver_new.html", h.newGameView(rows, gin.H{"Err": err.Error()}))
		return
	}
	cp := model.NewServerCreateParams{
		RegionName:     p.RegionName,
		VersionPackage: c.PostForm("version_package"),
		Rows:           toNewServerRows(plan.Rows),
	}
	params, _ := model.MarshalParams(cp)
	wo, err := h.orders.CreateAndExecute(model.TypeNewServer, "批量创建游戏服:"+cp.Summary(), cp.Summary(), params, currentUser(c))
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/orders/"+strconv.FormatUint(uint64(wo.ID), 10))
}
