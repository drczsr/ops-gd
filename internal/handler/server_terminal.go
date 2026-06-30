package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"

	"gongdan/internal/service/executor"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// wsUpgrader WebSocket 升级器;同源校验(防跨站 WebSocket 劫持)。
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // 非浏览器/无 Origin
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	},
}

// requireAdmin 远程连接(直连 root 终端)仅管理员可用。返回 true 表示已拦截。
func (h *Handler) blockNonAdmin(c *gin.Context) bool {
	if !h.isAdmin(c) {
		c.String(http.StatusForbidden, "仅管理员可使用远程连接")
		return true
	}
	return false
}

// ServerTerminalPage 渲染全屏 Web 终端页(新标签打开)。
func (h *Handler) ServerTerminalPage(c *gin.Context) {
	if h.blockNonAdmin(c) {
		return
	}
	id := int(parseID(c))
	s, ok, err := h.findServer(id)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		c.String(http.StatusNotFound, "服务器不存在")
		return
	}
	h.render(c, "server_terminal.html", gin.H{"Title": "远程连接 - " + s.Desc, "S": s})
}

// termClientMsg 前端发来的消息:t=i 输入(d=数据),t=r 调整窗口(cols/rows)。
type termClientMsg struct {
	T    string `json:"t"`
	D    string `json:"d"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// ServerTerminalWS WebSocket:把浏览器终端桥接到目标机的 SSH 交互 shell。
func (h *Handler) ServerTerminalWS(c *gin.Context) {
	if h.blockNonAdmin(c) {
		return
	}
	id := int(parseID(c))
	s, ok, err := h.findServer(id)
	if err != nil || !ok {
		c.String(http.StatusNotFound, "服务器不存在")
		return
	}
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	user := currentUser(c)
	log.Printf("[远程连接] 用户 %s 打开 服%d(%s) 终端", user, id, s.IP)
	defer log.Printf("[远程连接] 用户 %s 关闭 服%d 终端", user, id)

	// mock 模式不连真机,给出提示后关闭
	if h.settings.Mode() != "real" {
		_ = conn.WriteMessage(websocket.BinaryMessage,
			[]byte("当前为 mock 模式,未连接真实服务器。\r\n在「系统设置」切换为 real 模式后即可远程连接。\r\n"))
		return
	}

	sh, err := executor.OpenSSHShell(h.probeConfig(), s.IP, 24, 80)
	if err != nil {
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte("连接失败: "+err.Error()+"\r\n"))
		return
	}
	defer sh.Close()

	// 远端输出 -> 浏览器(唯一的 ws 写者)
	go func() {
		buf := make([]byte, 8192)
		for {
			n, rerr := sh.Stdout.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				_ = conn.Close() // 触发主循环 ReadMessage 返回
				return
			}
		}
	}()

	// 浏览器输入/resize -> 远端
	for {
		_, data, rerr := conn.ReadMessage()
		if rerr != nil {
			return
		}
		var msg termClientMsg
		if json.Unmarshal(data, &msg) == nil && (msg.T == "i" || msg.T == "r") {
			switch msg.T {
			case "i":
				_, _ = sh.Stdin.Write([]byte(msg.D))
			case "r":
				_ = sh.Resize(msg.Rows, msg.Cols)
			}
			continue
		}
		_, _ = sh.Stdin.Write(data) // 兜底:非控制消息当原始输入
	}
}
