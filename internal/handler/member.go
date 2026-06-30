package handler

import (
	"net/http"

	"gongdan/internal/model"
	"gongdan/internal/service/auth"

	"github.com/gin-gonic/gin"
)

func (h *Handler) MemberList(c *gin.Context) {
	rows, _ := h.members.All()

	// 标记哪些 user_id 已有登录账号(User 表)。
	var usernames []string
	h.db.Model(&model.User{}).Pluck("username", &usernames)
	hasAccount := make(map[string]bool, len(usernames))
	for _, u := range usernames {
		hasAccount[u] = true
	}

	// username -> WeworkUserID,供编辑表单回填。
	var users []model.User
	h.db.Select("username", "wework_user_id").Find(&users)
	weworkOf := make(map[string]string, len(users))
	for _, u := range users {
		weworkOf[u.Username] = u.WeworkUserID
	}

	h.render(c, "members.html", gin.H{
		"Title":      "成员管理",
		"Members":    rows,
		"HasAccount": hasAccount,
		"WeworkOf":   weworkOf,
	})
}

func (h *Handler) MemberSave(c *gin.Context) {
	m := &model.WorkOrderMember{
		UserID:          c.PostForm("user_id"),
		DisplayName:     c.PostForm("display_name"),
		CanSubmit:       c.PostForm("can_submit") == "on",
		CanApprove:      c.PostForm("can_approve") == "on",
		CanExecute:      c.PostForm("can_execute") == "on",
		CanTest:         c.PostForm("can_test") == "on",
		CanAdmin:        c.PostForm("can_admin") == "on",
		CanServers:      c.PostForm("can_servers") == "on",
		CanGSConfig:     c.PostForm("can_gsconfig") == "on",
		CanMergePreview: c.PostForm("can_mergepreview") == "on",
	}
	if m.UserID == "" {
		c.String(http.StatusBadRequest, "user_id 必填")
		return
	}

	// 同步登录账号:username = user_id。新账号必须设密码;留空表示保留原密码。
	password := c.PostForm("password")
	if err := auth.UpsertAccount(h.db, m.UserID, password, m.DisplayName); err != nil {
		c.String(http.StatusBadRequest, err.Error())
		return
	}

	// 写入企业微信 userid(群机器人 @人用);留空表示清除。
	weworkUID := c.PostForm("wework_userid")
	if err := h.db.Model(&model.User{}).Where("username = ?", m.UserID).
		Update("wework_user_id", weworkUID).Error; err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	if err := h.members.Save(m); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/members")
}
