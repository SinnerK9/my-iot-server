package websocket

import (
	"log/slog"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second    // 写超时，向客户端发消息最多等 10 秒
	pongWait       = 60 * time.Second    // 心跳超时——60 秒没收到pong就断开
	pingPeriod     = (pongWait * 9) / 10 // 每54秒发一次 ping
	maxMessageSize = 512                 //消息最大512字节
)

// client代表一个websocket连接。一个client ＝ 一个在线用户 ： 同一个用户多个标签页是多个websocket
type Client struct {
	Hub    *Hub            //所属的hub
	Conn   *websocket.Conn //websocket连接对象
	Send   chan []byte     //缓冲channel，负责将消息传给writepump
	UserID uint64          //用于鉴别这个连接属于的用户
}

// NewClient 创建 Client 实例。
// 写构造函数而非让调用方手拼 &Client{...}，防止漏掉 make channel，
// 导致向 nil channel 发送消息从而永久阻塞。
func NewClient(hub *Hub, conn *websocket.Conn, userID uint64) *Client {
	return &Client{
		Hub:    hub,
		Conn:   conn,
		Send:   make(chan []byte, 256),
		UserID: userID,
	}
}

func (c *Client) ReadPump() {
	//利用defer，RAII
	defer func() {
		c.Hub.Unregister <- c //通知Hub用户下线
		c.Conn.Close()        //关闭底层TCP连接
	}()
	c.Conn.SetReadLimit(maxMessageSize)

	//SetReadDeadline：如果 60 秒内没有任何数据到达（包括 pong），ReadMessage 返回 i/o timeout → for 循环 break → defer 执行清理
	//防止占住连接不放
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	//SetPongHandler：收到客户端 pong 响应时调用，刷新Deadline
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		//
		_, msg, err := c.Conn.ReadMessage()
		if err != nil {
			//判断是不是意料之中的错误：用户离开页面/异常关闭
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
			) {
				slog.Error("ws read error", "err", err, "userID", c.UserID)
			}
			break //不论是不是设置的错误，都退出循环并执行defer里的收尾逻辑
		}
		c.Hub.Broadcast <- msg //收到消息后广播给所有在线客户端
	}
}

// 专门的写goroutine，所有发给客户端的信息必须通过c.Send排队，然后透过它发送给客户端
// 解决了WriteMessage不是线程安全的问题
func (c *Client) WritePump() {
	//创建心跳计时器，每一个period（54s）往ticker.C里发送一个Ping帧
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		//情况一：有消息要发送
		case msg, ok := <-c.Send:
			if !ok {
				//说明该Send channel已被close
				//Go的特征，range阻塞等待channel，发送方用关闭Send通知接收方数据已发完
				//range读到关闭的通道后自动退出，ok=false
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			//在每次写操作之前设置一个10秒超时，一旦十秒内写不进去（网络极差）则放弃此次写入
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return //写超时，则连接已死，退出goroutine
			}
		//情况二：定时器到点了，Ping一下保活
		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return //Ping不出去了，连接已死，退出
			}
		}
	}
}
