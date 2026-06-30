package handler

import (
	"io"
	"net/http"
	"strconv"

	"gongdan/internal/mergepreview"
	"gongdan/internal/model"
	"gongdan/internal/service/member"

	"github.com/gin-gonic/gin"
)

const mpPageSize = 10

// mpReady:需「合服预告」菜单权限(admin override);且模块已配置。
func (h *Handler) mpReady(c *gin.Context) bool {
	if !h.members.CanMenu(currentUser(c), member.MenuMergePreview) {
		c.Redirect(http.StatusFound, "/orders")
		return false
	}
	if h.mergePrev == nil {
		c.String(http.StatusServiceUnavailable, "未配置合服预告(缺少 mergepreview.db_dsn / COS)")
		return false
	}
	return true
}

// mpRender 渲染管理页:列表 + 可选的提示。
func (h *Handler) mpRender(c *gin.Context, extra gin.H) {
	q := c.Query("q")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	rows, _ := h.mergePrev.List(q, mpPageSize, (page-1)*mpPageSize)
	total, _ := h.mergePrev.Count(q)
	data := gin.H{
		"Title": "合服预告", "Rows": rows, "Q": q, "Page": page, "PageSize": mpPageSize,
		"Total": total, "HasNext": int64(page*mpPageSize) < total,
	}
	if n := c.Query("imported"); n != "" {
		data["ImportMsg"] = "导入成功:" + n + " 行"
	}
	for k, v := range extra {
		data[k] = v
	}
	h.render(c, "mergepreview.html", data)
}

func (h *Handler) MergePreviewPage(c *gin.Context) {
	if !h.mpReady(c) {
		return
	}
	h.mpRender(c, nil)
}

// MergePreviewAddRows 追加多行:并列数组
// row_id[]/row_name[]/row_previewdur[]/row_compopen[]/row_compdur[]/row_compitem[]。
func (h *Handler) MergePreviewAddRows(c *gin.Context) {
	if !h.mpReady(c) {
		return
	}
	ids := c.PostFormArray("row_id")
	names := c.PostFormArray("row_name")
	previewDurs := c.PostFormArray("row_previewdur")
	compOpens := c.PostFormArray("row_compopen")
	compDurs := c.PostFormArray("row_compdur")
	compItems := c.PostFormArray("row_compitem")
	var rows []mergepreview.Row
	for i := range ids {
		if ids[i] == "" {
			continue // 跳过空行
		}
		id, err := strconv.Atoi(ids[i])
		if err != nil || id <= 0 {
			h.mpRender(c, gin.H{"AddErr": "服务器id必须是正整数:" + ids[i]})
			return
		}
		name := ""
		if i < len(names) {
			name = names[i]
		}
		if name == "" {
			h.mpRender(c, gin.H{"AddErr": "服 " + ids[i] + " 缺「说明」"})
			return
		}
		pd, err := strconv.Atoi(get(previewDurs, i))
		if err != nil {
			h.mpRender(c, gin.H{"AddErr": "服 " + ids[i] + " 的「预告持续几天」必须是整数"})
			return
		}
		co, err := strconv.Atoi(get(compOpens, i))
		if err != nil {
			h.mpRender(c, gin.H{"AddErr": "服 " + ids[i] + " 的「开服第几天显示补偿入口」必须是整数"})
			return
		}
		cd, err := strconv.Atoi(get(compDurs, i))
		if err != nil {
			h.mpRender(c, gin.H{"AddErr": "服 " + ids[i] + " 的「补偿入口持续几天」必须是整数"})
			return
		}
		ci, err := strconv.Atoi(get(compItems, i))
		if err != nil {
			h.mpRender(c, gin.H{"AddErr": "服 " + ids[i] + " 的「是否有合服补偿」必须是整数(0/1)"})
			return
		}
		rows = append(rows, mergepreview.NewRow(id, name, pd, co, cd, ci))
	}
	if len(rows) == 0 {
		h.mpRender(c, gin.H{"AddErr": "没有可追加的行"})
		return
	}
	conflicts, err := h.mergePrev.AppendRows(rows)
	if err != nil {
		h.mpRender(c, gin.H{"AddErr": "入库失败:" + err.Error()})
		return
	}
	// 排除冲突的 Id,得到本次实际入库的行,单独传给模板高亮显示。
	conflictSet := map[int]bool{}
	for _, id := range conflicts {
		conflictSet[id] = true
	}
	var added []mergepreview.Row
	for _, r := range rows {
		if !conflictSet[r.ID] {
			added = append(added, r)
		}
	}
	msg := "已追加 " + strconv.Itoa(len(added)) + " 行"
	if len(conflicts) > 0 {
		msg += ";以下 Id 已存在未追加:" + intsJoin(conflicts)
	}
	h.mpRender(c, gin.H{"AddMsg": msg, "JustAdded": added})
}

func (h *Handler) MergePreviewDeleteRow(c *gin.Context) {
	if !h.mpReady(c) {
		return
	}
	id, _ := strconv.Atoi(c.PostForm("id"))
	if id > 0 {
		_ = h.mergePrev.Delete(id)
	}
	c.Redirect(http.StatusFound, "/mergepreview")
}

// MergePreviewPublish 「提交并更新」:生成全量 → 与线上 diff → 建 mergepublish 工单。
func (h *Handler) MergePreviewPublish(c *gin.Context) {
	if !h.mpReady(c) {
		return
	}
	data, err := h.mergePrev.GenerateBytes()
	if err != nil {
		h.mpRender(c, gin.H{"GenErr": err.Error()})
		return
	}
	rows, _ := h.mergePrev.Store().AllRows()
	newTable := mergepreview.Tableize(rows)
	key := h.cfg.GSConfig.COS.MergePreviewObjectKey
	if key == "" {
		key = "server/MergeServerFunction.txt"
	}
	wo, created, err := h.buildAndCreateConfigOrder(
		model.TypeMergePublish, "合服预告发布", key,
		map[string][]byte{key: data}, newTable, mergepreview.FileToTable, currentUser(c))
	if err != nil {
		h.mpRender(c, gin.H{"GenErr": "建单失败:" + err.Error()})
		return
	}
	if !created {
		h.mpRender(c, gin.H{"SubmitMsg": "预告库与线上一致,无需下发"})
		return
	}
	c.Redirect(http.StatusFound, "/orders/"+strconv.FormatUint(uint64(wo.ID), 10))
}

func get(a []string, i int) string {
	if i < len(a) {
		return a[i]
	}
	return ""
}

func intsJoin(xs []int) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ","
		}
		out += strconv.Itoa(x)
	}
	return out
}

// MergePreviewImport 上传 MergeServerFunction.txt 覆盖导入合服预告库(清空重灌,等同 CLI -import-mergepreview)。仅 admin。
func (h *Handler) MergePreviewImport(c *gin.Context) {
	if h.mergePrev == nil {
		c.String(http.StatusServiceUnavailable, "未配置合服预告(缺少 mergepreview.db_dsn)")
		return
	}
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
	text, err := mergepreview.DecodeAuto(raw)
	if err != nil {
		c.String(http.StatusBadRequest, "文件解码失败: "+err.Error())
		return
	}
	if err := h.mergePrev.ImportOverwrite(text); err != nil {
		c.String(http.StatusInternalServerError, "导入失败: "+err.Error())
		return
	}
	n, _ := h.mergePrev.Count("")
	c.Redirect(http.StatusFound, "/mergepreview?imported="+strconv.FormatInt(n, 10))
}

// MergePreviewExport 下载合服预告(MergeServerFunction.txt)。仅 admin。
func (h *Handler) MergePreviewExport(c *gin.Context) {
	if h.mergePrev == nil {
		c.String(http.StatusServiceUnavailable, "未配置合服预告(缺少 mergepreview.db_dsn)")
		return
	}
	data, err := h.mergePrev.GenerateBytes()
	if err != nil {
		c.String(http.StatusInternalServerError, "生成失败: "+err.Error())
		return
	}
	// GenerateBytes 产出 GBK(与导入用的 MergeServerFunction.txt 同编码,可无损再导入)。
	c.Header("Content-Disposition", `attachment; filename="MergeServerFunction.txt"`)
	c.Data(http.StatusOK, "application/octet-stream", data)
}
