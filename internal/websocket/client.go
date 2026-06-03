package websocket

import (
	"time"

	"github.com/gorilla/websocket"
)

const(
	writeWait = 10* time.Second  // 写超时，向客户端发消息最多等 10 秒
	pongWait = 60 * time.Second  // 心跳超时——60 秒没收到pong就断开
	pingPeriod = (pongWait * 9) / 10  // 每54秒发一次 ping
	maxMessageSize = 512  //消息最大512字节
)

//client代表一个websocket连接。一个client ＝ 一个在线用户 ： 同一个用户多个标签页是多个websocket
type Client struct{
	Hub *Hub //所属的hub
	Conn *websocket.Conn //websocket连接对象
	Send chan[]byte //缓冲channel，负责将消息传给writepump
	UserID uint64 //用于鉴别这个连接属于的用户
}