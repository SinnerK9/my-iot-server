package websocket

import (
	"log/slog"
	"sync"
)

// Hub 管理所有活跃的 WebSocket 连接。
// 一个进程只有一个 Hub 实例 main.go 里创建，传给所有 handler。
type Hub struct {
	// Clients 是连接注册表：userID → Client。同一个 userID 可以有多个 Client（多标签页、多设备）。
	// 用 map 而不是 slice：断开连接时 O(1) 删除。
	Clients map[uint64]*Client

	// Mu 保护 Clients map 的并发读写。
	// 用 RWMutex 而不是 Mutex：
	// 广播（读 map → 找到所有 Client）不需要互斥——多个广播可以同时"读"。
	// 只有 Register/Unregister（写 map）时才需要排他锁。
	Mu sync.RWMutex

	// Register 是"上线"通知 channel。
	// Client 创建后把自己发到这个 channel → Hub.Run() 收到后写入 Clients map。
	Register chan *Client

	// Unregister 是"下线"通知 channel。
	// Client 断开后把自己发到这个 channel → Hub.Run() 收到后从 Clients map 删除。
	Unregister chan *Client
}

// NewHub 创建并返回 Hub 实例。
// Register/Unregister 用无缓冲 channel——
// Hub.Run() 必须同步处理注册/注销，不能堆积（堆积意味着 map 状态不一致）。
func NewHub() *Hub {
	return &Hub{
		Clients:    make(map[uint64]*Client),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
	}
}

// Start 启动 Hub 的主事件循环
func (h *Hub) Start() {
	slog.Info("Hub starting")
	go h.run()
}

// run 是 Hub 的主循环——一个 goroutine，select 三个 channel。
func (h *Hub) run() {
	for {
		select {
		case client := <-h.Register:

		case client := <-h.Unregister:

		}
	}
}
