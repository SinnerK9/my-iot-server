package handler

import (
	"log/slog"
	"net/http"

	ws "github.com/SinnerK9/my-iot-server/internal/websocket" //简写ws
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
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			slog.Error("ws upgrade failed", "err", err)
			return
		}
		//从context取Auth中间件注入的UserID
		userID, ok := getUserID(c)
		if !ok {
			conn.Close()
			return
		}
		//创建新client并将其注册到hub
		client := ws.NewClient(hub, conn, userID)
		hub.Register <- client
		slog.Info("ws connected", "userID", userID)
	}
}
