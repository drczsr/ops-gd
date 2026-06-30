package handler

import (
	"errors"
	"net/http"
	"strconv"

	"gongdan/internal/configdiff"
	"gongdan/internal/gsconfig/cos"
	"gongdan/internal/mergepreview"
	"gongdan/internal/model"

	"github.com/gin-gonic/gin"
)

// parseToTable 把一份配置文件字节解析成 configdiff.Table。
// 两类工单各传各的解析器(gsconfig / mergepreview)。
type parseToTable func(raw []byte) (configdiff.Table, error)

// buildAndCreateConfigOrder 配置类工单的统一建单:
// 生成的 newTable 已由调用方算好;这里拉线上基准 → diff → 空变更拦截 → CreateWithArtifact。
// 返回 (工单, 是否建单, 错误);created=false 且 err=nil 表示"无变更未建单"。
func (h *Handler) buildAndCreateConfigOrder(
	woType, title, primaryKey string, files map[string][]byte,
	newTable configdiff.Table, parseBaseline parseToTable, createBy string,
) (*model.WorkOrder, bool, error) {
	baseRaw, err := h.cos.Download(primaryKey)
	var baseTable configdiff.Table
	switch {
	case errors.Is(err, cos.ErrNotFound):
		baseTable = configdiff.Table{KeyCol: newTable.KeyCol} // 线上无文件=空基准
	case err != nil:
		return nil, false, err
	default:
		if baseTable, err = parseBaseline(baseRaw); err != nil {
			return nil, false, err
		}
	}
	if configdiff.Diff(baseTable, newTable).Empty() {
		return nil, false, nil
	}
	baseVer, verr := h.cos.HeadVersionID(primaryKey)
	if errors.Is(verr, cos.ErrNotFound) {
		baseVer = ""
	} else if verr != nil {
		return nil, false, verr
	}
	wo, cerr := h.orders.CreateWithArtifact(woType, title, "全服", primaryKey, files, baseVer, createBy)
	if cerr != nil {
		return nil, false, cerr
	}
	return wo, true, nil
}

// OrderConfigData 返回 configpush/mergepublish 工单的审批表格数据:
// 快照解析成 grid + 实时与线上 diff + 基准是否过期。
func (h *Handler) OrderConfigData(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	wo, err := h.orders.Get(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "工单不存在"})
		return
	}
	if wo.Type != model.TypeConfigPush && wo.Type != model.TypeMergePublish {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该工单无配置表"})
		return
	}
	art, err := h.orders.GetArtifact(wo.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	parse := h.tableParser(wo.Type)
	newTable, err := parse(art.SnapshotFiles[art.PrimaryKey])
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "解析快照: " + err.Error()})
		return
	}
	var baseTable configdiff.Table
	baseRaw, derr := h.cos.Download(art.PrimaryKey)
	switch {
	case errors.Is(derr, cos.ErrNotFound):
		baseTable = configdiff.Table{KeyCol: newTable.KeyCol}
	case derr != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "下载线上基准: " + derr.Error()})
		return
	default:
		if baseTable, err = parse(baseRaw); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "解析线上基准: " + err.Error()})
			return
		}
	}
	d := configdiff.Diff(baseTable, newTable)
	curVer, verr := h.cos.HeadVersionID(art.PrimaryKey)
	if errors.Is(verr, cos.ErrNotFound) {
		curVer = ""
	} else if verr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读线上版本: " + verr.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"columns":       newTable.Cols,
		"rows":          newTable.Rows,
		"changed":       d.Changed,
		"added":         d.AddedKeys,
		"removed":       d.RemovedKeys,
		"keyCol":        newTable.KeyCol,
		"baselineStale": curVer != art.BaselineVersionID,
	})
}

// tableParser 按工单类型返回对应的"字节→Table"解析器。
func (h *Handler) tableParser(woType string) parseToTable {
	if woType == model.TypeMergePublish {
		return mergepreview.FileToTable
	}
	return gsToTable
}

// OrderRollback 用某条已完成配置工单的部署版本,建一张同类型新工单(回滚),走正常审批/执行。
func (h *Handler) OrderRollback(c *gin.Context) {
	if !h.isAdmin(c) {
		c.String(http.StatusForbidden, "回滚需要管理员权限")
		return
	}
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	wo, err := h.orders.Get(uint(id))
	if err != nil || (wo.Type != model.TypeConfigPush && wo.Type != model.TypeMergePublish) {
		c.String(http.StatusBadRequest, "该工单不可回滚")
		return
	}
	art, err := h.orders.GetArtifact(wo.ID)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	var row model.WorkOrderArtifact
	if err := h.db.Where("order_id = ?", wo.ID).First(&row).Error; err != nil || row.DeployedVersionID == "" {
		c.String(http.StatusBadRequest, "该工单无已部署版本,无法回滚")
		return
	}
	data, err := h.cos.Download(art.PrimaryKey, row.DeployedVersionID)
	if err != nil {
		c.String(http.StatusInternalServerError, "下载历史版本: "+err.Error())
		return
	}
	parse := h.tableParser(wo.Type)
	newTable, err := parse(data)
	if err != nil {
		c.String(http.StatusInternalServerError, "解析历史版本: "+err.Error())
		return
	}
	title := model.TypeLabel(wo.Type) + "(回滚自 " + wo.OrderNo + ")"
	newWO, created, err := h.buildAndCreateConfigOrder(
		wo.Type, title, art.PrimaryKey,
		map[string][]byte{art.PrimaryKey: data}, newTable, parse, currentUser(c))
	if err != nil {
		c.String(http.StatusInternalServerError, "建回滚工单: "+err.Error())
		return
	}
	if !created {
		c.String(http.StatusOK, "目标版本与当前线上一致,无需回滚")
		return
	}
	c.Redirect(http.StatusFound, "/orders/"+strconv.FormatUint(uint64(newWO.ID), 10))
}
