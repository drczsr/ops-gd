package handler

import (
	"net/http"

	"gongdan/internal/gameserver"

	"github.com/gin-gonic/gin"
)

// GameServers 返回按区服名分组的游戏服列表 JSON(供新建表单的多选用)。
// 仅含可发版/热更的服:Id>10000 且 WorldType∈{0,2,3}。
func (h *Handler) GameServers(c *gin.Context) {
	servers, err := h.gsconfig.ServerSource().Servers()
	if err != nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	groups := gameserver.GroupServers(servers, h.settings.ShowAllServers())
	c.JSON(http.StatusOK, groups)
}
