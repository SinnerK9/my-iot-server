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

//补全Upgrade逻辑
// 返回一个Gin Handler，接收Websocket连接
func WsHandler(hub *ws.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		//联调需求：Websocket不支持自定义HTTP Header，需要从Query string读token，而非Authorization header
		token := c.Query("token")
		if token == "" {
			auth := c.GetHeader("Authorization")
			token = strings.TrimPrefix(auth, "Bearer ")
		}
		//没有token直接拒绝http升级
		if token == "" {
			c.JSON(http.StatusUnauthorized,gin.H{"code": 4010,"msg":"缺少认证 token"})
			return
		}
		//解析jwt，失败则拒绝
		claims ,err := jwtutil.ParseToken(token)
		if err != nil{
			c.JSON(http.StatusUnauthorized,gin.H{"code":4011,"msg":"Token 过期或无效"})
			return
		}
		//鉴权完全通过才从http升级到websocket
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			slog.Error("ws upgrade failed", "err", err)
			return
		}
		// userID 已从 query string 的 token 解析出来，不需要再读 Gin context
		//（Auth 中间件没走——WS 路由在 auth 组外面）

		//创建新client并将其注册到hub
		client := ws.NewClient(hub, conn, claims.UserID)
		hub.Register <- client
		//Register后启动读写协程
		//两个goroutine都是阻塞的，readpump在Readmessage上，writePump在select上
		go client.ReadPump()
		go client.WritePump()
		slog.Info("ws connected", "userID", claims.UserID)
	}
}
