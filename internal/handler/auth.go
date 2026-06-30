package handler

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"gongdan/internal/service/auth"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

func (h *Handler) LoginPage(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", h.loginViewData("", "", ""))
}

func (h *Handler) LoginSubmit(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")

	user, err := auth.Verify(h.db, username, password)
	if err != nil {
		c.HTML(http.StatusOK, "login.html", h.loginViewData(username, password, err.Error()))
		return
	}

	s := sessions.Default(c)
	s.Set("user", user.Username)
	s.Set("display", user.DisplayName)
	_ = s.Save()
	c.Redirect(http.StatusFound, "/orders")
}

func (h *Handler) loginViewData(username, password, errMsg string) gin.H {
	u, p := loginPrefill()
	if strings.TrimSpace(username) != "" {
		u = username
	}
	if strings.TrimSpace(password) != "" {
		p = password
	}
	return gin.H{
		"PasswordEnabled": h.cfg.Auth.PasswordEnabled,
		"WeworkEnabled":   h.cfg.Auth.WeworkEnabled,
		"Error":           errMsg,
		"PrefillUsername": u,
		"PrefillPassword": p,
	}
}

func loginPrefill() (username, password string) {
	if !demoAutofillEnabled() {
		return "", ""
	}
	username = strings.TrimSpace(os.Getenv("DEMO_LOGIN_USERNAME"))
	password = strings.TrimSpace(os.Getenv("DEMO_LOGIN_PASSWORD"))
	if username == "" {
		username = "admin"
	}
	if password == "" {
		password = "admin123"
	}
	return username, password
}

func demoAutofillEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("DEMO_AUTO_FILL_LOGIN"))
	if raw == "" {
		return false
	}
	ok, err := strconv.ParseBool(raw)
	return err == nil && ok
}

func (h *Handler) Logout(c *gin.Context) {
	s := sessions.Default(c)
	s.Clear()
	_ = s.Save()
	c.Redirect(http.StatusFound, "/login")
}
