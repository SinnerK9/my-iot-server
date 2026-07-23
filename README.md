# SHVA — Smart Home Voice Assistant

基于 Go 的智能家居后端服务。用户输入自然语言指令（"打开客厅的灯"），系统通过 LLM 解析意图、MQTT 下发控制指令、Redis 管理设备状态、WebSocket 实时推送结果。

---

## 系统架构

```
                    ┌──────────┐
                    │  客户端   │  Android / 浏览器
                    └────┬─────┘
                         │  HTTPS REST  +  WSS 推送
                         ▼
┌────────────────────────────────────────────────────────────┐
│                      Go Server :7777                        │
│                                                             │
│   ┌──────────┐   ┌──────────┐   ┌──────────────┐           │
│   │  Gin      │   │  WebSocket│   │  ChatOrche-   │          │
│   │  Router   │   │  Hub      │   │  strator      │          │
│   │           │   │           │   │               │          │
│   │  Auth     │   │  map[     │   │  WS ─→ LLM ─→ │          │
│   │  Device   │   │   userID] │   │  MQTT ─→ Hub  │          │
│   │  Chat     │   │  ┌──────┐ │   └──────┬───────┘           │
│   │  Health   │   │  │ *Cl. │ │          │                   │
│   └─────┬─────┘   │  ├──────┤ │   ┌──────┴───────┐           │
│         │         │  │ *Cl. │ │   │  LLM Client  │── HTTPS ─→ 云端 API
│   ┌─────┴─────┐   │  └──────┘ │   └──────────────┘           │
│   │  Service  │   │   event   │                               │
│   │  业务逻辑  │   │   loop    │   ┌──────────────┐           │
│   └─────┬─────┘   └────┬─────┘   │  MQTT Client  │── MQTT ──→ EMQX
│         │              │ 广播     └──────────────┘           │
│   ┌─────┴─────┐        │                                     │
│   │Repository │        ├──────── 定向推送 ◄──────────────────│
│   │ sqlx+Redis│        │                                     │
│   └──┬────┬───┘        │                                     │
│      │    │            │                                     │
└──────┼────┼────────────┼─────────────────────────────────────┘
       │    │            │
       ▼    ▼            ▼
  ┌────────┐ ┌────────┐ ┌──────────┐
  │ MySQL  │ │ Redis  │ │   EMQX   │
  │ :3307  │ │ :6379  │ │   :1883  │
  │ 持久化  │ │ 状态/   │ │ 消息路由  │
  └────────┘ │ 缓存    │ └─────┬────┘
             └────────┘       │ MQTT
                              ▼
                       ┌──────────┐
                       │  家具端   │
                       │ (模拟器)  │
                       └──────────┘
```

**四层分层**：`Handler（HTTP 适配） → Service（业务规则） → Repository（数据访问） → Model（结构定义）`

---

## 技术栈

| 类别 | 技术 | 备注 |
|---|---|---|
| 语言 | Go 1.26 | `github.com/SinnerK9/my-iot-server` |
| HTTP | Gin | 路由、中间件、SSE |
| 数据库 | MySQL 8.0 + sqlx | 参数化查询 + StructScan 自动映射 |
| 缓存 | Redis 7 + go-redis/v9 | Hash + Set + TTL |
| WebSocket | gorilla/websocket | Hub 并发模型 |
| MQTT | EMQX 5 + paho.mqtt.golang | QoS 1，通配符订阅 |
| LLM | 云端 API（OpenAI 兼容） | 标准库 `net/http`，零外部依赖 |
| 认证 | JWT (HS256) + bcrypt | Access 15min + Refresh 7d |
| 部署 | Docker Compose | MySQL + Redis + EMQX |

---

## 快速开始

### 1. 启动基础设施

```bash
docker compose up -d
```

启动 MySQL（`3307`）、Redis（`6379`）、EMQX（`1883`，Dashboard `18083` admin/public）。

### 2. 配置环境变量

```bash
export LLM_KEY="sk-your-key"              # 不配则自动降级本地关键词匹配
export LLM_URL="https://api.deepseek.com"  # 默认
export LLM_MODEL="deepseek-chat"           # 默认
```

### 3. 启动服务

```bash
go run ./cmd/server/
```

### 4. 全链路测试

浏览器打开 `mock.html`，按顺序操作：
- 登录 → 一键创建设备 → 连接 WS → 点快捷指令（"开客厅灯"）

或使用 curl：

```bash
# 登录
TOKEN=$(curl -s -X POST http://localhost:7777/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"account":"13800138000","password":"12345678"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['access_token'])")

# 创建设备
curl -X POST http://localhost:7777/v1/devices \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"device_id":"light-001","type":"light","name":"客厅灯","room":"客厅"}'

# 发送指令
curl -X POST http://localhost:7777/v1/chat \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"message":"打开客厅的灯"}'
```

---

## API

### 公开

| Method | Path | 说明 |
|---|---|---|
| `GET` | `/v1/health` | 服务存活 |
| `GET` | `/v1/health/deps` | 依赖健康（DB + Redis） |
| `POST` | `/v1/auth/register` | 注册 |
| `POST` | `/v1/auth/login` | 登录 → JWT 双 token |
| `POST` | `/v1/auth/refresh` | 刷新 token |

### 受保护（`Authorization: Bearer <token>`）

| Method | Path | 说明 |
|---|---|---|
| `GET` | `/v1/users/me` | 当前用户 |
| `GET` | `/v1/devices` | 设备列表 |
| `POST` | `/v1/devices` | 创建设备 |
| `GET` | `/v1/devices/:id` | 设备详情 |
| `PUT` | `/v1/devices/:id` | 修改设备 |
| `POST` | `/v1/devices/:id/bind` | 绑定（事务 + FOR UPDATE） |
| `DELETE` | `/v1/devices/:id` | 解绑 |
| `GET` | `/v1/online/users` | 在线用户 |
| `POST` | `/v1/chat` | LLM 非流式 |
| `POST` | `/v1/chat/stream` | LLM 流式（SSE） |

### WebSocket

| Path | 说明 |
|---|---|
| `/v1/ws?token=<jwt>` | 全双工消息通道 |

---

## 项目结构

```
my-iot-server/
├── cmd/
│   ├── server/main.go       # 入口：初始化所有依赖 → 路由注册 → Graceful Shutdown
│   └── stress/main.go       # WS 压测脚本
├── internal/
│   ├── config/              # 环境变量 → struct
│   ├── model/               # User, Device, Request, Response
│   ├── handler/             # HTTP 层（Gin）
│   ├── middleware/           # JWT Auth, CORS
│   ├── service/             # 业务逻辑 + ChatOrchestrator（全链路编排）
│   ├── repository/          # sqlx CRUD + Redis 操作 + 事务（FOR UPDATE）
│   ├── websocket/           # Hub event loop + Client + ReadPump/WritePump
│   └── client/              # LLM HTTP + MQTT 客户端
├── pkg/
│   ├── jwtutil/             # JWT 签发 / 校验
│   └── hashutil/            # bcrypt
├── migrations/              # DDL
├── docker-compose.yml       # MySQL + Redis + EMQX
├── mock.html                # 全链路演示页面
├── docs/                    # 需求 / 设计 / 学习笔记
└── pprof :6060              # 性能分析
```

---

## 关键设计

### WebSocket Hub——并发安全

`gorilla/websocket` 的 `WriteMessage` 不支持并发调用。每个 Client 持有一个缓冲 `Send chan`，所有想写 Conn 的地方（广播、定向推送、心跳 Ping）往 channel 投递，由唯一的 WritePump goroutine 串行消费写入。Hub.run() 是单 goroutine 事件循环——Register、Unregister、Broadcast 三个 channel 集中处理，Clients map 无需额外加锁。

`close(client.Send)` 由 Hub.run() 在 `delete(map)` 之后执行——避免广播 goroutine 往已关闭 channel 写入导致 panic。

### MQTT + Redis 状态管理

**MQTT**——发布/订阅模式。Topic 为 `shva/device/{id}/cmd` 和 `shva/device/{id}/status`，单层通配符 `+` 一次订阅覆盖所有设备。QoS 1（至少送达一次）。

**Redis**——管理设备在线状态。`HSET device:{id}` 存详情 + `EXPIRE 120s` 自动过期 + `SADD online_devices` 快速集合查询。心跳（54s 间隔）刷新 TTL，断连 120s 后自动清理。

**MySQL**——持久化用户和设备归属。三者分工：MySQL 管持久、Redis 管临时状态、Hub 管瞬时连接。

### LLM——流式 + 容错

非流式：`json.Marshal → http.Post → json.Unmarshal`，10s 超时。

流式：`bufio.Scanner` 逐行读 SSE（`data: {...}`），提取 `choices[0].delta.content`，通过回调实时推送。30s 超时。

容错：无 API Key 时自动降级本地关键词匹配。LLM 返回非标准 JSON（markdown 包裹/尾随注释）时三级提取：直接解析 → 从 ` ```json ``` ` 块截取 → 首尾 `{}` 截断。

### 数据库——sqlx 而非 GORM

用 `database/sql + sqlx`，直接写 SQL，`Get/Select/NamedExec` 自动 StructScan。连接池：`MaxOpenConns(25)`、`MaxIdleConns(5)`、`ConnMaxLifetime(1h)`。事务：`Beginx + SELECT FOR UPDATE + defer Rollback` 保证并发绑定设备时的原子性。

---

## 压测

```bash
go run ./cmd/stress/ -n 300 -dur 15 -token "<jwt>"
```

| 指标 | 结果 |
|---|---|
| 并发连接 | 300 |
| 成功率 | 100% |
| 压测前 goroutine | 21 |
| 压测后 goroutine | **21** |
| 泄漏 | **0** |

性能面板：`http://localhost:6060/debug/pprof/`

---

## 学习笔记

`docs/MyNotes.md` —— C++ epoll reactor → Go 概念映射 + 每日开发记录。
