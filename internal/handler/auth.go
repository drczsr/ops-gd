package handler

import (
	"net/http"

	"gongdan/internal/service/auth"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

func (h *Handler) LoginPage(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", gin.H{
		"PasswordEnabled": h.cfg.Auth.PasswordEnabled,
		"WeworkEnabled":   h.cfg.Auth.WeworkEnabled,
	})
}

func (h *Handler) LoginSubmit(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")

	user, err := auth.Verify(h.db, username, password)
	if err != nil {
		c.HTML(http.StatusOK, "login.html", gin.H{
			"PasswordEnabled": h.cfg.Auth.PasswordEnabled,
			"WeworkEnabled":   h.cfg.Auth.WeworkEnabled,
			"Error":           err.Error(),
		})
		return
	}

	s := sessions.Default(c)
	s.Set("user", user.Username)
	s.Set("display", user.DisplayName)
	_ = s.Save()
	c.Redirect(http.StatusFound, "/orders")
}

func (h *Handler) Logout(c *gin.Context) {
	s := sessions.Default(c)
	s.Clear()
	_ = s.Save()
	c.Redirect(http.StatusFound, "/login")
}
