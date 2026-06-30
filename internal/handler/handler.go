package handler

import (
	"crypto/rand"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"gongdan/internal/config"
	"gongdan/internal/gameserver"
	"gongdan/internal/gsconfig"
	"gongdan/internal/mergepreview"
	"gongdan/internal/model"
	"gongdan/internal/service/executor"
	"gongdan/internal/service/member"
	"gongdan/internal/service/order"
	"gongdan/internal/service/workflow"
	"gongdan/internal/settings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const sessionName = "gongdan_session"

// Handler 持有所有依赖。
type Handler struct {
	cfg       *config.Config
	db        *gorm.DB
	orders    *order.Service
	members   *member.Service
	settings  *settings.Store
	gsconfig  *gsconfig.Service
	mergePrev *mergepreview.Service
	cos       executor.COSClient

	// 被「强制停止(kill)」过的服:这些服需用修复/内存启动,普通启动会被拦截。
	// 进程内存态(单实例内网工具够用),启动成功后清除。
	killedMu sync.Mutex
	killed   map[int]bool
}

func New(cfg *config.Config, gdb *gorm.DB, orders *order.Service, members *member.Service,
	st *settings.Store, gs *gsconfig.Service, mp *mergepreview.Service, cosClient executor.COSClient) *Handler {
	return &Handler{cfg: cfg, db: gdb, orders: orders, members: members, settings: st,
		gsconfig: gs, mergePrev: mp, cos: cosClient, killed: make(map[int]bool)}
}

// TemplateFuncMap 返回全部模板函数,供 main.go 的 r.SetFuncMap 和测试共用。
func TemplateFuncMap() template.FuncMap {
	return template.FuncMap{
		"dict": func(values ...any) map[string]any {
			m := make(map[string]any)
			for i := 0; i+1 < len(values); i += 2 {
				m[values[i].(string)] = values[i+1]
			}
			return m
		},
		"list":        func(values ...any) []any { return values },
		"typeCN":      model.TypeLabel,
		"statusCN":    workflow.StatusLabel,
		"stageCN":     workflow.StageLabel,
		"typeBadge":   model.TypeBadge,
		"statusClass": workflow.StatusClass,
		"targetBrief": model.TargetBrief,
		"worldType": func(v any) string {
			switch x := v.(type) {
			case int:
				return gameserver.WorldTypeName(x)
			case string:
				n, _ := strconv.Atoi(strings.TrimSpace(x))
				return gameserver.WorldTypeName(n)
			default:
				return ""
			}
		},
		"statusStep": workflow.StatusStep,
		"mainStages": workflow.MainStages,
		"add1":       func(i int) int { return i + 1 },
		"dec": func(i int) int {
			if i > 1 {
				return i - 1
			}
			return 1
		},
		"outline": func(cls string) string { return strings.Replace(cls, "btn-", "btn-outline-", 1) },
	}
}

// Register 注册所有路由与中间件。
func (h *Handler) Register(r *gin.Engine) {
	r.SetFuncMap(TemplateFuncMap())
	secret, generated := sessionSecret(h.cfg.Server.SessionSecret)
	if generated {
		log.Printf("警告: 未配置 server.session_secret,已生成临时随机密钥;重启后所有登录态失效," +
			"生产环境请在 config.yaml 的 server.session_secret 配置一个固定的强随机串")
	}
	store := cookie.NewStore(secret)
	// 显式设为 http 可用:gorilla/sessions v1.4.0 默认 Secure+SameSite=None,
	// 在非 https(内网 http://IP:8080)下浏览器会丢弃 cookie 导致登录死循环。
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7天
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	r.Use(sessions.Sessions(sessionName, store))

	r.GET("/login", h.LoginPage)
	r.POST("/login", h.LoginSubmit)
	r.GET("/logout", h.Logout)

	authed := r.Group("/", h.requireLogin)
	{
		authed.GET("/", func(c *gin.Context) { c.Redirect(http.StatusFound, "/orders") })
		authed.GET("/orders", h.OrderList)
		authed.GET("/orders/mine", h.OrderMine)
		authed.GET("/orders/pending", h.OrderPending)
		authed.GET("/orders/executions", h.OrderExecutions)
		authed.GET("/orders/auditlog", h.OrderAuditLog)
		authed.GET("/orders/new", h.OrderNewPage)
		authed.POST("/orders", h.OrderCreate)
		authed.POST("/orders/match-tool", h.OrderMergeMatchTool) // 合服建单:据被合服实际版本匹配工具包
		authed.GET("/orders/:id", h.OrderDetail)
		authed.POST("/orders/:id/act", h.OrderAct)
		authed.POST("/orders/:id/repackage", h.OrderRepackage) // 新建服失败工单换包
		authed.GET("/orders/:id/logs", h.OrderLogs)            // htmx 轮询片段
		authed.GET("/orders/:id/config", h.OrderConfigData)    // 配置类工单审批表格数据
		authed.POST("/orders/:id/rollback", h.OrderRollback)   // 配置类工单回滚到历史版本
		// 用户管理:仅管理员(原对所有登录用户开放,属安全洞)。
		authed.GET("/members", h.requireAdmin, h.MemberList)
		authed.POST("/members", h.requireAdmin, h.MemberSave)
		// 服务器管理「菜单」门禁 CanServers 只管页面入口(admin override);
		// 启停/批量/终端等动作仍各自校验 CanExecute / isAdmin(更严,不被本门禁放宽)。
		srvMW := h.requireMenu(member.MenuServers)
		authed.GET("/servers", srvMW, h.ServerList)                       // 服务器管理页
		authed.GET("/servers/:id", srvMW, h.ServerDetail)                 // 单服详情页
		authed.GET("/servers/status", h.ServerStatus)                     // 全服状态探测 JSON
		authed.POST("/servers/batch", h.ServerBatch)                      // 批量启停(自带执行角色校验)
		authed.POST("/servers/batch/loginlimit", h.ServerBatchLoginLimit) // 批量改登录限制(自带执行角色校验)
		authed.POST("/servers/:id/start", h.ServerStart)                  // 启动单服(自带执行角色校验)
		authed.POST("/servers/:id/stop", h.ServerStop)                    // 停服(自带执行角色校验)
		authed.GET("/servers/:id/status", h.ServerStatusOne)              // 单服状态探测(启停后轮询用)
		authed.GET("/servers/:id/terminal", h.ServerTerminalPage)         // Web 终端页(自带 isAdmin 校验)
		authed.GET("/servers/:id/terminal/ws", h.ServerTerminalWS)        // 终端 WebSocket 桥接
		authed.GET("/gameservers", h.GameServers)                         // JSON
		authed.GET("/settings", h.SettingsPage)
		authed.POST("/settings", h.SettingsSave)
		authed.GET("/gsconfig", h.GSList)
		authed.GET("/gsconfig/new", h.GSNewPage)
		authed.POST("/gsconfig/new", h.GSCreate)
		authed.POST("/gsconfig/_generate", h.GSGenerate)
		authed.POST("/gsconfig/fullupdate", h.GSFullUpdate)
		authed.GET("/gsconfig/newgame", h.GSNewGamePage)
		authed.POST("/gsconfig/newgame/preview", h.GSNewGamePreview)
		authed.POST("/gsconfig/newgame/commit", h.GSNewGameCommit)
		authed.GET("/gsconfig/create", h.GSCreatePage)
		authed.POST("/gsconfig/create/preview", h.GSCreatePreview)
		authed.POST("/gsconfig/create/commit", h.GSCreateCommit)
		authed.GET("/gsconfig/grid", h.GSGridPage)
		authed.GET("/gsconfig/grid/data", h.GSGridData)
		authed.POST("/gsconfig/grid/save", h.GSGridSave)
		authed.POST("/gsconfig/import", h.requireAdmin, h.GSImport) // 上传覆盖导入服列表(admin)
		authed.GET("/gsconfig/export", h.requireAdmin, h.GSExport)  // 下载服列表(含库凭据,admin)
		authed.GET("/gsconfig/:id", h.GSDetail)
		authed.POST("/gsconfig/:id", h.GSUpdate)
		authed.POST("/gsconfig/:id/delete", h.GSDelete)
		authed.GET("/mergepreview", h.MergePreviewPage)
		authed.POST("/mergepreview/rows", h.MergePreviewAddRows)
		authed.POST("/mergepreview/rows/delete", h.MergePreviewDeleteRow)
		authed.POST("/mergepreview/publish", h.MergePreviewPublish)
		authed.POST("/mergepreview/import", h.requireAdmin, h.MergePreviewImport) // 上传覆盖导入预告(admin)
		authed.GET("/mergepreview/export", h.requireAdmin, h.MergePreviewExport)  // 下载预告(admin)
	}
}

// sessionSecret 返回 cookie 签名密钥:优先用配置值;为空则随机生成 32 字节
// (generated=true,调用方据此提示),避免再用写死的弱密钥。
func sessionSecret(configured string) (secret []byte, generated bool) {
	if s := strings.TrimSpace(configured); s != "" {
		return []byte(s), false
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("生成 session 密钥失败: " + err.Error())
	}
	return b, true
}

// requireLogin 未登录重定向到登录页。
func (h *Handler) requireLogin(c *gin.Context) {
	user := currentUser(c)
	if user == "" {
		c.Redirect(http.StatusFound, "/login")
		c.Abort()
		return
	}
	c.Next()
}

// requireAdmin 中间件:非管理员重定向到 /orders。
func (h *Handler) requireAdmin(c *gin.Context) {
	if !h.members.IsAdmin(currentUser(c)) {
		c.Redirect(http.StatusFound, "/orders")
		c.Abort()
		return
	}
	c.Next()
}

// requireMenu 中间件工厂:无该菜单权限(且非管理员)重定向到 /orders。
func (h *Handler) requireMenu(menu string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !h.members.CanMenu(currentUser(c), menu) {
			c.Redirect(http.StatusFound, "/orders")
			c.Abort()
			return
		}
		c.Next()
	}
}

// currentUser 从 session 取当前登录用户名。
func currentUser(c *gin.Context) string {
	s := sessions.Default(c)
	v := s.Get("user")
	if v == nil {
		return ""
	}
	return v.(string)
}

// render 渲染整页模板,统一注入当前用户与是否管理员(供 layout 侧边栏判断)。
// 片段模板(如 logs_inner)和登录页不要走这里。
func (h *Handler) render(c *gin.Context, name string, data gin.H) {
	if data == nil {
		data = gin.H{}
	}
	user := currentUser(c)
	if _, ok := data["User"]; !ok {
		data["User"] = user
	}
	data["IsAdmin"] = h.members.IsAdmin(user)
	// 侧边栏菜单可见性(admin 已在 CanMenu 内 override)。
	data["CanServers"] = h.members.CanMenu(user, member.MenuServers)
	data["CanGSConfig"] = h.members.CanMenu(user, member.MenuGSConfig)
	data["CanMergePreview"] = h.members.CanMenu(user, member.MenuMergePreview)
	// 侧边栏「待审批」徽章:全局待审批工单数(0 时模板不显示)。
	if h.orders != nil {
		data["PendingCount"] = h.orders.PendingApproveCount()
	}
	c.HTML(http.StatusOK, name, data)
}
