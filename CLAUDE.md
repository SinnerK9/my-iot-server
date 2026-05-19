# CLAUDE.md

## 项目定位

智能家居语音交互助手（SHVA）的 **Go 服务器**——整个系统的消息中枢。

这同时是一个**个人学习 + 面试作品项目**，目标是 4 周内完成一个能写进简历的 Go 后端系统：WebSocket Hub 并发模型 + Redis 状态管理 + LLM 流式链路 + MQTT 设备通信。

## 我的背景

C++ 手写过 epoll + writev + reactor 架构的 WebServer（`cpp_proj1/WebServer_proj`），对 socket/bind/listen/epoll/线程池/RAII 有底层经验。**通过 C++ → Go 概念映射学习 Go**。

**交流语言**：所有注释、回答、讨论全部使用中文。

## 当前进度：Week 1 Day 3

正在做：`database/sql` + `sqlx` 接入 MySQL，建表 users/devices，掌握 Query/QueryRow/Exec/StructScan。

已完成：
- Day 1: Gin 骨架 + Graceful Shutdown（signal.NotifyContext）
- Day 2: 配置管理（env → struct）+ Slog 日志 + docker-compose.yml

## 技术栈与核心决策

| 类别 | 选型 | 备注 |
|---|---|---|
| 语言 | Go 1.26 | module: `github.com/SinnerK9/my-iot-server` |
| HTTP 框架 | Gin | 轻量路由，标准库 `net/http` 打底 |
| 数据库驱动 | **database/sql + sqlx** | 不用 GORM，不隐藏 SQL |
| 缓存 | Redis 7 (go-redis/v9) | 设备在线状态、Token 缓存 |
| 消息中间件 | EMQX 5 (paho.mqtt.golang) | MQTT v3.1.1，只核心 topic，QoS 1 |
| LLM | 云端 API (通义千问/豆包/DeepSeek) | Go Server 直接 HTTPS 调用 |
| 语音服务 | 本地 ASR (FunASR, WS :9100) + TTS (Piper, HTTP :9200) | Week 3 后才涉及 |
| JWT | golang-jwt/jwt | Access 15min + Refresh 7d |
| 密码哈希 | bcrypt cost=10 | golang.org/x/crypto/bcrypt |
| 部署 | Docker Compose | MySQL + Redis + EMQX |

## 项目结构（Week 1 建完不变）

```
iot-voice-gateway/
├── cmd/server/main.go
├── docker-compose.yml
├── internal/
│   ├── config/       // 环境变量 → struct 映射
│   ├── handler/      // Gin handlers（HTTP 层，不含业务逻辑）
│   ├── middleware/   // JWT 鉴权中间件
│   ├── model/        // struct 定义，用 db tag（非 ORM tag），sqlx 映射
│   ├── repository/   // 数据访问层：sqlx + Redis 操作
│   ├── service/      // 薄业务层，调用 repository
│   ├── websocket/    // Hub 并发模型（Week 2 核心）
│   └── client/       // LLM / MQTT 外部调用封装（Week 3）
└── pkg/jwtutil/      // JWT 生成/校验工具
```

## 4 周日程（单开）

### Week 1：地基 + 认证 + 设备管理

| Day | 任务 | 面试锚点 |
|---|---|---|
| 1 | 项目结构 + Gin 骨架 + Graceful Shutdown | 项目结构、Shutdown 机制 |
| 2 | 配置管理 + Slog 日志 + docker-compose | 配置管理策略 |
| **3** ← 当前 | **database/sql + sqlx 接入：建表 users/devices，Query/QueryRow/Exec/StructScan，显式设连接池** | **连接池参数、为什么不用 GORM** |
| 4 | 注册/登录 API + bcrypt + 统一响应封装 {code/msg/data} | 密码安全、bcrypt 原理 |
| 5 | JWT 双 token + middleware 提取 userID | Middleware 模式、双 token 设计 |
| 6 | 设备管理 CRUD（sqlx + 事务 db.Beginx()） | RESTful、事务边界 |
| 7 | Overflow Day | 补 sqlx 调试 / JWT 边界 / 不写新代码 |

**Week 1 避坑**：
- sqlx 只用到 `sqlx.NewDb` / `db.Get` / `db.Select` / `db.NamedExec` / `StructScan`，不深入 `In` 和 `Rebind`
- MySQL 连接池三行（面试必考）：`db.SetMaxOpenConns(25)`、`SetMaxIdleConns(5)`、`SetConnMaxLifetime(time.Hour)`

### Week 2：WebSocket Hub + Redis（核心周，给足时间）

| Day | 任务 | 面试锚点 |
|---|---|---|
| 1 | Hub 结构体：`map[uint64]*Client` + `sync.RWMutex`；Client 持 conn + send chan | 为什么用 Hub 模式 |
| 2 | Hub.Run()：监听 register/unregister/broadcast channel；unregister 先删 map 再 close(send) | channel + 锁协作 |
| 3 | readPump（SetReadDeadline 60s 心跳）+ writePump（单 goroutine 消费 send chan，串行化 WriteMessage） | goroutine 生命周期、WriteMessage 非线程安全 |
| 4 | Hub 联调：20 行 HTML 连 WS，3 标签页测广播；重点调 unregister 边界（重复 close、竞态） | 并发 bug 排查——面试故事素材 |
| 5 | Redis 接入：go-redis/v9；HSET + EXPIRE 120s；SADD online_devices | 数据结构选择：Hash vs String vs Set |
| 6 | Hub + Redis 联动：WS 连接时 HSET+SADD；心跳刷新 TTL；断开时 SREM+HDEL | 缓存一致性、过期策略 |
| 7 | Overflow Day | 重写 Hub 核心 / 调并发 panic |

**Week 2 深水区（预计卡时间的地方）**：
- `close(client.send)` 时机：必须在 Hub.Run() 的 unregister case 里、且已从 map 删除后。如果在 readPump 里直接 close，会和 writePump 的 `range send` 产生竞态 panic
- writePump 用 `range send` 读到零值退出，外部不直接写 client.send，全走 Hub 的 broadcast
- Day 4 调不通直接用 Day 7 追——这是最高优先级

### Week 3：外部链路（LLM + MQTT + 串联）

| Day | 任务 | 面试锚点 |
|---|---|---|
| 1 | LLM 非流式：net/http POST JSON，prompt 硬编码，context.WithTimeout(10s) | HTTP Client 配置、超时控制 |
| 2 | LLM 流式（SSE）：bufio.Scanner 按行读 `data:`，流式返回；超时/异常降级为固定文本 | SSE 协议、流式解析 |
| 3 | MQTT：paho.mqtt.golang，Publish + Subscribe 核心 topic，QoS 1 | MQTT 与 Redis 的分工 |
| 4 | 链路串联：WS 收文本 → LLM → 解析意图 JSON → MQTT Publish → Redis 更新状态 → Hub Broadcast | 系统架构设计、异步链路 |
| 5 | 鉴权补完：WS 升级时 query string 读 `?token=` 校验 JWT；设备操作归属校验 | WebSocket 鉴权方案 |
| 6 | 边界处理：LLM 非预期 JSON 容错、MQTT 断线重连、Redis 断线影响 | 容错设计、降级策略 |
| 7 | Overflow Day | 追 Week 2 Hub 尾巴 / 调端到端 bug |

**Week 3 关键决策**：
- LLM prompt 固定：`用户指令：{text}。解析意图，返回严格 JSON：{"action":"turn_on/off","target":"light"}`
- MQTT 只核心 topic，不做 QoS 2，不做遗嘱消息（面试时能说出"遗嘱机制是生产必备，当前为缩短链路未实现"即可）
- LLM 先非流式跑通 → 再改流式

### Week 4：演示、压测、叙事、投递

| Day | 任务 |
|---|---|
| 1 | Mock 前端：单文件 mock.html，原生 JS 连 WS：登录 → 拿 token → 连 WS → 发文本 → 收推送 |
| 2 | pprof 压测 + 技术叙事文档：500-1000 WS 连接，验证无内存泄漏；写出 5 分钟技术叙事 |
| 3 | 简历修改：突出"独立设计 WS Hub 并发模型 + Redis 状态 + LLM 流式链路" |
| 4 | Go 八股突击：GMP、Channel 底层、GC、内存逃逸、网络编程 |
| 5 | 集中投递：优先"Go 后端/云原生/IoT"方向 |
| 6 | 全局保险日 / Week 2&3 追债 |
| 7 | Overflow Day |

## C++ → Go 关键映射表

| C++ (你的 WebServer) | Go |
|---|---|
| `Logger::get().init("server.log")` | `slog.SetDefault(slog.New(slog.NewJSONHandler(...)))` |
| `socket() + bind() + listen() + epoll_ctl()` 30 行 | `&http.Server{Addr: ":8080", Handler: mux}` 一行 |
| `while(is_running_) { epoller_.wait(-1); for...dispatch }` | `srv.ListenAndServe()` 内部自动 Reactor 循环 |
| `std::thread([&]{ reactor_loop(); }).detach()` | `go func() { srv.ListenAndServe() }()` |
| `ThreadPool::submit(task)` | `go task()` — goroutine 用户态调度，无需线程池 |
| `sigaction() + sigwait(SIGINT)` | `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` |
| `is_running_ = false` 退出循环 | `<-ctx.Done()` → `srv.Shutdown(shutdownCtx)` |
| `RAII: ~Epoller() { close(epoll_fd_); }` | `defer stop()` / `defer cancel()` |
| epoll 按 fd 分发事件 | ServeMux 按 URL 路径分发（不按 fd） |
| `ThreadPool::~ThreadPool() { while(pending>0) sleep(10ms) }` 轮询等待 | `srv.Shutdown(ctx)` — context 超时替代轮询 |
| OS 线程 (8 核 → 8 worker) | goroutine (~2KB 栈，几万个没问题，GMP 调度) |
| `mysql_real_connect()` 重试循环 | Docker healthcheck + `db.Ping()` |

## 编码规范

- **import 路径**：必须从 `go.mod` 的 module 名开始，**禁止相对 import**
  ```go
  // ✅ 正确
  import "github.com/SinnerK9/my-iot-server/internal/config"
  // ❌ 错误
  import "../../internal/config"
  ```
- **可见性**：首字母大写 = 公开（对应 C++ `public:`），小写 = 私有
- **struct 字面量用冒号**，不是点：
  ```go
  // ✅ 冒号
  Port: getenv("PORT", "8080"),
  // ❌ 点——C++ 调方法的肌肉记忆
  Port.getenv("PORT", "8080"),
  ```
- **返回局部变量指针安全**：Go 编译器自动逃逸分析分配到堆（C++ 里这是 UB）
- **Git commit**：[Conventional Commits](https://www.conventionalcommits.org/)（`feat:` / `fix:` / `docs:` / `refactor:` / `test:`）
- **Go 格式**：`gofmt` + `golangci-lint`
- **`.env` 不入 git**，真实配置通过 `.env.example` 提供模板
- **注释用中文**，日志用英文（slog 结构化 key=value）

## 数据库操作原则

**只用 sqlx，不隐藏 SQL**：
```go
// ✅ 会用到的
db.Get(&user, "SELECT * FROM users WHERE id=?", id)
db.Select(&users, "SELECT * FROM users")
db.NamedExec("INSERT INTO users (name) VALUES (:name)", map[string]interface{}{"name": "test"})
// StructScan 是 sqlx 自动做的，Rows.StructScan(&user)

// ❌ 暂时不深入
// In / Rebind —— Week 1 不碰
```

连接池配置（面试必问）：
```go
db.SetMaxOpenConns(25)           // 最大打开连接数
db.SetMaxIdleConns(5)            // 最大空闲连接数
db.SetConnMaxLifetime(time.Hour) // 连接最大存活时间
```

## 基础设施

```bash
docker compose up -d          # 启动 MySQL + Redis + EMQX
docker compose ps              # 查看状态
docker compose logs -f mysql   # 查看日志
```

端口约定：
- `3307` → Docker MySQL（宿主机 3306 被系统 MySQL 占用）
- `6379` → Docker Redis
- `1883` → EMQX MQTT
- `18083` → EMQX Dashboard（admin/public）
- `7777` → Go Server 默认端口（config.go）
- `9100` → ASR Server（Week 3+）
- `9200` → TTS Server（Week 3+）

## 项目约束

- 个人学习 + 面试作品定位，不面向商用
- 4 周周期（2026-05-11 起）
- 每周日 = Overflow Day，不排新任务，只追债/重写
- 家具端用软件模拟器，不走真实硬件
- 分支策略：`dev` → `feature/*`；PR 需审核
