package handler

import (
	"log/slog"
	"net/http"
	"strings"

	ws "github.com/SinnerK9/my-iot-server/internal/websocket" //简写ws
	"github.com/SinnerK9/my-iot-server/pkg/jwtutil"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// 定义一个协议升级Upgrader，将http连接变为双向websocket
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	//校验请求来源,此处姑且直接放行
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// 返回一个Gin Handler，接收Websocket连接
func WsHandler(hub *ws.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		//联调需求：Websocket不支持自定义HTTP Header，需要从Query string读token，而非Authorization header
		token := c.Query("token")
		if token == "" {
			auth := c.GetHeader("Authorization")
			token = strings.TrimPrefix(auth, "Bearer ")
		}

		var userID uint64
		if token != "" {
			claims, err := jwtutil.ParseToken(token)
			if err != nil {
				return
			}
			userID = claims.UserID
		} else {
			userID = 0
		}
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			slog.Error("ws upgrade failed", "err", err)
			return
		}
		// userID 已从 query string 的 token 解析出来，不需要再读 Gin context
		//（Auth 中间件没走——WS 路由在 auth 组外面）

		//创建新client并将其注册到hub
		client := ws.NewClient(hub, conn, userID)
		hub.Register <- client
		//Register后启动读写协程
		//两个goroutine都是阻塞的，readpump在Readmessage上，writePump在select上
		go client.ReadPump()
		go client.WritePump()
		slog.Info("ws connected", "userID", userID)
	}
}
