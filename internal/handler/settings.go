package handler

import (
	"net/http"

	"gongdan/internal/settings"

	"github.com/gin-gonic/gin"
)

// isAdmin 是否管理员:内置 admin 账号或被授予管理员角色的成员。
func (h *Handler) isAdmin(c *gin.Context) bool {
	return h.members.IsAdmin(currentUser(c))
}

func (h *Handler) SettingsPage(c *gin.Context) {
	if !h.isAdmin(c) {
		c.Redirect(http.StatusFound, "/orders")
		return
	}
	h.render(c, "settings.html", gin.H{"Title": "系统设置", "S": h.settings.Get()})
}

func (h *Handler) SettingsSave(c *gin.Context) {
	if !h.isAdmin(c) {
		c.Redirect(http.StatusFound, "/orders")
		return
	}
	n := settings.Settings{
		Mode:           c.PostForm("mode"),
		AutoExecute:    c.PostForm("auto_execute") == "on",
		AutoOpen:       c.PostForm("auto_open") == "on",
		ShowAllServers: c.PostForm("show_all_servers") == "on",
	}
	if n.Mode != "real" { // 白名单,避免乱值;非 real 一律 mock
		n.Mode = "mock"
	}
	if err := h.settings.Update(n); err != nil {
		h.render(c, "settings.html", gin.H{"Title": "系统设置", "S": n, "Err": err.Error()})
		return
	}
	h.render(c, "settings.html", gin.H{"Title": "系统设置", "S": h.settings.Get(), "Saved": true})
}
