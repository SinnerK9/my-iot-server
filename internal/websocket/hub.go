package websocket

import (
	"log/slog"
	"sync"

	"github.com/SinnerK9/my-iot-server/internal/repository"
)

// Hub 管理所有活跃的 WebSocket 连接。
// 一个进程只有一个 Hub 实例 main.go 里创建，传给所有 handler。
type Hub struct {
	// Clients：userID → 该用户的所有连接（一个 set）。
	// 同一个 userID 可以有多个 Client（多标签页、多设备）。
	// map[*Client]bool 是 Go 里用 map 模拟 set 的惯用写法——bool 值忽略，只用来 O(1) 删。
	Clients    map[uint64]map[*Client]bool
	Mu         sync.RWMutex
	Register   chan *Client
	Unregister chan *Client
	Broadcast  chan []byte
	OnMessage  func(userID uint64, payload []byte) //外部注入的消息处理函数
}

// NewHub 创建并返回 Hub 实例。
func NewHub() *Hub {
	return &Hub{
		Clients:    make(map[uint64]map[*Client]bool),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Broadcast:  make(chan []byte),
	}
}

func (h *Hub) Start() {
	slog.Info("Hub starting")
	go h.run()
}

// Run 是 Hub 的主循环——一个 goroutine，select 三个 channel。
func (h *Hub) run() {
	for {
		select {
		case client := <-h.Register:
			h.Mu.Lock()
			// 如果这个 userID 还没有 set，先创建一个
			if h.Clients[client.UserID] == nil {
				h.Clients[client.UserID] = make(map[*Client]bool)
			}
			//增加判断：加入之前set是不是空的？是则是该用户第一个连接,在redis中设置该用户在线
			isFirst := len(h.Clients[client.UserID]) == 0
			h.Clients[client.UserID][client] = true
			h.Mu.Unlock()
			if isFirst {
				h.markUserOnline(client.UserID)
			}
			slog.Info("client registered", "userID", client.UserID)

		case client := <-h.Unregister:
			h.Mu.Lock()
			set, ok := h.Clients[client.UserID]
			if !ok || !set[client] {
				h.Mu.Unlock()
				continue //重复unregister直接跳过
			}
			delete(set, client)
			close(client.Send)
			//删除后set空了，该用户最后一个链接
			isLast := len(set) == 0
			if isLast {
				delete(h.Clients, client.UserID)
			}
			h.Mu.Unlock()
			if isLast {
				h.markUserOffline(client.UserID)
			}
			slog.Info("client unregistered", "userID", client.UserID)

		case msg := <-h.Broadcast:
			h.Mu.RLock()
			for _, set := range h.Clients {
				for client := range set {
					select {
					case client.Send <- msg:
					default:
					}
				}
			}
			h.Mu.RUnlock()
		}
	}
}

// SendToUser 给指定用户的所有连接发消息（定向推送）。
func (h *Hub) SendToUser(userID uint64, msg []byte) {
	h.Mu.RLock()
	set, ok := h.Clients[userID]
	h.Mu.RUnlock()
	if ok {
		for client := range set {
			select {
			case client.Send <- msg:
			default:
			}
		}
	}
}

// markUserOnline与redis联动，标记用户上线
func (h *Hub) markUserOnline(userID uint64) {
	if err := repository.SetUserOnline(userID); err != nil {
		slog.Error("redis mark online failed", "err", err, "userID", userID)
	}
}

// markUserOffline在redis标记用户下线
func (h *Hub) markUserOffline(userID uint64) {
	if err := repository.SetUserOffline(userID); err != nil {
		slog.Error("redis mark offline failed", "err", err, "userID", userID)
	}
}

// RefreshUserTTL 刷新 Redis 里该用户的 TTL（心跳时调用）。
func (h *Hub) RefreshUserTTL(userID uint64) {
	if err := repository.RefreshUserTTL(userID); err != nil {
		slog.Error("redis refresh TTL failed", "err", err, "userID", userID)
	}
}
