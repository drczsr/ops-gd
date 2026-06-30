package handler

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"gongdan/internal/configdiff"
	"gongdan/internal/gameserver"
	"gongdan/internal/gsconfig"
	"gongdan/internal/model"
	"gongdan/internal/service/member"

	"github.com/gin-gonic/gin"
)

// gsCol 列表页一列:Field 为配置库列名,Label 为表头显示名。
type gsCol struct {
	Field string
	Label string
}

// 列表页展示的基础列(顺序即展示顺序)。
// 注:WorldType 列在模板里经 worldType 函数转中文;SelfPublicIp=内网IP、RealSelfPublicIp=外网IP(以列说明为准)。
var gsBasicCols = []gsCol{
	{"Id", "ID"},
	{"Desc", "服务器名称"},
	{"WorldType", "服务器类型"},
	{"SelfPublicIp", "内网IP"},
	{"RealSelfPublicIp", "外网IP"},
	{"BattleWorldID", "所属战场服"},
}

// gsReady:需「游戏服配置」菜单权限(admin override);且模块已配置(service 非 nil)。
func (h *Handler) gsReady(c *gin.Context) bool {
	if !h.members.CanMenu(currentUser(c), member.MenuGSConfig) {
		c.Redirect(http.StatusFound, "/orders")
		return false
	}
	if h.gsconfig == nil {
		c.String(http.StatusServiceUnavailable, "未配置游戏服配置库(gsconfig.db_dsn)")
		return false
	}
	return true
}

func (h *Handler) GSList(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	h.renderGSList(c, c.Query("q"), "")
}

// renderGSList 拉取全部匹配服(不分页)按大区分组渲染列表页;errMsg 非空时附带错误提示。
func (h *Handler) renderGSList(c *gin.Context, q, errMsg string) {
	rows, err := h.gsconfig.Store().ListServers(q, h.settings.ShowAllServers(), 1000000, 0)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		a := strings.TrimSpace(rows[i]["Id"])
		b := strings.TrimSpace(rows[j]["Id"])
		ai, aerr := strconv.Atoi(a)
		bi, berr := strconv.Atoi(b)
		if aerr == nil && berr == nil {
			return ai < bi
		}
		if aerr == nil {
			return true
		}
		if berr == nil {
			return false
		}
		return a < b
	})
	for _, r := range rows {
		r["_Region"] = gsRegion(r)
	}
	groups := buildGSGroups(rows)
	msg := ""
	if n := c.Query("imported"); n != "" {
		msg = "\u5bfc\u5165\u6210\u529f:" + n + " \u4e2a\u670d"
		if cols := c.Query("cols"); cols != "" {
			msg += "(" + cols + " \u5217)"
		}
	}
	h.render(c, "gsconfig_list.html", gin.H{
		"Title": "\u6e38\u620f\u670d\u914d\u7f6e", "Cols": gsBasicCols, "Groups": groups,
		"Q": q, "Total": len(rows), "Rows": rows, "Err": errMsg, "Msg": msg,
	})
}

// gsGroup 列表页按大区归并的一组服。
type gsGroup struct {
	Region string
	Rows   []gsconfig.ServerRow
}

// buildGSGroups 按大区(WoRegion 优先,回退 Desc 前缀 GroupName)归并。
// rows 已按 Id 升序,故各大区首次出现的顺序即"组内最小 Id"顺序。
func buildGSGroups(rows []gsconfig.ServerRow) []gsGroup {
	idx := map[string]int{}
	var groups []gsGroup
	for _, r := range rows {
		region := gsRegion(r)
		i, ok := idx[region]
		if !ok {
			i = len(groups)
			idx[region] = i
			groups = append(groups, gsGroup{Region: region})
		}
		groups[i].Rows = append(groups[i].Rows, r)
	}
	return groups
}
func gsRegion(r gsconfig.ServerRow) string {
	region := strings.TrimSpace(r["WoRegion"])
	if region == "" {
		region = strings.TrimSpace(gameserver.GroupName(r["Desc"]))
	}
	if region == "" {
		region = "\u5176\u4ed6"
	}
	return region
}

func (h *Handler) GSDetail(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	id := c.Param("id")
	cols, err := h.gsconfig.Store().Columns()
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	row, err := h.gsconfig.Store().GetServer(id)
	if err != nil {
		c.String(http.StatusNotFound, "未找到该服")
		return
	}
	h.render(c, "gsconfig_detail.html", gin.H{"Title": "编辑服 " + id, "Cols": cols, "Row": row})
}

func (h *Handler) GSUpdate(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	id := c.Param("id")
	cols, _ := h.gsconfig.Store().Columns()
	fields := map[string]string{}
	for _, col := range cols {
		if col.Name == "Id" {
			continue
		}
		if v, ok := c.GetPostForm(col.Name); ok {
			fields[col.Name] = v
		}
	}
	if err := h.gsconfig.Store().UpdateServer(id, fields); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/gsconfig/"+id)
}

func (h *Handler) GSNewPage(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	cols, _ := h.gsconfig.Store().Columns()
	all, _ := h.gsconfig.Store().AllRows()
	tmpl := map[string]string{}
	if len(all) > 0 {
		tmpl = all[len(all)-1] // AllRows 已按 Id 升序,取最大 Id 作模板
	}
	h.render(c, "gsconfig_new.html", gin.H{"Title": "新增服", "Cols": cols, "Tmpl": tmpl})
}

func (h *Handler) GSCreate(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	store := h.gsconfig.Store()
	cols, _ := store.Columns()
	id := c.PostForm("Id")
	n, err := strconv.Atoi(id)
	meta, _ := store.Meta()
	if err != nil || n <= 0 || (meta.MaxID > 0 && n > meta.MaxID) {
		h.gsCreateError(c, cols, "Id 必须为正整数且不超过 MAX_ID")
		return
	}
	if _, gerr := store.GetServer(id); gerr == nil {
		h.gsCreateError(c, cols, "该 Id 已存在")
		return
	}
	fields := map[string]string{}
	for _, col := range cols {
		fields[col.Name] = c.PostForm(col.Name)
	}
	fields["Id"] = id
	if err := store.AddServer(id, fields); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/gsconfig/"+id)
}

func (h *Handler) gsCreateError(c *gin.Context, cols []gsconfig.Column, msg string) {
	row := map[string]string{}
	for _, col := range cols {
		row[col.Name] = c.PostForm(col.Name)
	}
	h.render(c, "gsconfig_new.html", gin.H{"Title": "新增服", "Cols": cols, "Tmpl": row, "Err": msg})
}

// gsKindFromWorldType 由 WorldType 推断冻结参数里的 Kind。
func gsKindFromWorldType(wt string) string {
	switch strings.TrimSpace(wt) {
	case "2":
		return "battle"
	case "3":
		return "copy"
	case "4":
		return "center"
	default: // 0 及其它当游戏服
		return "game"
	}
}

func gsNeedsALB(row map[string]string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(row["RealSelfPublicUrl"])), "wss://")
}

// gsDeleteBlockReason 返回删除该服会破坏的依赖原因(空=可删)。仅选中行范围:
// 删战斗服时仍有游戏服(WT0)挂靠、删中心服时仍有别的行引用,均拒绝。
func gsDeleteBlockReason(rows []map[string]string, id int, kind string) string {
	idStr := strconv.Itoa(id)
	switch kind {
	case "battle":
		for _, r := range rows {
			if strings.TrimSpace(r["WorldType"]) == "0" && strings.TrimSpace(r["BattleWorldID"]) == idStr {
				return fmt.Sprintf("仍有游戏服 %s 挂靠该战斗服,请先删除/改挂这些游戏服", r["Id"])
			}
		}
	case "center":
		for _, r := range rows {
			if strings.TrimSpace(r["Id"]) == idStr {
				continue // 排除中心服自身(它的 GlobalCenterWorldID 指向自己)
			}
			if strings.TrimSpace(r["GlobalCenterWorldID"]) == idStr {
				return fmt.Sprintf("仍有服 %s 引用该中心服,请先处理这些服", r["Id"])
			}
		}
	}
	return ""
}

// rowsToMaps 把 []gsconfig.ServerRow 转成 []map[string]string(依赖校验用)。
func rowsToMaps(rows []gsconfig.ServerRow) []map[string]string {
	out := make([]map[string]string, len(rows))
	for i, r := range rows {
		out[i] = r
	}
	return out
}

// GSDelete 删除单个游戏服:校验+依赖拦截→冻结参数→建系统工单(免审批立即执行)。
func (h *Handler) GSDelete(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	id := c.Param("id")
	n, err := strconv.Atoi(id)
	if err != nil || n <= 0 {
		h.gsListErr(c, "非法服号")
		return
	}
	store := h.gsconfig.Store()
	row, err := store.GetServer(id)
	if err != nil {
		h.gsListErr(c, "未找到该服")
		return
	}
	rows, err := store.AllRows()
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	kind := gsKindFromWorldType(row["WorldType"])
	if reason := gsDeleteBlockReason(rowsToMaps(rows), n, kind); reason != "" {
		h.gsListErr(c, "无法删除:"+reason)
		return
	}
	dp := model.DeleteServerParams{
		ID: n, Kind: kind, ServerName: row["Desc"], SelfPublicIp: row["SelfPublicIp"],
		DataBaseName: row["DataBaseName"], MySqlIp: row["MySqlIp"], MySqlPort: row["MySqlPort"],
		DataBaseUser: row["DataBaseUser"], DataBasePsw: row["DataBasePsw"],
		Fields: row, RemoveALB: gsNeedsALB(row), RegenServerlist: true,
	}
	params, _ := model.MarshalParams(dp)
	wo, err := h.orders.CreateAndExecute(model.TypeDeleteServer,
		"删除游戏服:"+dp.Summary(), dp.Summary(), params, currentUser(c))
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/orders/"+strconv.FormatUint(uint64(wo.ID), 10))
}

func (h *Handler) GSGenerate(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	if err := h.gsconfig.GenerateAndPublish(); err != nil {
		h.renderGSList(c, "", "生成/上传失败:"+err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/gsconfig?ok=1")
}

// ---- 批量编辑大表 ----

type gridColumn struct {
	Field    string `json:"field"`
	Title    string `json:"title"`
	Desc     string `json:"desc"` // 中文描述(来自文件第4行),可空
	Type     string `json:"type"`
	Editable bool   `json:"editable"`
}

type gridChange struct {
	ID    string `json:"id"`
	Field string `json:"field"`
	Value string `json:"value"`
}

type gridSaveReq struct {
	Changes []gridChange `json:"changes"`
}

// GSGridPage 渲染批量编辑大表页(数据走 /gsconfig/grid/data)。
func (h *Handler) GSGridPage(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	h.render(c, "gsconfig_grid.html", gin.H{"Title": "批量编辑游戏服配置"})
}

// GSGridData 返回全部列与全部服(JSON),供前端表格初始化。
func (h *Handler) GSGridData(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	store := h.gsconfig.Store()
	cols, err := store.Columns()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	rows, err := store.AllRows()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	descs, _ := store.ColumnDescriptions() // 可空,失败不阻断
	out := make([]gridColumn, len(cols))
	for i, col := range cols {
		out[i] = gridColumn{Field: col.Name, Title: col.Name, Desc: descs[col.Name],
			Type: col.GameType, Editable: col.Name != "Id"}
	}
	c.JSON(http.StatusOK, gin.H{"columns": out, "rows": rows})
}

// gsToTable 把 gsconfig 文件字节解析成 configdiff.Table(server 投影,按 Ordinal 排列列)。
func gsToTable(raw []byte) (configdiff.Table, error) {
	cols, _, rows, err := gsconfig.Parse(raw)
	if err != nil {
		return configdiff.Table{}, err
	}
	ordered := append([]gsconfig.Column(nil), cols...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Ordinal < ordered[j].Ordinal })
	names := make([]string, len(ordered))
	for i, c := range ordered {
		names[i] = c.Name
	}
	return configdiff.Table{Cols: names, Rows: rows, KeyCol: "Id"}, nil
}

// GSFullUpdate 全服更新:从配置库生成 server+client 快照 → 与线上 diff → 建 configpush 工单。
func (h *Handler) GSFullUpdate(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	store := h.gsconfig.Store()
	cols, err := store.Columns()
	if err != nil {
		h.gsListErr(c, "读列定义失败:"+err.Error())
		return
	}
	meta, err := store.Meta()
	if err != nil {
		h.gsListErr(c, "读元信息失败:"+err.Error())
		return
	}
	rows, err := store.AllRows()
	if err != nil {
		h.gsListErr(c, "读数据行失败:"+err.Error())
		return
	}
	serverData, err := gsconfig.GenerateFor(cols, meta, rows, "server")
	if err != nil {
		h.gsListErr(c, "生成 server 文件失败:"+err.Error())
		return
	}
	clientData, err := gsconfig.GenerateFor(cols, meta, rows, "client")
	if err != nil {
		h.gsListErr(c, "生成 client 文件失败:"+err.Error())
		return
	}
	serverKey := h.cfg.GSConfig.COS.ServerObjectKey
	files := map[string][]byte{
		serverKey:                          serverData,
		h.cfg.GSConfig.COS.ClientObjectKey: clientData,
	}
	newTable, err := gsToTable(serverData)
	if err != nil {
		h.gsListErr(c, "解析生成结果失败:"+err.Error())
		return
	}
	wo, created, err := h.buildAndCreateConfigOrder(
		model.TypeConfigPush, "全服更新", serverKey, files, newTable, gsToTable, currentUser(c))
	if err != nil {
		h.gsListErr(c, "建单失败:"+err.Error())
		return
	}
	if !created {
		h.gsListErr(c, "配置库与线上一致,无需下发")
		return
	}
	c.Redirect(http.StatusFound, "/orders/"+strconv.FormatUint(uint64(wo.ID), 10))
}

// gsListErr 用列表页渲染一条错误提示(复用分组装载)。
func (h *Handler) gsListErr(c *gin.Context, msg string) {
	h.renderGSList(c, "", msg)
}

// GSGridSave 校验并事务批量更新改动过的单元格。
func (h *Handler) GSGridSave(c *gin.Context) {
	if !h.gsReady(c) {
		return
	}
	var req gridSaveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "请求格式错误:" + err.Error()})
		return
	}
	if len(req.Changes) == 0 {
		c.JSON(http.StatusOK, gin.H{"ok": true, "updated": 0})
		return
	}
	store := h.gsconfig.Store()
	cols, err := store.Columns()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	colByName := make(map[string]gsconfig.Column, len(cols))
	for _, col := range cols {
		colByName[col.Name] = col
	}
	perID := map[string]map[string]string{}
	for _, ch := range req.Changes {
		if ch.Field == "Id" {
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": "Id 列不可修改"})
			return
		}
		col, ok := colByName[ch.Field]
		if !ok {
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": "未知列:" + ch.Field})
			return
		}
		if col.GameType == "INT" || col.GameType == "BOOL" {
			if _, err := strconv.Atoi(strings.TrimSpace(ch.Value)); err != nil {
				c.JSON(http.StatusOK, gin.H{"ok": false,
					"error": fmt.Sprintf("服 %s 的列 %s 值 %q 不是整数", ch.ID, ch.Field, ch.Value)})
				return
			}
		}
		if perID[ch.ID] == nil {
			perID[ch.ID] = map[string]string{}
		}
		perID[ch.ID][ch.Field] = ch.Value
	}
	if err := store.UpdateServers(perID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "updated": len(req.Changes)})
}

// GSImport 上传 ServerConfigList.txt 覆盖导入配置库(整库清空重灌,等同 CLI -import-gsconfig -force)。
// 仅 admin(路由门禁);自动识别 GBK/UTF-8(同 Parse)。
func (h *Handler) GSImport(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		c.String(http.StatusBadRequest, "请选择要导入的文件: "+err.Error())
		return
	}
	f, err := fh.Open()
	if err != nil {
		c.String(http.StatusBadRequest, "打开文件失败: "+err.Error())
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		c.String(http.StatusBadRequest, "读取文件失败: "+err.Error())
		return
	}
	cols, meta, rows, err := gsconfig.Parse(raw)
	if err != nil {
		c.String(http.StatusBadRequest, "解析失败: "+err.Error())
		return
	}
	store := h.gsconfig.Store()
	if err := store.DropManaged(); err != nil {
		c.String(http.StatusInternalServerError, "清空旧表失败: "+err.Error())
		return
	}
	if err := store.EnsureSchema(cols); err != nil {
		c.String(http.StatusInternalServerError, "建表失败: "+err.Error())
		return
	}
	if err := store.ImportAll(cols, meta, rows); err != nil {
		c.String(http.StatusInternalServerError, "导入失败: "+err.Error())
		return
	}
	if err := store.EnsureRegionColumn(); err != nil {
		c.String(http.StatusInternalServerError, "补建大区列失败: "+err.Error())
		return
	}
	c.Redirect(http.StatusFound, fmt.Sprintf("/gsconfig?imported=%d&cols=%d", len(rows), len(cols)))
}

// GSExport 下载当前配置库的 server 投影(ServerConfigList.txt,含库凭据;仅 admin)。
func (h *Handler) GSExport(c *gin.Context) {
	data, err := h.gsconfig.GenerateFullGBKBytes()
	if err != nil {
		c.String(http.StatusInternalServerError, "生成失败: "+err.Error())
		return
	}
	c.Header("Content-Disposition", `attachment; filename="ServerConfigList.txt"`)
	c.Data(http.StatusOK, "application/octet-stream", data)
}
