# SHVA — 智能家居语音交互助手（Go 服务器）

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green)](./LICENSE)

基于 Go 的智能家居后端服务——**WebSocket Hub 并发模型 + Redis 状态管理 + LLM 流式链路 + MQTT 设备通信**。

用户在浏览器/App 中输入自然语言指令（"打开客厅的灯"），服务器通过 LLM 解析意图、MQTT 下发控制指令给家具端、Redis 管理设备在线状态、WebSocket 实时推送结果——全链路闭环。

---

## 架构

```
┌─ 客户端 ─────────────────────┐
│  Android App / 浏览器 mock    │
└──────┬───────────────────────┘
       │ HTTPS (REST) + WSS (实时推送)
       ▼
┌─ Go Server :7777 ──────────────────────────────────────┐
│                                                        │
│  Gin Router ──→ Handler ──→ Service ──→ Repository     │
│       │                                        │        │
│       ├─ /v1/auth/*  注册/登录/JWT              ├─ MySQL │
│       ├─ /v1/devices  设备 CRUD + 事务           │        │
│       ├─ /v1/chat     LLM 非流式/SSE             ├─ Redis │
│       └─ /v1/ws       WebSocket 长连接           │        │
│                                                        │
│  ┌─ WebSocket Hub ──────────────────────────────┐      │
│  │  Hub.Run() event loop (register/unregister/   │      │
│  │  broadcast)  →  map[userID]map[*Client]bool   │      │
│  │  per-Client: ReadPump (心跳60s) + WritePump   │      │
│  └──────────────────────────────────────────────┘      │
│                                                        │
│  ┌─ 外部编排 ──────────────────────────────────────┐   │
│  │  ChatOrchestrator:                              │   │
│  │    WS文本 → LLM → 解析意图JSON → MQTT下发 →      │   │
│  │    Redis更新 → Hub广播                           │   │
│  └─────────────────────────────────────────────────┘   │
└────────────────────────────────────────────────────────┘
       │                               │
       ▼                               ▼
┌─ EMQX Broker ──┐          ┌─ 云端 LLM API ──┐
│  MQTT :1883     │          │  通义/豆包/DeepSeek │
└────────┬────────┘          └──────────────────┘
         │ MQTT
         ▼
┌─ 家具端 (软件模拟器) ──┐
│  灯 / 空调 / 窗帘 / 插座  │
└─────────────────────────┘
```

---

## 技术栈

| 类别 | 选型 | 说明 |
|---|---|---|
| 语言 | Go 1.26 | module: `github.com/SinnerK9/my-iot-server` |
| HTTP 框架 | Gin | 轻量路由，底层 `net/http` |
| 数据库 | MySQL 8.0 + **sqlx** | 不用 GORM，SQL 透明可控 |
| 缓存 | Redis 7 + go-redis/v9 | Hash + Set + TTL 管理在线状态 |
| 消息中间件 | EMQX 5 + paho.mqtt.golang | MQTT QoS 1，通配符订阅 |
| LLM | 云端 API（OpenAI 兼容） | Go 标准库 `net/http` 直连，零依赖 |
| WebSocket | gorilla/websocket | Hub 并发模型，双 goroutine/连接 |
| JWT | golang-jwt/jwt v5 | Access 15min + Refresh 7d 双 token |
| 密码 | bcrypt cost=10 | golang.org/x/crypto |
| 部署 | Docker Compose | MySQL + Redis + EMQX 一键起 |

---

## 快速开始

### 1. 启动基础设施

```bash
docker compose up -d
```

启动 MySQL（3307）、Redis（6379）、EMQX（1883 + Dashboard 18083）。

### 2. 配置环境变量

```bash
export LLM_KEY="sk-your-api-key"        # 可选——不配则自动降级为关键词匹配
export LLM_URL="https://api.deepseek.com"  # 可选
export LLM_MODEL="deepseek-chat"           # 可选
```

### 3. 启动服务器

```bash
go run ./cmd/server/
```

### 4. 打开演示页面

浏览器打开 `mock.html` → 登录 → 一键创建设备 → 连接 WS → 发送指令。

或使用 curl：

```bash
# 注册
curl -X POST http://localhost:7777/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"phone":"13800138000","email":"test@example.com","password":"12345678","nickname":"test"}'

# 登录（获取 token）
curl -X POST http://localhost:7777/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"account":"13800138000","password":"12345678"}'

# 创建设备
curl -X POST http://localhost:7777/v1/devices \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{"device_id":"light-001","type":"light","name":"客厅灯","room":"客厅"}'

# 非流式聊天
curl -X POST http://localhost:7777/v1/chat \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{"message":"打开客厅的灯"}'

# 流式聊天（SSE）
curl -X POST http://localhost:7777/v1/chat/stream \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{"message":"打开客厅的灯"}' --no-buffer
```

---

## API 总览

### 公开路由

| Method | Path | 说明 |
|---|---|---|
| GET | `/v1/health` | 服务存活检查 |
| GET | `/v1/health/deps` | 依赖健康（DB + Redis） |
| POST | `/v1/auth/register` | 用户注册 |
| POST | `/v1/auth/login` | 登录——返回 JWT 双 token |
| POST | `/v1/auth/refresh` | 刷新 Access Token |

### 受保护路由（需 `Authorization: Bearer <token>`）

| Method | Path | 说明 |
|---|---|---|
| GET | `/v1/users/me` | 当前用户信息 |
| GET | `/v1/devices` | 我的设备列表 |
| POST | `/v1/devices` | 注册新设备 |
| GET | `/v1/devices/:id` | 设备详情 |
| PUT | `/v1/devices/:id` | 修改设备信息 |
| POST | `/v1/devices/:id/bind` | 绑定设备（事务 + FOR UPDATE） |
| DELETE | `/v1/devices/:id` | 解绑设备 |
| GET | `/v1/online/users` | 在线用户列表（Redis） |
| POST | `/v1/chat` | LLM 非流式聊天 |
| POST | `/v1/chat/stream` | LLM 流式聊天（SSE） |

### WebSocket

| Path | 鉴权 | 说明 |
|---|---|---|
| `/v1/ws?token=<jwt>` | query string JWT | 全双工消息通道 |

---

## 项目结构

```
my-iot-server/
├── cmd/
│   ├── server/main.go       # 入口：初始化 → 路由 → Graceful Shutdown
│   └── stress/main.go       # WS 压测脚本（500 连接验证无泄漏）
├── internal/
│   ├── config/config.go     # 环境变量 → struct 映射
│   ├── model/               # User, Device, Request, Response struct
│   ├── handler/             # Gin handlers（HTTP 层）
│   ├── middleware/           # JWT Auth + CORS
│   ├── service/             # 业务逻辑 + ChatOrchestrator 编排
│   ├── repository/          # sqlx CRUD + Redis 操作 + 事务
│   ├── websocket/           # Hub 并发模型 + Client + ReadPump/WritePump
│   └── client/              # LLM HTTP + MQTT 外部调用
├── pkg/
│   ├── jwtutil/jwt.go       # JWT 生成/校验
│   └── hashutil/bcrypt.go   # bcrypt 哈希
├── migrations/              # SQL 建表脚本
├── docker-compose.yml       # MySQL + Redis + EMQX
├── mock.html                # 全链路演示页面
├── docs/                    # 需求/设计/面试题库/技术叙事
└── pprof :6060              # 性能分析（net/http/pprof）
```

---

## 关键设计决策

### 为什么用 sqlx 不用 GORM

sqlx 不隐藏 SQL——`DB.Get/Select/NamedExec` 自动 StructScan 但 SQL 完全透明。面试被问"慢查询怎么排查"时，sqlx 用户直接拿日志里的 SQL 去 EXPLAIN。

### WebSocket Hub 并发模型

`gorilla/websocket` 的 `WriteMessage` 不是线程安全的。每个 Client 持有一个 `Send chan []byte`，唯一的 WritePump goroutine 串行消费——等价于 C++ epoll 架构里的 write_queue + EPOLLOUT。Hub.run() 是单 goroutine event loop，所有 map 操作集中处理——不需要锁。

`close(client.Send)` 时机：必须在 Hub.run() 的 unregister case 里 `delete(map)` 之后。如果在 readPump 里关——广播 goroutine 可能正往 channel 里写——`send on closed channel` panic。

### MQTT + Redis 状态分工

设备在线状态是"高频写、短生命周期"——不适合 MySQL。Redis `HSET` + `EXPIRE 120s` + `SADD online_devices`——心跳每 54s 刷新 TTL，断连 120s 后自动清理。MySQL 只管持久数据（用户、设备归属）。

### LLM 容错三级

1. 无 API Key → 自动降级本地关键词匹配
2. LLM 返回非 JSON（markdown 包裹/尾随注释）→ `extractJSON` 三级提取
3. LLM 超时 → 兜底文本"大模型服务不可用"

---

## 压测结果

```bash
go run ./cmd/stress/ -n 300 -dur 15
```

| 指标 | 结果 |
|---|---|
| 并发连接 | 300 |
| 成功率 | 300/300 (100%) |
| 收发消息 | 47,806 |
| 压测前 goroutine | 21 |
| 压测后 goroutine | **21** |
| goroutine 泄漏 | **0** |

pprof 面板：`http://localhost:6060/debug/pprof/`

---

## 学习笔记

- `docs/MyNotes.md` — C++ ↔ Go 概念映射 + 每日学习记录
