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

	//新增，用于给所有在线客户端广播消息
	Broadcast chan []byte
}

// NewHub 创建并返回 Hub 实例。
// Register/Unregister 用无缓冲 channel——
// Hub.Run() 必须同步处理注册/注销，不能堆积（堆积意味着 map 状态不一致）。
func NewHub() *Hub {
	return &Hub{
		Clients:    make(map[uint64]*Client),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Broadcast:  make(chan []byte),
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
		//情况1：新客户端上线
		case client := <-h.Register:
			h.Mu.Lock() //保护并发读写，Clients在多个goroutine里写，必须加锁
			h.Clients[client.UserID] = client
			h.Mu.Unlock()
			slog.Info("client registered", "userID", client.UserID)

		//情况2：客户端断开，从注销通道里取出一个client
		case client := <-h.Unregister:
			h.Mu.Lock()
			//检查发现client在map里：注意，只判断userID不够，因为可能出现同id的新旧连接问题
			if existing, ok := h.Clients[client.UserID]; ok && existing == client {
				delete(h.Clients, client.UserID)
				close(client.Send)
				//close必须在delete之后
				//单纯close后chan无法再写入，但是尚未delete之前广播可能尝试访问client
				//一旦在此时访问client会尝试往已经关闭的chan里写东西，会导致panic
			}
			h.Mu.Unlock()
			slog.Info("client unregistered", "userID", client.UserID)

		case msg := <-h.Broadcast:
			h.Mu.RLock()
			for _, client := range h.Clients {
				//select + default进行非阻塞发送
				//一旦某个client的Send Chan满了不会导致堵塞，而是跳过之
				select {
				case client.Send <- msg:
					//发送成功情况
				default:
					//Send chan满了，跳过
				}
			}
			h.Mu.RUnlock()
		}
	}
}

func (h *Hub) SendToUser(userID uint64, msg []byte) {
	h.Mu.RLock()
	client, ok := h.Clients[userID]
	h.Mu.RUnlock()
	if ok {
		select {
		case client.Send <- msg:
		default:
		}
	}
}
