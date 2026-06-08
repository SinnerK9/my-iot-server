package websocket

import (
	"log/slog"
	"sync"
)

// Hub 管理所有活跃的 WebSocket 连接。
// 一个进程只有一个 Hub 实例 main.go 里创建，传给所有 handler。
type Hub struct {
	// Clients：userID → 该用户的所有连接（一个 set）。
	// 同一个 userID 可以有多个 Client（多标签页、多设备）。
	// map[*Client]bool 是 Go 里用 map 模拟 set 的惯用写法——bool 值忽略，只用来 O(1) 删。
	Clients map[uint64]map[*Client]bool

	Mu sync.RWMutex

	Register   chan *Client
	Unregister  chan *Client
	Broadcast  chan []byte
}

// NewHub 创建并返回 Hub 实例。
func NewHub() *Hub {
	return &Hub{
		Clients:    make(map[uint64]map[*Client]bool),
		Register:   make(chan *Client),
		Unregister:  make(chan *Client),
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
			h.Clients[client.UserID][client] = true
			h.Mu.Unlock()
			slog.Info("client registered", "userID", client.UserID)

		case client := <-h.Unregister:
			h.Mu.Lock()
			if set, ok := h.Clients[client.UserID]; ok {
				if set[client] {
					delete(set, client)
					// 删完如果这个 userID 下没连接了，删掉整个 key
					if len(set) == 0 {
						delete(h.Clients, client.UserID)
					}
					// close 必须在 delete(map) 之后——广播不会再找到这个 client
					close(client.Send)
				}
			}
			h.Mu.Unlock()
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
