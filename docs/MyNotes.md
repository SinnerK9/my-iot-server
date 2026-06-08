  ## 修正后的 Week 1 Day 1 笔记：Go 服务入口 vs 你写的 WebServer

  1. 你的 C++ WebServer 启动流程（对照 WebServer_Proj/main.cpp + WebServer.cpp）

  main()
    → Logger::get().init("server.log")          // 日志初始化
    → MySQLPool::get_instance()                 // 连接池单例
    → WebServer server(port, thread_num)        // 构造：创建 Epoller + ThreadPool + HttpConn数组
    → server.start()                            // 进入 Reactor 主循环
         → init_socket_()                       //   socket→setsockopt→bind→listen→epoll_ctl(ADD)
         → while(is_running_) {                 //   事件循环
              epoller_.wait(-1)                 //     epoll_wait 阻塞
              for(i in n) dispatch(fd, events)  //     分发：listen_fd→handle_listen / EPOLLIN→handle_read / ...
           }

  Go 的 main.go 结构完全对应，但不需要你手动管理任何一个 epoll_ctl：

  main()
    → slog.SetDefault(...)                      // 日志初始化（对应你的 Logger::init）
    → http.NewServeMux()                        // 路由（你 Reactor 里没有，Go 自带）
    → srv := &http.Server{Addr, Handler, ...}   // 封装了 socket+bind+listen
    → go srv.ListenAndServe()                   // 新 goroutine 跑 accept 循环
    → signal.NotifyContext(...)                 // 等 SIGINT（对应你的 sigwait）
    → <-ctx.Done()                              // 信号来了
    → srv.Shutdown(ctx)                        // 优雅关闭（对应你的 stop() + close(listenfd)）

  2. 逐段对照分析

  日志初始化 —— 你的 vs Go 的

  // 你的 C++：Logger 单例模式，输出到文件
  Logger::get().init("server.log");
  LOG_INFO("Starting on port %d", port);

  // Go：slog 是标准库内置，不需要自己写 Logger 类
  slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
      Level: slog.LevelInfo,
  })))
  slog.Info("server starting", "addr", addr)  // key=value 对，不是 printf 格式串

  你写 C++ 时自己封装了 Logger 类（Logger::get() 是单例，LOG_INFO 带日志级别）。Go 的标准库 log/slog 把这些做成了内置能力——结构化日志（key=value 对）直接输出 JSON，不需要自己写格式化逻辑。

  socket + bind + listen —— 你手动调，Go 一行封装

  // 你的 WebServer::init_socket_()：
  listen_fd_ = socket(PF_INET, SOCK_STREAM, 0);     // 创建 socket
  HttpConn::set_nonblocking(listen_fd_);              // 非阻塞
  setsockopt(listen_fd_, SOL_SOCKET, SO_REUSEADDR, ...); // 端口重用
  bind(listen_fd_, ...);                              // 绑定
  listen(listen_fd_, 5);                              // 监听
  epoller_.add_fd(listen_fd_, EPOLLIN | EPOLLET | EPOLLRDHUP); // 注册到 epoll

  // Go：全部封装在 http.Server 内部
  srv := &http.Server{
      Addr:              ":8080",            // IP + 端口
      Handler:           mux,                // 路由
      ReadHeaderTimeout: 10 * time.Second,   // 防慢客户端（对应你设 SO_RCVTIMEO 的效果）
  }
  // socket/bind/listen/setsockopt/nonblock/epoll_ctl —— 全部自动完成

  你的 init_socket_() 干了 30 行，Go 一个 struct 字面量解决。Go 的 net/http 标准库内置了 Reactor——ListenAndServe() 内部就是 epoll（Linux 下用 epoll，macOS 用 kqueue），不需要你手动 epoll_create1。

  Reactor 主循环 —— 你的 while(is_running_) vs Go 的自动调度

  // 你的 WebServer::start()：
  while (is_running_) {
      int n = epoller_.wait(-1);           // 阻塞等事件
      for (int i = 0; i < n; i++) {       // 遍历就绪 fd
          int fd = epoller_.get_event_fd(i);
          uint32_t events = epoller_.get_events(i);

          if (fd == listen_fd_) {          // 新连接
              handle_listen_();            // accept 循环
          } else if (events & EPOLLIN) {   // 可读
              handle_read_(fd);            // 读数据 → 提交线程池
          } else if (events & EPOLLOUT) {  // 可写
              handle_write_(fd);           // 发送响应
          } else if (events & EPOLLRDHUP...) { // 异常
              handle_close_(fd);           // 关闭连接
          }
      }
  }

  Go 不需要写这个循环。ListenAndServe() 内部就是这个 while 循环的等价实现。Go 的 netpoll（运行时内置的 I/O 多路复用器）自动处理 accept → read → 分发 handler → write → close。

  区别在于：你 Reactor 的 事件分发这一层在 C++ 里必须自己写（if fd == listen_fd → X, elif EPOLLIN → Y）。Go 里这一层被 ServeMux（路由器）替代——根据 URL 路径分发，不是根据 fd 和 epoll event
  分发。但底层原理完全一样：都是事件驱动、非阻塞 I/O。

  你 handle_read 里的线程池提交 —— 对应 Go 的 goroutine

  // 你的 handle_read_()：
  thread_pool_.submit([this, fd]() {
      users_[fd].process();  // HTTP 解析 + 生成响应，CPU 密集，丢线程池
  });

  Go 不需要线程池：
  // Go handler 里：
  go func() {
      // 任何 CPU 密集操作直接 go，不需要线程池
      result := heavyComputation()
      w.Write(result)
  }()

  为什么 Go 不需要 ThreadPool？ 你 C++ 里 std::thread 创建的是 OS 线程，8 核机器跑 8 个 worker 线程是最优的。Go 的 goroutine 是用户态调度的——创建成本 ~2KB 栈 + 微秒级，几万个 goroutine 被 Go 运行时自动映射到几个 OS
  线程上执行（这就是 GMP 调度模型）。你写的 ThreadPool 实际上是个 goroutine 调度器的极简版——Go 把这件事做进了语言运行时。

  优雅退出 —— 你的 stop() vs Go 的 Shutdown

  // 你的 WebServer::stop()：
  void WebServer::stop() {
      is_running_ = false;  // 设原子变量，while(is_running_) 退出
  }
  // 但 notice：你没有在 stop() 里 close(listen_fd_)，也没有等 in-flight 请求完成

  // Go 的优雅退出：
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()
  <-ctx.Done()   // ← 这一行等价于你 Reactor 的 while(is_running_) + epoll_wait 一起被打断

  shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  defer cancel()
  srv.Shutdown(shutdownCtx)
  // Shutdown 做了三件事：
  // 1. close(listenfd)        —— 不再接受新连接
  // 2. 等所有正在处理的请求完成  —— 你的 ThreadPool 析构函数做的事
  // 3. 超时 5 秒后强制关闭      —— 你目前代码里没有的超时保护

  你的 WebServer::stop() 只是设了个 atomic<bool>，没有真正等线程池里的任务跑完——你依赖 ThreadPool 析构函数里的 while(pending_tasks_ > 0) sleep(10ms)，这是个轮询等待，不够优雅。Go 的 Shutdown(ctx) 用 context
  超时机制替代了轮询。

  3. 你今天手打的 main.go 和你已有知识的精确映射

  ┌───────────────────────────────────────────┬───────────────────────────────────────────────────────────────────┐
  │            你打的那行 Go 代码             │                    精确对应你 C++ 项目里的...                     │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤
  │ slog.SetDefault(slog.New(...))            │ Logger::get().init("server.log")                                  │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤
  │ http.NewServeMux()                        │ 无对应——你 Reactor 的 for(i in n) dispatch，Go 按路径分发而非 fd  │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤
  │ &http.Server{Addr: ":7777", Handler: mux} │ init_socket_() 的全部 30 行 + is_running_ 变量                    │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤
  │ go func() { srv.ListenAndServe() }()      │ std::thread([&]{ while(is_running_) epoller_.wait(-1) }).detach() │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤
  │ signal.NotifyContext(...)                 │ <signal.h> + sigaction() 注册 handler                             │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤
  │ <-ctx.Done()                              │ while(is_running_) 循环被打破的那一瞬间 + sigwait                 │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤
  │ srv.Shutdown(ctx)                         │ stop() + ThreadPool::~ThreadPool() 里的 while(pending>0) sleep    │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤
  │ defer stop() / defer cancel()             │ RAII: ~Epoller() 里的 close(epoll_fd_)


---

## Week 1 Day 2：配置管理 + Docker 基础设施

### 一、Config 包：环境变量 → struct 映射

新建 `internal/config/config.go`，修改 `cmd/server/main.go` 用 `config.Load()` 替代硬编码。

#### 公开 vs 私有——首字母大小写决定

```go
type Config struct {  // 大写 C → 导出类型，外部包可用
    Port   string     // 大写 P → 导出字段
}
func Load() *Config { ... }  // 大写 L → 导出函数
func getenv(key, def string) string { ... }  // 小写 g → 包内私有
```

C++ 里你写 `public:` / `private:`，Go 没有这两个关键字。**首字母大小写决定可见性**。

#### 这个语法坑了我：struct 字面量用冒号不是点

```go
// ✅ 正确：冒号
Port:   getenv("PORT", "8080"),

// ❌ 错误：点 —— 这是 C++ 调方法的习惯
Port.getenv("PORT", "8080"),
```

Go 里点号只用于调方法/访问实例字段，struct 字面量赋值用冒号。

#### 返回局部变量指针——Go 可以，C++ 不行

```go
func Load() *Config {
    return &Config{...}  // C++ 里这是未定义行为，Go 里编译器自动逃逸分析分配到堆上
}
```

#### import 路径必须从 go.mod 的 module 名开始

```go
// ❌ Go module 禁用相对 import
import "../../internal/config"

// ✅ 必须用模块全路径
import "github.com/SinnerK9/my-iot-server/internal/config"
```

---

### 二、docker-compose.yml：基础设施即代码

github.com/SinnerK9/my-iot-server/internal/config

#### 它解决什么问题

C++ webserver 开发时 MySQL 是手动 `apt install` 装系统上的。换机器得重装。docker-compose 把基础设施变成一个配置文件，`docker compose up -d` 一行全起。

#### 文件结构三层

```
version          ← 语法版本
services:        ← 你要跑的几个容器
  mysql:         ←   数据库
  redis:         ←   缓存
  emqx:          ←   消息中间件
volumes:         ← 声明持久化卷
```

#### 每个 service 回答六个问题

| 字段 | 解决的问题 | MySQL 例子 | Redis 例子 |
|---|---|---|---|
| `image` | 镜像从哪来？ | `mysql:8.0` | `redis:7-alpine`（5MB 精简 Linux） |
| `container_name` | 容器叫什么？ | `iot-mysql`（方便 `docker logs iot-mysql`） | `iot-redis` |
| `restart` | 挂了怎么办？ | `unless-stopped`（crash 自动拉起，手动 stop 尊重你） | 同 |
| `ports` | 怎么和外界通信？ | `"3307:3306"`（宿主机:容器内） | `"6379:6379"` |
| `volumes` | 数据存哪？ | 命名卷 + migrations 自动建表 | `redis_data:/data` |
| `healthcheck` | 怎么确认就绪？ | `mysqladmin ping` 每 5s 测 | `redis-cli ping` |

#### 端口映射：为什么用 3307

系统已有 MySQL（C++ webserver 时期装的）占着 3306。Docker 用 3307 映射：

```
C++ WebServer  →  127.0.0.1:3306  (系统 mysqld，不动)
Go 项目        →  127.0.0.1:3307  (Docker MySQL，容器内仍是 3306)
```

#### volumes 两种挂载

- `mysql_data:/var/lib/mysql` — 数据持久化，容器删了表还在（对应 C++ MySQL 的 datadir）
- `./migrations:/docker-entrypoint-initdb.d` — 容器第一次启动自动执行目录下所有 `.sql` 文件

#### healthcheck：轮询等待机制

MySQL 容器"起来"≠ 进程能接 SQL（有 10-30s 初始化）。healthcheck 让 Docker 替你做轮询：

```cpp
// 对应你 C++ 代码里手动写的重试逻辑：
while (retry < 10) { if (mysql_real_connect(...)) break; sleep(5); }
```

healthcheck 把这段逻辑从代码层移到了基础设施层。

#### EMQX — MQTT Broker

IoT 设备通信的标准协议。你智能家居里"打开灯"指令走 MQTT：

```
Go Server → MQTT Publish("device/light/cmd", "turn_on") → EMQX → 灯收到指令
```

Week 3 才用，现在提前跑着。

---

### 三、config.go 和 docker-compose 的对齐

| config.go 默认值 | docker-compose 对应 | 含义 |
|---|---|---|
| `DBHost: "127.0.0.1"` | （隐式） | Go 连宿主机 |
| `DBPort: "3307"` | `ports: "3307:3306"` | 宿主机 3307→容器 3306 |
| `DBUser: "root"` | MySQL 默认 | root 用户 |
| `DBPass: "123456"` | `MYSQL_ROOT_PASSWORD: "123456"` | 必须一致 |
| `DBName: "iot_gateway"` | `MYSQL_DATABASE: iot_gateway` | 自动创建 |

---

### 四、今天踩的坑

1. Go module 不能用相对 import，必须从 go.mod 的 module 名开始
2. struct 字面量赋值用冒号不是点——C++ 调方法的肌肉记忆
3. config.go 密码默认值要和 docker-compose 一致
4. 宿主机 3306 被系统 MySQL 占，改 3307，C++ 项目照旧用 3306
5. Docker Desktop 需在 Settings→Resources→WSL Integration 打开 Ubuntu-22.04

---

## Week 1 Day 3：数据库迁移 + sqlx 接入 + 分层架构

### 一、核心工程思想：Migrations（数据库迁移）为什么不是代码里建表

#### C++ 做法（你在 WebServer 里做的）

```cpp
// main.cpp 或 init 函数里：
MYSQL* conn = mysql_real_connect(...);
const char* sql = "CREATE TABLE IF NOT EXISTS users (id INT AUTO_INCREMENT PRIMARY KEY, ...)";
mysql_real_query(conn, sql, strlen(sql));
```

问题：**CREATE TABLE IF NOT EXISTS 永远不会帮你加新字段**。

明天你要给 users 表加一个 `avatar` 字段。你改代码里的 CREATE TABLE 字符串，重启程序——MySQL 一看"表已经存在了"，跳过，什么都不做。你只能手动 `mysql -u root -p` 进去敲 `ALTER TABLE`。换一台机器、换一个同事——他不知道你手动改了什么。

#### 工程化做法：Migrations

```
migrations/
├── 001_init.sql            ← 建 users + devices 表
├── 002_add_avatar.sql      ← ALTER TABLE users ADD COLUMN avatar VARCHAR(255)
└── 003_add_refresh_token.sql ← 以后再迁移
```

核心思想：

1. **每个数据库变更是一个新文件**，不改已有文件。文件名带序号，按顺序执行。
2. **每次变更都是增量**。001 是建表，002 是加列，003 是建索引——永远不会 "修改 001 然后重新跑"。
3. Git 版本控制能 diff 出来——"周三关梓浩加了个 avatar 字段，看 002_add_avatar.sql"。
4. 新环境 `docker compose up -d` 时，MySQL 容器的 `docker-entrypoint-initdb.d` 机制按文件名顺序执行所有 `.sql` 文件，得到完全一致的数据库结构。

#### 为什么这很重要

> "数据库 schema 和代码一样，必须受版本控制。Migrations 是数据库变更的唯一执行通道，它保证了所有环境（开发/测试/生产）的结构一致性。代码回滚时，你能精确知道数据库处于哪个 schema 版本。"

| 维度 | C++ WebServer | 这个 Go 项目 |
|---|---|---|
| 建表位置 | 代码里 `mysql_real_query("CREATE TABLE...")` | 独立的 `migrations/` SQL 文件 |
| 加新字段 | 手动连 MySQL 敲 ALTER，或改代码但重启不生效 | 新建 `002_xxx.sql`，跑一下就生效 |
| 数据库版本 | 没有版本概念——不知道现在是什么状态 | 文件名就是版本——`001` → `002` → `003` |
| 团队协作 | 本地改了，同事不知道，上线炸 | 所有环境跑同一批 migrations |
| 历史追溯 | 不知道是谁、什么时候、为什么改了表结构 | Git log 看 migrations 文件变更 |

---

### 二、MySQL 在这个 IoT 项目里存什么

四种核心数据：

| 表 | 内容 | 为什么需要 |
|---|---|---|
| `users` | 用户账号、密码 bcrypt hash | 注册/登录/JWT 鉴权 |
| `devices` | 设备 ID、归属用户、类型、在线状态 | "这个用户的设备列表"、"这个设备是你的才能控制" |
| `conversations` | 一次语音对话会话 | 查看历史对话 |
| `messages` | 对话里每条消息 | 展示对话内容 |

**MySQL 和 Redis 的分工**：

- **MySQL**：持久化——用户信息、设备绑定关系、历史对话。关机后还在。
- **Redis**：实时状态——谁在线、设备心跳、接口限流计数。丢了也能靠心跳重建。

比如"设备当前在线/离线"存在 Redis（HSET + EXPIRE 120s），因为它变化快、读写频繁。但"设备属于哪个用户"存在 MySQL——绑定关系不能丢。

---

### 三、Go 项目的分层架构

一个 HTTP 请求的完整路径：

```
客户端 POST /auth/register
  → cmd/server/main.go        （组装所有组件，启动服务器）
  → internal/middleware/       （安检：没 JWT → 401，有 → 解析 userID → 放行）
  → internal/handler/          （门面：收 HTTP 参数 → 调 service → 返回统一 {code,msg,data}）
  → internal/service/          （大脑：业务规则、校验、编排调用顺序）
  → internal/repository/       （管家：和数据库/Redis 对话，所有 SQL 写在这里）
  → internal/model/            （数据结构：Go struct ↔ 数据库行的映射）
```

**铁律**（违反即分层崩塌）：

```
handler → service → repository
   ✓         ✓          ✓

永远不允许：
  ✗ handler 直接调 repository（跳过了 service，业务规则无处放）
  ✗ service 直接拼 SQL 字符串（跳过了 repository，SQL 散落各处）
  ✗ repository 返回 HTTP 状态码（污染了数据层）
```

#### 对应你的 C++ 代码

| Go 层 | C++ WebServer 对应 |
|---|---|
| `handler` | `HttpConn::process()` 里解析 HTTP 头和 body 的部分 |
| `service` | `HttpConn::process()` 里做判断/调用/组装结果的业务逻辑 |
| `repository` | `MySQLPool::query()` 执行 SQL 的部分 |
| `model` | `struct User { int id; string phone; }` 头文件 |
| `middleware` | `if (fd_ == -1) { send_error(401); return; }` 鉴权检查 |
| `config` | 命令行参数 / ini 配置文件解析 |

**为什么在 C++ 里全混在一个函数里没事，Go 里要分层**：

C++ WebServer 是一个教学项目，500 行逻辑集中在一个 `HttpConn::process()` 里你能驾驭。但这个 Go 项目有 WebSocket Hub（Week 2）+ LLM 链路（Week 3）+ MQTT 通信，总行数至少 2000+。不分层的话，当你 Week 3 回来看 Week 1 代码时，你已经不知道 SQL 写在哪里了。

**每一层只有被上层调用，层级之间不跳跃。这使得：**

- 改数据库驱动（从 MySQL 换 PostgreSQL）→ 只改 repository
- 改 HTTP 返回格式（加个字段）→ 只改 handler
- 改密码强度规则 → 只改 service
- 其他层完全不动

---

### 四、sqlx 四个核心方法

| 方法 | 用途 | 示例 |
|---|---|---|
| `DB.Get(&dest, sql, args...)` | 查**一行** | `DB.Get(&user, "SELECT * WHERE phone=?", phone)` |
| `DB.Select(&dest, sql, args...)` | 查**多行** | `DB.Select(&users, "SELECT * WHERE status=?", "online")` |
| `DB.Exec(sql, args...)` | 写操作，不关心返回值 | `DB.Exec("DELETE FROM users WHERE id=?", id)` |
| `DB.NamedExec(sql, struct)` | 写操作，用 struct 字段名匹配占位符 | `DB.NamedExec("INSERT INTO users (name) VALUES (:name)", user)` |

### 五、为什么不用 GORM

| | GORM | sqlx |
|---|---|---|
| SQL 可见性 | 隐藏——`db.Where(...).First(&user)` 你不知道生成了什么 SQL | 透明——你就是写 SQL 的人 |
| 学习对象 | 学的是 GORM 方言（Preload、Joins、Association） | 学的是标准 SQL + Go 类型映射 |
| 面试排查 | 慢查询？你得先让 GORM 打日志看它生成了什么 SQL | 慢查询？这就是你写的 SQL，直接 EXPLAIN |
| 适用人群 | 不想写 SQL 的新手 | 你——C++ 里手写过 `mysql_real_query()`，SQL 不是问题 |

**核心原则**：你已有 SQL 能力，sqlx 只是帮你去掉了 C++ 里 `while(row = mysql_fetch_row(res)) { 手动赋值到 struct }` 的体力活。GORM 则是在你的 SQL 能力上套了一层自己发明的方言——你学的不是"Go 怎么操作数据库"，而是"GORM 怎么隐藏数据库"。

---

## Day 3 答疑笔记：深入理解数据库层的每个细节

> 以下是我在实际写代码过程中提出的问题，以及搞懂后的理解。

### Q1：sqlx 到底是干啥的？和 database/sql、mysql driver 的关系？

**真实的三层架构**：

```
你的代码
    │
    ▼
sqlx.DB ──┬── 内嵌 *sql.DB ──→  连接池、Ping、Exec、事务
          │                    （database/sql 标准库干的）
          │
          └── 自己的能力 ──→  DB.Get / DB.Select / DB.NamedExec
                              （sqlx 干的，省掉手动逐行赋值）
```

**sqlx 不是"只是一个翻译器"——它内嵌了标准库的 `*sql.DB`，继承所有连接池能力，再在上面加 struct 映射。** 我们平时只跟 sqlx 打交道，但它大部分活是透传给 `database/sql` 干的。因为 Go 的 struct 内嵌让 `sqlx.DB` 和 `*sql.DB` 看起来像一个东西。

### Q2：为什么匿名 import mysql driver？（`_ "github.com/go-sql-driver/mysql"`）

下划线 `_` 的意思是：**执行这个包的 `init()` 函数，但我不直接用包里的任何导出符号**。

```go
// go-sql-driver/mysql 内部：
func init() {
    sql.Register("mysql", &MySQLDriver{})  // 把自己注册到 database/sql 的驱动表里
}
```

之后你写 `sqlx.Open("mysql", dsn)` 时，database/sql 查表找到 `"mysql"` → MySQLDriver → 建连接。**没有匿名 import，注册表是空的，`sqlx.Open("mysql", dsn)` 直接报 `unknown driver`。**

这个设计的好处：换 PostgreSQL 只需要改两行——把 mysql driver 的匿名 import 换成 pg driver，Open 的 `"mysql"` 改成 `"postgres"`，其他所有代码完全不变。因为所有操作走的是 `database/sql` 标准接口，不是 MySQL 专有 API。

### Q3：sqlx.Open() 和 db.Ping() 分别做了什么？

**`sqlx.Open("mysql", dsn)`**：
- 在内存里创建一个 `sqlx.DB` 对象，初始化连接池数据结构
- **不拨号，不 TCP 握手，不验证密码**
- 只要驱动存在，Open 永远不报错

**`db.Ping()`**：
- 真的拨号到 MySQL，TCP 三次握手 → MySQL 握手协议 → 发 `SELECT 1` → 拿到响应
- 验证一切：地址可达、用户名密码对、数据库存在、网络通
- 成功后把连接放回池子里

**为什么拆成两步**：你可以在 Open 和 Ping 之间设置连接池参数：

```go
db, _ := sqlx.Open("mysql", dsn)  // 创建对象
db.SetMaxOpenConns(25)             // 配置池子——必须先有对象才能调
db.SetMaxIdleConns(5)
db.SetConnMaxLifetime(time.Hour)
db.Ping()                          // 配置好了再真正连接
```

对应你 C++ 的 `mysql_real_connect()`——C++ 一个函数同时做了 Open + Ping 两件事。Go 拆成两步是为了让你在中间插入配置。

### Q4：model/user.go 和 migrations/001_init.sql 是什么关系？

**没有代码层面的联系。** 这两套东西靠你手动保持一致。

```
001_init.sql → MySQL 里 users 表的列定义
user.go      → Go struct，db tag 告诉 sqlx "MySQL 的 phone 列对应 User.Phone 字段"
```

**运行时怎么串起来的**：sqlx 执行 SELECT 拿到结果后，用反射读 struct 的 db tag，建立列名 → 字段的映射表，然后自动填充。**sqlx 不会去读 001_init.sql，不会去查 MySQL 的表结构，只认 struct 上的 `db:"xxx"`。** 因此 SQL 列名和 db tag 必须一致——但这是你手动保持的，没有编译器检查。

model 包不需要 import sqlx——它是纯数据定义，不知道什么叫数据库。读 tag 的是 sqlx（运行时反射）。

### Q5：DB.Get() 内部做了哪几步？

```go
err := DB.Get(&user, "SELECT id,phone,... FROM users WHERE phone=?", phone)
```

1. **预编译**：把 `?` 替换成参数值，防止 SQL 注入。`?` 不是字符串拼接——sqlx 底层调 `database/sql` 的 `Prepare()`，参数和 SQL 结构分开传输。对应 C++ 的 `mysql_stmt_prepare()` + `mysql_stmt_bind_param()`。
2. **执行**：发 SQL 给 MySQL。对应 C++ 的 `mysql_real_query()`。
3. **映射**：用反射读 user struct 的 db tag，找到 `phone` 列 → `User.Phone` 字段，自动填充。
4. **返回**：填充好的 `*model.User`。

这一步替代了你 C++ 里 20 行：`mysql_stmt_init → prepare → bind_param → execute → store_result → bind_result → fetch → 手动逐列赋值 → close`。

### Q6：SQL CRUD 四条语句

| 语句 | 作用 | 模式 |
|---|---|---|
| `SELECT 列 FROM 表 WHERE 条件` | 读数据 | 查 |
| `INSERT INTO 表 (列...) VALUES (值...)` | 写新数据 | 增 |
| `UPDATE 表 SET 列=值 WHERE 条件` | 修改已有数据 | 改 |
| `DELETE FROM 表 WHERE 条件` | 删除数据 | 删 |

**不加 WHERE 的 UPDATE/DELETE 会操作全表**——这是不可逆的。

SELECT 显式列出列名（不用 `SELECT *`）的原因：
- `SELECT *` 以后表加了新列，sqlx 发现 struct 上没有对应字段可能报错
- 显式列名让你一眼看懂了返回哪些字段

### Q7：Go 包级变量的可见性

**`var DB *sqlx.DB`（大写 D）→ 整个项目都能访问。**
**`var db *sqlx.DB`（小写 d）→ 只有 repository 包能访问。**

对应 C++ 的 public/private：

```
大写 = 公开（包外 `repository.DB` 可用）
小写 = 包内私有（只有同一个 package 的文件能访问）
```

跟是不是全局变量没关系，只看首字母大小写。Go 没有 `public`/`private` 关键字，用首字母大小写决定可见性。

### Q8：编译踩坑 — `undefined: addr`

往 main.go 加了 cfg 初始化后，原来的 `addr := ":8080"` 丢了。修复用 config 的 Port：

```go
addr := ":" + cfg.Port  // 从环境变量/默认值 7777 取端口
```

config.go 的 `Load()` 读环境变量 PORT，默认值 `"7777"`，所以最终 `addr = ":7777"`。

---

## Day 4：注册/登录 API — 整条业务链路从零到跑通

### 一、Day 4 到底做了什么

Day 1-3 已经搭好了基础设施（Gin 骨架、配置管理、sqlx 连接池、模型层），但这些都是**准备动作**——没有一条从客户端请求到数据库响应的完整链路。

Day 4 的核心命题：**把数据库能力穿上 HTTP 的外衣**，让外部世界可以通过 REST API 注册和登录。

**注册请求的完整旅程**：

```
客户端 POST /v1/auth/register
  {"phone":"138...","email":"t@t.com","password":"12345678","nickname":"test"}
    │
    ▼
  Gin Router (main.go)     ← URL → 处理函数的映射表
    │
    ▼
  Handler 层                ← HTTP 协议相关的事：读请求体、校验参数、返回 JSON
    c.ShouldBindJSON(&req)   ← JSON 反序列化 + binding tag 校验，一步完成
    service.Register(&req)   ← 把活交给下层，自己不碰业务规则
    model.OK(c, data)        ← 包装 JSON 响应
    │
    ▼
  Service 层                ← 纯业务逻辑：查重、哈希、写入
    ├── 检查手机号是否已注册
    ├── 检查邮箱是否已注册
    ├── bcrypt 哈希密码
    └── repository.CreateUser(user)
    │
    ▼
  Repository 层             ← 只做数据库操作：SQL + sqlx 映射
    DB.NamedExec("INSERT INTO users ...", user)
    │
    ▼
  MySQL (Docker 3307)       ← users 表多了一行，password 列是 bcrypt 密文
```

### 二、对 Gin 框架的具象理解——它到底替代了什么

我在 Day 1 用的是标准库 `net/http`：

```go
// 标准库 handler：两个参数
mux.HandleFunc("v1/health", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")  // 手动设头
    w.Write([]byte(`{"status":"ok"}`))                   // 手动写字节
})
```

Gin 给了一个增强版的 handler 接口：

```go
func Ping(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
```

**`c *gin.Context` 是什么**：它就是把 `w http.ResponseWriter` 和 `r *http.Request` 合并成一个对象，再加了一堆便利方法。

| 标准库写法 | Gin 写法 | 少写了什么 |
|---|---|---|
| `r.URL.Query().Get("token")` | `c.Query("token")` | 从 Request 取参数 |
| `json.NewDecoder(r.Body).Decode(&v)` | `c.ShouldBindJSON(&v)` | JSON 反序列化 + 校验 |
| `w.Header().Set("Content-Type", ...)` + `json.NewEncoder(w).Encode(obj)` | `c.JSON(200, obj)` | 设头 + 序列化 + 写入，一步 |
| `w.WriteHeader(400)` | `c.Status(400)` | 设状态码 |
| `r.Method` | `c.Request.Method` | 通过 c 仍能拿到原始 Request |

**Gin 没有替代 `net/http`——底层还是 `http.Server`**。`gin.Default()` 返回的对象实现了 `http.Handler` 接口，所以可以原样传给 `&http.Server{Handler: r}`。graceful shutdown 逻辑也完全不用改——`srv.Shutdown()` 对 Gin 和标准库是一样的，因为底层是同一个 `http.Server`。

对应你 C++ 的经验：标准库相当于裸调 `socket+bind+listen+epoll`，Gin 相当于你的 `HttpConn::process()` 之上加了一层路由分发和参数解析——但底层 epoll 循环仍然是 Go runtime 在跑，Gin 不碰那一层。

### 三、struct tag 的三位读者

struct tag 看起来像注释，但其实是**给特定库看的配置字符串**。Go 编译器不管 tag 里写了什么，每个库用反射去读自己关心的那部分。

```go
Phone string `json:"phone" binding:"required,len=11" db:"phone"`
//             └── json 库读  └── Gin validator 读 ──┘ └── sqlx 读
```

| tag | 谁读 | 什么时候 | 做了什么 |
|---|---|---|---|
| `json:"phone"` | `encoding/json` | 序列化/反序列化 | JSON 里叫 `phone` ↔ Go 里叫 `Phone` |
| `binding:"required,len=11"` | Gin 的 `ShouldBindJSON` | 解析请求体时 | 校验：非空、11 位 |
| `db:"phone"` | sqlx 的 `StructScan` | SQL 行 → struct | 数据库列 `phone` ↔ `User.Phone` |

**三个 tag 互不干扰，各读各的。** Response struct 没有 `binding` 和 `db` tag，因为它只管输出 JSON。User model 没有 `json` tag（用了 `db` tag），因为它只管数据库映射——API 返回时用 `gin.H` 手动构建 JSON，不直接序列化 User。

### 四、统一响应格式 {code, msg, data}

前后端约定一个外壳：

```json
{"code": 0, "msg": "ok", "data": {...}}
```

- **`code`**：给机器判断（`if res.code === 0`）
- **`msg`**：给人看（弹出提示、调试日志）
- **`data`**：真正的业务数据

为什么不能每个接口返回不同格式——如果注册返回 `{"success":true}`，登录返回 `{"status":"ok"}`，前端要针对每个接口写不同判断。统一格式让前端只需一段判断逻辑。

**`interface{}` 就是 C++ 的 `void*`**——可以接收任何类型，所以 data 字段可以是用户对象、列表、数字、nil。

### Q1：pkg 层到底放什么？为什么是 hashutil 而不是 utils？

`pkg/` 放的是和业务无关的纯工具——换个项目也能直接用。`internal/` 放的是这个项目私有的实现，外部不能 import。

Go 风格是**小包优先**：`pkg/hashutil/`（2 个函数）、`pkg/jwtutil/`（3 个函数），而不是一个 `pkg/utils/` 装十几个不相关的函数。

好处：`hashutil.HashPassword()` 读起来是自然语言，import 列表一眼就知道依赖了什么。编译也更快——改 hashutil 只影响用它的文件，不会连累 jwtutil。

### Q2：bcrypt 原理——为什么用 cost=10

bcrypt 是专门设计来抵抗暴力破解的**慢哈希**：

```
"12345678" → 生成 16 字节随机 salt → 对 salt+password 做 2^10 = 1024 轮 Blowfish 加密
→ 输出 "$2a$10$<22字符salt><31字符hash>"
```

- **cost=10 就是 1024 轮**，一次验证大约 50-100ms——对用户无感，对暴力破解是灾难（试 100 万个密码需要约 30 小时）
- **salt 直接编码在输出里**，`CompareHashAndPassword` 自动从 hash 提取 salt 用于验证，不需要单独存储
- **SHA-256 不适合存密码**：它是快哈希，GPU 每秒能跑几十亿次；bcrypt 用 1024 轮刻意拖慢

### Q3：注册/登录安全——"账号或密码错误"为什么不区分

不管账号不存在还是密码不对，都返回同一个消息。这不是糊弄用户——如果区分"账号不存在"和"密码错误"，攻击者可以用差异来枚举注册了哪些账号（user enumeration）。**不泄露用户存在性**是安全基线。

### 五、今天踩的坑

1. **migrations SQL 注释导致建表失败**：MySQL 的 `--` 注释要求 `-- `（双横线后面有空格）。写了 `--注册时间`（没空格）→ MySQL 不认识 → `ERROR 1064 (42000)` → 表没建。改成不用行内注释，说明文字放在 SQL 外面的 COMMENT 子句里。

2. **Docker MySQL 初始化脚本只跑一次**：`docker-entrypoint-initdb.d` 机制只在 MySQL 数据目录**第一次初始化**时执行。后来改了 SQL 文件，`docker compose down -v` 删数据卷重建才行。

3. **非业务错误被吞掉**：Handler 里系统错误返回了 `5000` 但没有打 `slog.Error`——根本不知道数据库返回了什么错。加上 `slog.Error("Register failed", "err", err)` 后从日志秒定位到 `Table 'iot_gateway.users' doesn't exist`。

4. **`.gitignore` 的 `server` 模式误拦了 `cmd/server/`**：`server` 是匹配任意路径组件名，所以 `cmd/server/main.go` 也被忽略。改成 `/server`（只匹配根目录）解决。

### 六、今天新掌握的工具

```bash
docker logs iot-mysql                          # 看容器日志（建表错误在这里）
docker exec -i iot-mysql mysql -uroot -pXXX DB < file.sql  # 手动执行 SQL
docker compose down -v                          # 删除容器 + 数据卷（重建用）
```

### 七、全服务链路验证

运行 `go run ./cmd/server/` 后 curl 测试结果：

```
注册 → {"code":0,"msg":"ok","data":{"user_id":1}}    ✅
重复注册 → {"code":4090,"msg":"手机号已被注册","data":null}  ✅
登录 → {"code":0,"msg":"ok","data":{...用户信息...}}   ✅
密码错误 → {"code":4010,"msg":"账号或密码错误","data":null} ✅
```

整条链路：`curl JSON → Gin 路由 → ShouldBindJSON 校验 → Service 逻辑 → bcrypt 哈希 → repository NamedExec → MySQL INSERT → 返回统一 JSON`，全部跑通。


---

## Day 5：JWT 双 Token 工具包 — Access(15min)+Refresh(7d)+ParseToken

### 一、从硬编码登录到 JWT——我的起点

我的 C++ WebServer 的登录是这样的：

```cpp
const string ADMIN_USER = "admin";
const string ADMIN_PASS = "123456";
if (req.username == ADMIN_USER && req.password == ADMIN_PASS) {
    // 放行
}
```

这是**硬编码单用户认证**——用户名密码写死在代码里，只区分"通过"和"不通过"两个状态。没有会话概念，没有多用户，没有过期机制。

Day 5 的实际产出是 `pkg/jwtutil/` 工具包——提供 token 的生成和验证能力，供 Day 5 后半段（middleware + handler 改造）使用。今天先把这个"核心发动机"讲透。

### 二、Session vs JWT——两种"记住你是谁"的方案

你登录成功后，下一次请求怎么证明"刚才登录过了"？两种方案：

**方案 A：服务端 Session**

```
登录成功 → 服务器生成随机 session_id → 存入 Redis: session_id → userID
后续请求 → 客户端带 session_id → 服务器查 Redis → 拿到 userID
```

这是最直观的方案——对应 C++ 里你可能会做 `std::unordered_map<string, int> sessions`。

| 优点 | 缺点 |
|---|---|
| 服务端随时可以踢人（删 session） | 每次请求要查 Redis/内存 |
| 实现简单 | 服务器重启 → 内存 session 全丢 |

**方案 B：JWT（我们选的方案）**

```
登录成功 → 服务器把 userID 签名进 token → 把 token 字符串返回客户端
后续请求 → 客户端带 Authorization: Bearer <token> → 服务器验签 → 直接读 userID
```

| 优点 | 缺点 |
|---|---|
| 验签是纯数学运算，不查数据库/Redis | 签发后无法直接撤销 |
| 无状态——服务器重启不受影响 | 如果 token 泄露，在过期前攻击者能一直用 |

**我们为什么选 JWT**：Week 2 要做 WebSocket Hub——几百个设备长连接，每个连接都要鉴权。如果用 Session，每个 WS 握手都要查 Redis，延迟会累积。JWT 验签不产生任何 I/O，微秒级完成。

### 三、JWT 是什么——三部分拼接的字符串

```
eyJhbGciOiJIUzI1NiJ9.eyJ1c2VyX2lkIjoxLCJleHAiOjE3NDc4NTI4MDB9.SvuRxKdq...
    ↑                        ↑                             ↑
  Header                   Payload                       Signature
  {"alg":"HS256"}    {"user_id":1,"exp":...}       HMAC-SHA256(Header.Payload, secret)
```

**三段都是 Base64 编码**——不是加密！Base64 只是把二进制字节转成 64 个可打印字符，任何人都能解码看内容。你把 Payload 那段 Base64 解码，直接就能看到 `{"user_id": 1}`。

**安全性不靠"看不见"，靠"改不了"**——第三段签名保证了前两段没有被篡改。签名的正确性依赖于只有服务器持有的 secret。

### 四、secret 本质——一把服务器独有的钥匙

`secret` 是一个字节数组 `[]byte`，从配置文件的 `JWT_SECRET` 环境变量读进来：

```go
var secret []byte  // = []byte("dev-secret-change-in-production")
func Init(secretKey string) { secret = []byte(secretKey) }
```

这把钥匙永远不离开服务器。它不出现在 token 里，不出现在 HTTP 响应里，客户端完全不知道。Token 里没有 secret——签名是 secret 的"作用结果"，不是 secret 本身。

HS256（HMAC-SHA256）是对称密钥算法——加密和验证用同一把钥匙。为什么不用 RS256（非对称）？因为我们的 Go Server 是单体——签发者和验证者是同一个进程，不需要公私钥分离。

### 五、签名 ≠ 加密——这是最容易混淆的地方

**加密**：把内容变乱码，只有有密钥的人能解开看。

```
原文:  "user_id=1"
加密:  "a7f3c9e1b4..."  → 谁也看不懂
解密:  "user_id=1"      → 用密钥变回来
```

**签名**：原文大家都能看，附加一段"防伪标识"。任何人能验证这个防伪标识是真的，但只有有密钥的人能生成它。

```
原文:   "user_id=1"           → 任何人都能读
签名:   "d8a2f1..."           → 附在原文后面
验证:   原文 + 签名 + secret → ✓ 没被改过 / ✗ 被篡改了
```

**现实类比**：你给朋友写一封信（Payload），信的内容谁都可以看。你用特殊印章（secret）在封蜡上盖戳（签名）。朋友收到信，看到封蜡的戳印，确认是你的印章图案——信没被拆、没被换过。如果有人拆了信换了内容，他的印章图案和你不一样——朋友一眼看出"这个戳不对"。

### 六、`token.SignedString(secret)` 内部四步

这是 `jwt.go` 里 Generate 函数最后调的那行——真正产生签名、拼接最终 token 字符串。

```go
token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
return token.SignedString(secret)
```

`NewWithClaims` 创建的是一个**未签名的 Token 对象**——只是一个数据容器，打包了 Header + Payload + 算法信息。此时签名是空的、Valid 是 false。

`SignedString(secret)` 做四件事：

**第一步——序列化 Header 并 Base64 编码**：

```
{"alg":"HS256","typ":"JWT"}  →  json.Marshal  →  Base64  →  "eyJhbGciOiJIUzI1NiJ9"
```

**第二步——序列化 Payload(Claims) 并 Base64 编码**：

```
{"user_id":1,"exp":1747852800,"iat":1747851900}  →  json.Marshal  →  Base64  →  "eyJ1c2Vy..."
```

这里用 `base64.RawURLEncoding` 而不是 `StdEncoding`——JWT 标准规定用 URL 安全的 Base64（`-` `_` 替代 `+` `/`，末尾不加 `=`），因为 token 要放在 HTTP header 里，这些字符在 URL 和 header 中有特殊含义。

**第三步——计算 HMAC-SHA256 签名**：

```
signingInput = headerB64 + "." + claimsB64
signature = HMAC-SHA256(signingInput, secret)   →  32 字节二进制
sigB64 = base64(signature)
```

**第四步——拼接最终 token**：

```
headerB64 + "." + claimsB64 + "." + sigB64
```

JWT 库把 30 行逻辑（序列化、Base64、HMAC 计算、拼接）封装成了一个方法调用。

### 七、HMAC-SHA256 原理——secret 怎么参与签名

HMAC = Hash-based Message Authentication Code（基于哈希的消息认证码）。

公式：
```
HMAC-SHA256(message, secret) = SHA256(
    (secret XOR opad) || SHA256((secret XOR ipad) || message)
)
```

人话翻译：

1. 密钥不够 64 字节 → 用 0 补到 64 字节
2. 用两个固定魔数搅拌密钥：`inner_key = secret XOR 0x3636...`，`outer_key = secret XOR 0x5C5C...`
3. 内层哈希：`SHA256(inner_key + Payload)` → 32 字节
4. 外层哈希：`SHA256(outer_key + 内层结果)` → 32 字节最终签名

**为什么是两层**：防止 SHA-256 的长度扩展攻击（length extension attack）。裸 `SHA256(key+message)` 可以被攻击者在不知道 key 的情况下追加数据并算出有效哈希。HMAC 的双层结构阻断了这个攻击面。

**secret 参与了两层**——每层都有被不同魔数搅拌的密钥混入。攻击者没有 secret，就凑不出正确的 32 字节签名。

### 八、解析验证到底验证了什么——三个问题

```go
func ParseToken(tokenString string) (*Claims, error) {
    // 1. 拆三段
    parts := strings.Split(tokenString, ".")
    headerB64, payloadB64, sigB64 := parts[0], parts[1], parts[2]

    // 2. 用同样的 secret 重新算签名
    我自己算的签名 := HMAC-SHA256(headerB64 + "." + payloadB64, secret)

    // 3. 比对——逐字节比较
    if 我自己算的签名 != base64Decode(sigB64) {
        return nil, error("签名不匹配——Payload 被改过")   // 验证失败①
    }

    // 4. 检查过期时间
    if claims.ExpiresAt < time.Now() {
        return nil, error("Token 已过期")                  // 验证失败②
    }

    return claims, nil  // 全部通过
}
```

验证回答三个问题：

| 步骤 | 问题 | 机制 |
|---|---|---|
| 签名比对 | "这个 token 是我们签发的吗？Payload 被改过吗？" | 只有持有 secret 的人才能产生匹配的签名。Payload 改一个字节 → 签名必然对不上 |
| exp 检查 | "token 还在有效期内吗？" | 现在时间 > exp → 过期 |

验证**不回答**的问题：
- "签发之后这个 token 有没有被撤销？"——不知道（JWT 无状态，不查数据库）
- "签发者是谁？"——知道但我们没设 `iss` 字段

### 九、双 Token 设计——解决"安全 vs 体验"的矛盾

如果只有一个 token：设短了用户频繁登录，设长了泄露后危害大。两个 token 把矛盾拆成两个维度：

| | Access Token | Refresh Token |
|---|---|---|
| 有效期 | **15 分钟** | **7 天** |
| 用途 | 每次 API 请求 | 只用来换新的 Access Token |
| 传输频率 | 每请求一次 | 15 分钟甚至更久才用一次 |
| 被盗后果 | 15 分钟后自动失效 | 做 Token Rotation 可被动检测 |

**Token 生命周期**：

```
00:00  登录 → 拿 Access(A) + Refresh(R)
00:10  正常用 A 调 API
00:16  A 过期 → 用 R 换新的 A' + R'（R 同时刷新，旧 R 失效）
6 天后  如果一直活跃，R 被不断刷新，实际有效期可远超 7 天
7 天后  如果 7 天没登录，R 也过期 → 重新输入密码
```

**Token Rotation（旋转）**：刷新 Access 时同时换新的 Refresh Token。如果 Refresh Token 被窃取，你和攻击者同时用它刷新——先到服务器的拿新 token，另一个人的旧 token 就废了。你下次用旧 token 刷新时失败 → 知道自己被攻击 → 重新登录。这是一种"被动安全探测"机制。

### 十、jwt.go 的五个设计决策

| # | 决策点 | 选了 | 为什么 |
|---|---|---|---|
| 1 | 暴露什么接口 | 3 个函数 + 1 个 Init | 最小暴露——调用方只需要 Generate 和 Parse |
| 2 | 密钥怎么管理 | 包级变量 + Init | 学习项目够用，调用方不感知密钥细节 |
| 3 | Claims 里存什么 | userID + exp + iat | 最少够用——handler 拿到 userID 后自己查库 |
| 4 | 算法 | HS256（对称） | 单体服务，无需公私钥分离 |
| 5 | 有效期放在哪 | 包内常量，两个独立函数 | 策略内聚，调用方不需要知道"几分钟过期" |

### 十一、jwt_test.go——单元测试验证完整回路

```go
func TestGenerateAndParse(t *testing.T) {
    Init("test-secret")
    token, _ := GenerateAccessToken(42)     // 生成
    claims, _ := ParseToken(token)           // 验证
    // claims.UserID == 42 → 生成和验证是同一把 secret → 回路通
}

func TestTamperedToken(t *testing.T) {
    Init("test-secret")
    token, _ := GenerateAccessToken(1)
    tampered := token[:len(token)-1] + "X"  // 篡改最后一个字符
    _, err := ParseToken(tampered)
    // err != nil → 篡改被检测出来了
}
```

这两个测试覆盖了：正常生成+验证回路、篡改检测。

### 十二、今天踩的坑

1. **测试文件重复代码导致编译失败**：`jwt_test.go` 末尾多了一段 `if err == nil { ... }`，不在函数内部。Go 编译器报 `expected declaration, found 'if'`。删掉重复的 4 行解决。

2. **CRLF 警告**：Git 在 Windows/WSL 混合环境下反复警告 `LF will be replaced by CRLF`。这是 WSL 和 Windows 文件系统之间的换行符转换问题——Git 的 `core.autocrlf` 配置项。对代码行为无影响，暂时忽略。

### 十三、当前进度与测试结果

Day 5 实际完成的文件：

| 文件 | 操作 | 作用 |
|---|---|---|
| `pkg/jwtutil/jwt.go` | 新建 | JWT 生成/解析核心——GenerateAccessToken / GenerateRefreshToken / ParseToken |
| `pkg/jwtutil/jwt_test.go` | 新建 | 单元测试——正常生成+解析回路 + 篡改检测 |
| `internal/config/config.go` | 修改 | Config struct 加 JWTSecret 字段 |
| `go.mod` / `go.sum` | 自动 | golang-jwt/jwt/v5 依赖 |

`go test ./pkg/jwtutil/` 两个用例全部通过：
- `TestGenerateAndParse`：生成 token → 解析 token → userID 一致 → ✓ 生成和验证回路正确
- `TestTamperedToken`：改 token 最后一个字符 → 验证失败 → ✓ 篡改被检测

下一步将把 jwtutil 接入 login handler（返回 token）并编写 Auth middleware 做请求鉴权。


---

## Day 5（续）：Middleware + JWT 接入 HTTP 层

### 一、中间件到底在做什么层面的事情

一个真实请求的完整调用栈：

```
Gin 收到 HTTP 请求
  → 路径匹配 /v1/users/me → handlers 数组 = [Auth中间件, GetProfile]
  → c.Next()
    → handlers[0](c)  ← Auth 中间件
        → c.GetHeader("Authorization") → token
        → jwtutil.ParseToken(token)     → claims
        → c.Set("userID", claims.UserID)
        → c.Next()          ← 执行权交给下一个
            → handlers[1](c)  ← handler.GetProfile
                → c.Get("userID") → 类型断言 → 查库 → 返回 JSON
            ← GetProfile 返回
        ← 中间件的 c.Next() 返回
    ← Gin 的 c.Next() 返回
  → 响应发回客户端
```

中间件和 handler 是**同一个类型**——都是 `func(*gin.Context)`。区别只在行为：

| | handler | 中间件 |
|---|---|---|
| 做什么 | 处理业务，返回响应 | 前置检查，注入数据 |
| 调 `c.Next()` 吗 | 通常不调——它是终点 | **必须调**——把请求往下传 |
| 调 `c.Abort()` 吗 | 不调 | 校验失败时调——截断请求链 |

`c.Next()` 不是并发——是**同步调用**。中间件暂停自己，等下一个 handler 跑完，然后继续执行 `c.Next()` 后面的代码。

`c.Set("key", val)` / `c.Get("key")` 是 Gin context 内部的 `map[string]interface{}` KV 存储。中间件往里写，handler 往外读。这是中间件和 handler 之间传递数据的标准方式。

### 二、Auth 中间件做了什么

`internal/middleware/auth.go`——路由到 handler 之前的拦截器：

1. `c.GetHeader("Authorization")` —— 从 HTTP header 取 token
2. `strings.TrimPrefix(authHeader, "Bearer ")` —— 去掉 OAuth 标准前缀，拿到纯 token
3. `jwtutil.ParseToken(tokenString)` —— 验签 + 检查过期
4. `c.Set("userID", claims.UserID)` —— 注入身份，供 handler 使用
5. `c.Next()` —— 放行给下一个 handler

任一环节失败 → `c.Abort()` 截断请求链，返回 4010/4011。

**对应 C++**：你的 WebServer 里鉴权是写在每个 handler 开头的一段重复代码——`if (!check_auth(fd)) return send_401()`。Go 的中间件把这段重复检查抽到了一个集中位置。

### 三、Login handler 改造——返回 JWT 双 token

之前的 Login 直接返回 user 信息。改造后在 service.Login 验证通过后：

1. `jwtutil.GenerateAccessToken(user.ID)` → 15 分钟有效期
2. `jwtutil.GenerateRefreshToken(user.ID)` → 7 天有效期
3. 响应里加 `expires_in: 900` —— 供前端做倒计时，提前静默刷新

为什么 token 生成在 handler 层而不是 service 层：token 是 HTTP 协议的概念（Bearer 前缀、OAuth 规范），不是业务概念。以后做 CLI 或 gRPC 时换一种鉴权方式，service 不用动。

### 四、RefreshToken——Token Rotation 被动安全

`POST /v1/auth/refresh` 接收 refresh_token，验证通过后签发**新的 Access + 新的 Refresh**，旧的 Refresh 同时失效。

这就是 Token Rotation：每次刷新都换掉 Refresh Token。如果 Refresh Token 被窃取，你和攻击者同时用它刷新——先到服务器的拿新 token，另一个人的旧 token 就废了。你下次用旧 token 刷新失败 → 知道你被攻击 → 重新登录。

Refresh 端点不需要 Auth 中间件——用户正是因为没有有效的 Access Token 才来刷的。

### 五、GetProfile——受保护端点

`GET /v1/users/me` 在 Auth 中间件之后执行：

1. `c.Get("userID")` → 取中间件注入的身份
2. 类型断言 `raw.(uint64)` → `interface{}` 转回具体类型
3. `repository.GetUserByID(userID)` → 查库
4. 返回 phone/email/nickname——**不返回 password**

`c.Get` 返回 `(interface{}, bool)`——第二个返回值 `exists` 告诉 key 在不在。这是防御性编程：理论上中间件保证了这一步一定拿到 userID，但如果路由配漏了没加中间件，`exists` 为 false 能做兜底。

### 六、main.go 路由分组设计

```go
// 公开路由——不经过 Auth 中间件
r.GET("/v1/health", handler.Ping)
r.POST("/v1/auth/register", handler.Register)
r.POST("/v1/auth/login", handler.Login)
r.POST("/v1/auth/refresh", handler.RefreshToken)

// 受保护路由组——经过 Auth 中间件
auth := r.Group("/v1")
auth.Use(middleware.Auth())
{
    auth.GET("/users/me", handler.GetProfile)
}
```

`r.Group("/v1")` 创建路由组，组内所有路由共享前缀和中间件。不在组里的路由不受影响。

### 七、今天踩的坑

1. **main.go import 路径缺 `my-iot-server`**：写成了 `"github.com/SinnerK9/internal/middleware"`，少了模块名中间那段。Go 的 import 必须从 go.mod 的 module 完整路径开始。

2. **`jwtutil.Init(cfg.JWTSecret)` 漏调**：没有 Init 的话 `jwtutil.secret` 是 nil（`[]byte` 零值），Login 调 `token.SignedString(nil)` 不会编译报错但运行时会炸——签名用的密钥是空的。

3. **GetUserByEmail 的 SELECT 漏 password 列（Day 4 遗留）**：邮箱登录时 `user.Password` 永远是空字符串，bcrypt 验证永远失败。修复后三个 GetUser 方法（ByPhone/ByEmail/ByID）的 SELECT 列名完全一致。

4. **参数错误提示冒号后少空格**：`"参数错误:"+err.Error()` 少了个空格，跟其他 handler 的风格不统一。

### 八、commit 记录（本段）

```bash
b66bec3 fix: GetUserByEmail 补充缺失的 password 列 + 新增 GetUserByID
bf4bbee docs: jwtutil 注释补全
7f722e8 feat: JWT 鉴权中间件——提取 Authorization header 并注入 userID
c084007 feat: Login 返回 Access+Refresh 双 token + RefreshToken 接口
007419b feat: GetProfile 受保护端点——从 context 取 userID 返回用户信息
d6ece87 feat: main.go 注入 JWT + middleware——启动时 Init + /v1 受保护路由组
```

---

## Week 1 Day 6 笔记：设备管理 CRUD + sqlx 事务

### 一、RESTful 设备 API 设计

6 个端点，URL 用名词（资源）、HTTP method 用动词（操作）：

| Method | Path | 含义 |
|---|---|---|
| GET | `/v1/devices` | 查当前用户所有设备 |
| POST | `/v1/devices` | 注册新设备 |
| GET | `/v1/devices/:device_id` | 查单台设备详情 |
| PUT | `/v1/devices/:device_id` | 修改设备名称/房间 |
| POST | `/v1/devices/:device_id/bind` | 绑定设备（事务） |
| DELETE | `/v1/devices/:device_id` | 解绑设备 |

**RESTful vs RPC**：RPC 风格 URL 里塞动词（`/api/doCreateDevice`），RESTful 只用名词——操作类型交给 HTTP method 表达。`GET` = 读，`POST` = 新增，`PUT` = 改，`DELETE` = 删。

**Gin 路径参数**：`:device_id` 在路由里是占位符，handler 里用 `c.Param("device_id")` 取值。Gin 内部用 radix tree 做路由匹配。

### 二、sqlx 事务——Day 6 核心

**事务解决什么问题**：两个用户同时绑定同一台设备，如果不用事务——

```
用户A 查 → owner=0 ✓          用户B 查 → owner=0 ✓（A 还没写入）
用户A UPDATE owner=A           用户B UPDATE owner=B（覆盖了A）
```

两个人都"绑定成功"，但只有 B 真正持有——A 的绑定悄无声息被覆盖。

**事务 + FOR UPDATE 怎么解决**：

```go
tx, err := DB.Beginx()          // BEGIN——事务开始
defer tx.Rollback()             // RAII 兜底——不管怎么退出，事务一定关闭

// SELECT ... FOR UPDATE：行级排他锁
// 第一个事务锁住这行，第二个事务在这里阻塞等待
err = tx.Get(&device, `SELECT * FROM devices WHERE device_id=? FOR UPDATE`, deviceID)

if device.OwnerID != 0 {        // 第二个事务醒来后读到 owner 已不为 0
    return 错误                  // → defer Rollback 执行
}

// 更新归属——仍在事务内
_, err = tx.Exec(`UPDATE devices SET owner_id=?, bound_at=?, status='online' WHERE device_id=?`,
    ownerID, now, deviceID)

err = tx.Commit()               // COMMIT——释放锁，改动永久生效
```

**`defer tx.Rollback()` 模式**：Commit 成功后 Rollback 返回 `ErrTxDone`（已提交的事务不能回滚），自动忽略；中间 `return err` 时 Rollback 真正执行，撤销所有未提交改动。这对应 C++ 的 RAII——构造拿锁，析构放锁。

**FOR UPDATE**：MySQL 行级排他锁（X Lock）。锁在 COMMIT/ROLLBACK 时自动释放。跨进程有效——这是分布式环境下的"mutex"。

对应 C++：`std::lock_guard<std::mutex>` 保护进程内内存，FOR UPDATE 保护数据库行。

### 三、归属校验——每一层各司其职

三层架构里归属校验放在 **service 层**：

```
handler  → 解析请求（c.Param / c.ShouldBindJSON / getUserID）
service → 校验"设备是否属于当前用户"——业务规则
repo    → 纯 SQL——不关心"谁有权"
```

**为什么不在 handler 里校验**：如果 CLI 工具绕过 HTTP 直接调 repository，handler 层的校验就被跳过了。service 层是所有入口的必经之路。

**GetDevice/UpdateDevice/UnbindDevice**：查设备 → `device.OwnerID != userID` → 拒绝。

**BindDevice 不同**：不是"检查属于我"，而是"检查没有别人绑定"：
```go
if device.OwnerID != 0 && device.OwnerID != userID {
    return errors.New("设备已被他人绑定")
}
```

### 四、getUserID 辅助函数——DRY 原则

Day 5 的 `GetProfile` 里把类型断言逻辑内联了。Day 6 有 6 个 handler 都要取 userID——

```go
func getUserID(c *gin.Context) (uint64, bool) {
    raw, exists := c.Get("userID")
    if !exists {
        model.Fail(c, 4010, "未登录")
        return 0, false
    }
    userID, ok := raw.(uint64)
    if !ok {
        slog.Error("userID 类型断言失败", "raw", raw)
        model.Fail(c, 5000, "服务器内部错误")
        return 0, false
    }
    return userID, true
}
```

返回 `(uint64, bool)` 模式：调用方 `userID, ok := getUserID(c); if !ok { return }`。bool 为 false 时辅助函数已经写了 Fail 响应，handler 只需 return。

**代码重复三次以上就应该抽函数**——这是 DRY（Don't Repeat Yourself）。

### 五、本日踩的坑

1. **INSERT 漏了 owner_id 字段**：`CreateDevice` 的 NamedExec 只写了 device_id/type/name/room/status 五个字段，漏了 owner_id。service 层设置了 `device.OwnerID = userID`，但 SQL 根本没插入这个值——创建后查列表为空。修复：INSERT 语句补上 `owner_id` 和 `:owner_id`。

2. **BindDevice 归属检查逻辑反了**：从 GetDevice/UpdateDevice 直接复制了 `if device.OwnerID != userID`，但 BindDevice 的场景是"设备还没主人，我要做主人"——OwnerID=0 时 `0 != 5` 永远为真，每次绑定都被拒。修复：`if device.OwnerID != 0 && device.OwnerID != userID`。

3. **handler 返回 error 类型**：`func ListDevices(c *gin.Context) error`——Gin handler 签名是 `func(*gin.Context)`，没有返回值。加 `error` 会导致编译失败。

4. **`:=` vs `=` 混用**：同一个作用域里用 `:=` 声明了 `err`，后面再用 `err :=` 会编译报错——`:=` 要求至少有一个新变量。修复：后续用 `err =` 赋值。

5. **`tx.Commit()` 返回单值却用了 `_, err =`**：Commit 只返回 `error`，不能赋给两个变量。`Exec` 返回 `(Result, error)` 才需要用 `_` 丢弃 Result。

6. **UPDATE 后漏了 err 检查**：`tx.Exec` 之后直接 `tx.Commit`——如果 UPDATE 失败（数据库错误），会静默提交一个没有改动的空事务。修复：UPDATE 后加 `if err != nil { return }`。

---

## Week 2 Day 1 笔记：WebSocket Hub 架构设计

### 一、前置知识：Socket → HTTP → WebSocket

#### Socket——传输层字节管道

Socket 是操作系统提供的传输层接口，只负责在网络上建立一个双向字节流管道：

```cpp
// C++：你的 WebServer 在操作的就是 Socket
socket() → bind() → listen() → accept() → read()/write()
```

Socket 不关心你传什么内容——HTTP 请求、你自己拼的二进制协议、还是 WebSocket 帧——Socket 只管把字节从 A 搬到 B。

#### HTTP REST——建立在 Socket 上的"一问一答"协议

HTTP 是在 Socket 上传送的数据格式。把 Socket 理解为电话线，HTTP 就是电话接通后说的那套"你好/请给我/再见"的对话规则。

**REST 模式的特征**：请求-响应（Request-Response），**客户端必须先问**，服务器不能主动说话。

```
客户端                         服务器
  │                              │
  │ ──── GET /devices ────────→ │  客户端先说话
  │ ←─── 200 {devices:[...]} ── │  服务器回应
  │                              │
  │  ← 没有请求时，连接沉默或关闭 →
```

#### WebSocket——同一个 Socket 上的"全双工聊天"协议

WebSocket 复用同一个 TCP Socket，在 HTTP 握手后切换协议：

```
客户端                             服务器
  │ ─── GET /ws HTTP/1.1 ────────→ │  普通 HTTP 请求
  │     Upgrade: websocket           │  但带了这句话
  │ ←── 101 Switching Protocols ── │  服务器同意升级
  ═══════ HTTP 到此结束，以下都是 WebSocket 帧 ═══════
  │ ─── {"type":"turn_on"} ───────→ │  客户端随时发
  │ ←── {"temp":26} ────────────── │  服务器也能主动推！
```

**核心规则变化**：

| | HTTP REST | WebSocket |
|---|---|---|
| 通信模式 | 半双工问答 | 全双工消息 |
| 谁先说话 | 必须客户端先 | 都可以 |
| 连接生命周期 | 一次请求/响应 → 关闭 | 持久连接 |
| 消息格式 | HTTP header + body（几百字节） | 轻量帧（2-6 字节头） |
| 服务器推送 | 做不到 | 这是核心用途 |

### 二、Goroutine——Go 的并发执行单元

**C++ 对照**：

```cpp
// C++：开线程跑 epoll 主循环
std::thread t([&] {
    while (is_running_) {
        epoller_.wait(-1);  // 这件事一直在跑
    }
});
t.join();
```

```go
// Go：开 goroutine——语法几乎一样，但成本完全不是一个量级
go func() {
    for {
        // 这件事一直在跑
    }
}()
```

| | C++ `std::thread` | Go goroutine |
|---|---|---|
| 创建成本 | ~1MB 栈 + 内核调度 | ~2KB 栈 + 用户态调度 |
| 能开多少个 | 8 核 → 8 个最佳 | 几万个没问题 |
| 切换代价 | 内核态切换，昂贵 | 用户态切换，廉价 |

**关键理解**：你 C++ 里写了 `ThreadPool` 来限制线程数——因为线程太贵了不能开太多。Go 不需要 ThreadPool，goroutine 便宜到可以**每个 WebSocket 连接开两个 goroutine 伺候**（一个读、一个写）。

### 三、Channel——goroutine 之间传消息的管道

两个 goroutine 怎么通信？C++ 里需要 **mutex + queue + condition_variable** 三个东西配合：

```cpp
// C++：线程间通信——共享内存 + 锁
std::queue<Task> task_queue_;
std::mutex mtx_;
std::condition_variable cv_;

// 生产者：
{ std::lock_guard<std::mutex> lock(mtx_); task_queue_.push(task); }
cv_.notify_one();

// 消费者：
std::unique_lock<std::mutex> lock(mtx_);
cv_.wait(lock, [] { return !task_queue_.empty(); });
Task t = task_queue_.front(); task_queue_.pop();
```

上面这一大坨，Go 里就是一个 channel：

```go
ch := make(chan int)  // 创建 channel

ch <- 42   // 生产者：把 42 扔进管道
x := <-ch  // 消费者：从管道取出 42
```

**Channel 就是一个自带锁的线程安全队列**。100 个 goroutine 同时往里扔，内部已经加好锁了。

**无缓冲 vs 有缓冲**：

```go
// 无缓冲 make(chan int)：发送方必须等接收方准备好
ch := make(chan int)
go func() { ch <- 1 }()  // 卡住，直到有人 <-ch

// 有缓冲 make(chan int, 256)：可以先塞 256 个再卡住
ch := make(chan int, 256)
ch <- 1  // 不卡——缓冲还有空位
ch <- 2  // 不卡
// ... 塞满 256 个后，第 257 个卡住
```

**两条核心规则**：
- 发送方阻塞：如果管道满了，`ch <- x` 会卡住直到有人取走
- 接收方阻塞：如果管道空了，`<-ch` 会卡住直到有人放进来

### 四、Hub 模式——WebSocket 版的 Epoller

**你的 C++ WebServer 架构**：有一个"主控室"——while 循环——盯着所有 fd，有事情发生了就分派处理。

```cpp
while (is_running_) {
    int n = epoller_.wait(-1);   // ← 主循环：等事件
    for (int i = 0; i < n; i++) {
        int fd = epoller_.get_fd(i);
        if (fd == listen_fd_)    handle_listen_();  // 新连接
        else if (EPOLLIN)        handle_read_(fd);  // 有数据
        else if (EPOLLOUT)       handle_write_(fd); // 能写了
        else if (EPOLLRDHUP)     handle_close_(fd); // 断了
    }
}
```

**Go Hub 是同样的思想，但工具变了**：

| C++ 工具 | Go 工具 |
|---|---|
| epoll（事件通知） | channel（消息通知） |
| ThreadPool（任务分发） | goroutine（任务执行） |
| 锁（保护共享数据） | 不需锁（channel 自带安全） |

**Hub 结构体的翻译**：

```go
type Hub struct {
    clients    map[uint64]*Client  // 对应 users_[MAX_FD]
    register   chan *Client        // "有新连接来了"通道
    unregister chan *Client        // "有连接断了"通道
}
```

**主循环对照**：

| C++ Epoller 主循环 | Go Hub.run() |
|---|---|
| `epoller_.wait(-1)` 阻塞等事件 | `select { case <-ch: }` 阻塞等 channel 消息 |
| 新 fd → `users_[fd] = newConn` | `<-h.register` → `clients[id] = c` |
| fd 断开 → `close(fd)` + 清理 | `<-h.unregister` → `delete(clients)` + `close(send)` |
| `std::thread([&]{ reactor }).detach()` | `go h.run()` |

**为什么用 channel 而不是直接加锁写 map**：

Go 的经典模式：**"Share memory by communicating"**（通过通信来共享内存，而不是通过共享内存来通信）。

Hub.run() 是**唯一**读写 clients map 的 goroutine——没有竞态条件，根本不需要锁。所有对 map 的操作通过 channel 发给 run()，run() 串行处理。

### 五、Client 结构体——每个连接的状态

```go
type Client struct {
    Hub    *Hub           // 所属 Hub——广播时需要
    Conn   *websocket.Conn // 底层 WebSocket 连接
    Send   chan []byte    // 缓冲 channel（256）——消息队列
    UserID uint64         // 这个连接属于谁
}
```

**Send chan 为什么存在——核心设计决策**：

gorilla/websocket 的 WriteMessage **不支持并发调用**。但多个 goroutine 可能都要给同一个客户端发消息：
- Hub 广播 → 需要写 Conn
- 心跳 ping → 需要写 Conn
- Handler 定向推送 → 需要写 Conn

**Send chan 的解决方案**：所有想给客户端发消息的人，**不直接写 Conn**，而是往 Send channel 里扔消息。**只有一个 goroutine（writePump）在消费 Send channel**——串行写入 Conn。

```
多个生产者                      一个消费者
Hub 广播 ──→ Send ←──writePump──→ Conn.WriteMessage()
心跳 ping ──→  ch   (唯一的写 goroutine)
定向推送 ──→      |
              buffered (256)
```

**C++ 对照**：你的 HttpConn 里的 `write_queue_`——epoll 在 fd 可写时从队列取数据写入——就是 Send chan 的等价位。

**为什么 Send 带缓冲（256）**：如果没有缓冲，外部 goroutine 发消息会阻塞等 writePump 取走。writePump 写网卡很慢（ms 级），这期间外部 goroutine 白白等着。有 256 个缓冲位，外部扔进去就走——这叫**削峰填谷**。

### 六、每个 Client 的两个 Goroutine

Go 的 WebSocket 库规定读写必须在不同 goroutine——ReadMessage() 是阻塞的，读写同 goroutine 会互相卡住。

```
Client 连上了
    │
    ├── go readPump()   负责：conn.ReadMessage() 循环读
    │                    收到消息 → Hub 广播
    │
    └── go writePump()  负责：从 Send chan 取消息 → conn.WriteMessage()
                         多个地方都往 Send chan 扔，writePump 串行写出
```

| Goroutine | C++ 对照 | 职责 |
|---|---|---|
| readPump | `handle_read(fd)` | 循环读消息，心跳检测 |
| writePump | `handle_write(fd)` | 从 Send chan 取消息，串行写入连接 |

### 七、一个连接从生到死

```
1. 浏览器发起 WebSocket 连接
   ↓
2. Gin handler 调用 upgrader.Upgrade(w, r) → HTTP 升级成 WebSocket
   ↓
3. 创建 Client{ Hub, Conn, Send(make(chan []byte, 256)), UserID }
   ↓
4. go client.readPump()  +  go client.writePump()
   ↓
5. hub.Register <- client  →  Hub.run() 收到 → 写入 clients map
   ↓
6. 连接存续期：readPump 循环读，writePump 循环写，心跳保活
   ↓
7. 连接断开（超时/关标签页/网络断）：
   readPump 的 ReadMessage 返回 error
   → defer: hub.Unregister <- client
   → Hub.run(): delete(clients, id) + close(client.Send)
   → close(Send) 后 writePump 的 range Send 退出
   → writePump defer: conn.Close()
   → 两个 goroutine 都退出，连接完全清理
```

**关键安全点**：`close(client.Send)` 必须在 Hub.run() 的 unregister case 里执行、且已经从 map 删除之后。如果在 readPump 里直接 close，会和 writePump 的 `range Send` 产生竞态 panic。

### 八、本日踩的坑

1. **client.go 的 import 格式错误**：写了 `import{` 少了一个空格——Go 要求 `import (` 左括号前必须有空格。修复：`import (`。

2. **channel 方向和 make 语法混淆**：`Send chan[]byte` 写成了 `Send chan []byte` 中间缺少空格——`chan` 是类型前缀，后面跟元素类型。正确：`Send chan []byte`。

3. **对 Hub/Channel/Goroutine 的名词恐惧**：这三个概念其实都是 C++ 里已有概念的 Go 版本——Hub = Epoller 主循环，Channel = mutex+queue+cv，Goroutine = 轻量线程。不需要想得太复杂，本质上就是"事件循环 + 消息队列 + 工作线程"的另一种写法。

---

## Week 2 Day 2 笔记：Hub.Run() 事件循环 + Broadcast + SendToUser

### 一、Day 2 做了什么

Day 1 搭了 Hub + Client 的结构体骨架，`run()` 是空壳。Day 2 把 `run()` 填满——一个完整的三路事件循环，外加一个定向推送方法。

改动集中在 `internal/websocket/hub.go`：
- Hub struct 加 `Broadcast chan []byte`
- `run()` 填满：register / unregister / broadcast 三个 case
- 新增 `SendToUser()` —— 给指定用户发消息

### 二、run()——三路 select 事件循环

```go
func (h *Hub) run() {
    for {
        select {
        case client := <-h.Register:      // 事件1：新连接上线
        case client := <-h.Unregister:    // 事件2：连接断开
        case msg := <-h.Broadcast:        // 事件3：广播消息
        }
    }
}
```

**C++ 对照**：

```
Go:  for { select { case <-ch1: case <-ch2: case <-ch3: } }
C++: while(is_running_) {
         epoller_.wait(-1);
         for(i in n) dispatch(fd, events);  // EPOLLIN→读 / EPOLLOUT→写 / RDHUP→断
     }
```

都是单线程事件循环——一个 goroutine 串行处理所有事件。区别是你 Reactor 按 **fd + epoll event** 分发，Hub 按 **channel 类型**分发。

### 三、Register case——新连接上线

```go
case client := <-h.Register:
    h.Mu.Lock()
    h.Clients[client.UserID] = client
    h.Mu.Unlock()
```

没什么花活——加锁、写 map、解锁。`Lock()` 用排他锁因为写 map。

### 四、⭐ Unregister case——断开连接，三段安全保护

这是 Day 2 最重要的设计决策，我自己写的注释里重点标了：

```go
case client := <-h.Unregister:
    h.Mu.Lock()
    // 安全检查①：只判断 userID 不够，必须同时判断指针
    if existing, ok := h.Clients[client.UserID]; ok && existing == client {
        // 安全检查②：先 delete 再 close——顺序不能反
        delete(h.Clients, client.UserID)
        close(client.Send)
    }
    h.Mu.Unlock()
```

**安全检查①：`existing == client`——为什么只判断 userID 不够？**

同一个用户重新连接（刷新页面）时，新 Client 覆盖了 map 里的旧 Client。旧 Client 的 goroutine 随后注销——走到 unregister case 时，map 里的 `Clients[userID]` 已经指向**新** Client 了。

如果只判断 `ok`（key 存在），就会错误地把新 Client 从 map 删掉、close 掉新 Client 的 Send chan——新连接莫名其妙断了。

`existing == client` 做**指针比较**：注销的必须是 map 里当前存的那个对象。旧 Client 和新 Client 是不同的指针——`&Client{...} != &Client{...}`——即使 userID 相同。所以旧 Client 注销时会发现"map 里存的不是我了"，跳过删除，新连接不受影响。

**安全检查②：`delete(map)` 必须在 `close(channel)` 之前**

```
正确顺序：
  delete(h.Clients, userID)   // ① 先从 map 移除——broadcast 再遍历不到它了
  close(client.Send)           // ② 再关 channel——writePump 安全退出

错误顺序（如果在 readPump 里 close）：
  readPump goroutine: close(client.Send)     // 关 channel
  Hub.broadcast goroutine: client.Send <- msg // ← 同时发生！panic: send on closed channel
```

**规则**：`close(ch)` 必须由**唯一写入方**执行。Send chan 的唯一写入方是 `Hub.run()`（通过 broadcast 和 SendToUser）。在 unregister case 里，`delete` 先执行保证了没有后续写入，然后 `close` 安全执行。

### 五、⭐ Broadcast case——非阻塞发送 + 读写锁

```go
case msg := <-h.Broadcast:
    h.Mu.RLock()                        // 读锁——允许多个 broadcast 同时遍历
    for _, client := range h.Clients {
        select {
        case client.Send <- msg:         // 发送成功
        default:                         // ⭐ Send chan 满了——跳过，不阻塞
        }
    }
    h.Mu.RUnlock()
```

**`select default` 为什么不能省略？**

如果写成 `client.Send <- msg`（没有 select default），当某个客户端的 Send chan 缓冲满了（256 条积压），这条语句会**永久阻塞**。而它又持有 `h.Mu.RLock()`——Register 和 Unregister 都在等 `h.Mu.Lock()`——整个 Hub 死锁。

`select default` = "发不了就算了"——宁可丢一条广播消息给慢客户端，也不能拖死整个 Hub。

**RLock vs Lock**：

| 操作 | 锁 | 理由 |
|---|---|---|
| Register（写 map） | `Lock()` | 独占 |
| Unregister（写 map + close） | `Lock()` | 独占 |
| Broadcast（遍历读 map） | `RLock()` | 共享——多个 goroutine 可以同时读 |
| SendToUser（查一个 client） | `RLock()` | 共享 |

对应 C++：`shared_lock` vs `unique_lock`，原理完全一样。

### 六、SendToUser——定向推送

```go
func (h *Hub) SendToUser(userID uint64, msg []byte) {
    h.Mu.RLock()
    client, ok := h.Clients[userID]
    h.Mu.RUnlock()
    if ok {
        select {
        case client.Send <- msg:
        default:  // 同样非阻塞——不拖死调用方
        }
    }
}
```

和 Broadcast 的区别：Broadcast 遍历所有 Client，SendToUser 只找一个。Week 3 设备状态推送时用——设备状态变了，只通知设备 owner，不是全站广播。

锁的粒度很关键：`RLock` 只在查 map 时持有——查到 client 指针后立即 `RUnlock`，然后无锁状态下往 Send chan 发消息。如果发消息时还持着锁，慢客户端会连带拖住其他 SendToUser 调用者。

### 七、今天踩的坑

1. **SendToUser 漏了 receiver**：第一版写成了 `func SendToUser(userID uint64, msg []byte)` 没有 `(h *Hub)`。函数体内用了 `h.Mu.RLock()`——编译器报 `undefined: h`。Go 的方法必须声明它属于哪个类型。修复：`func (h *Hub) SendToUser(...)`。

### 八、Day 2 收尾：NewClient + WsHandler + main.go

**NewClient 构造函数**：

```go
func NewClient(hub *Hub, conn *websocket.Conn, userID uint64) *Client {
    return &Client{
        Hub:    hub,
        Conn:   conn,
        Send:   make(chan []byte, 256),
        UserID: userID,
    }
}
```

为什么写构造函数：Send chan 必须 `make` 初始化——channel 的零值是 nil，往 nil channel 发消息永久阻塞。让调用方手拼 `&Client{...}` 早晚有人漏掉 make。构造函数把这个细节封死。

**WsHandler——HTTP 升级为 WebSocket 的入口**：

```go
func WsHandler(hub *ws.Hub) gin.HandlerFunc {
    return func(c *gin.Context) {
        conn, _ := upgrader.Upgrade(c.Writer, c.Request, nil)
        userID, _ := getUserID(c)
        client := ws.NewClient(hub, conn, userID)
        hub.Register <- client
    }
}
```

**闭包模式**：外半层 `func WsHandler(hub)` 在 main.go 启动时调一次，返回的函数"记住"了 hub。内半层 `func(c *gin.Context)` 每次 WS 请求时 Gin 调一次，拿到这次请求的信封 c。

对应 C++：`auto handler = [hub](Request* c) { ... }`——lambda 捕获。

**main.go 路由**：`auth.GET("/ws", handler.WsHandler(hub))`。放在受保护路由组里——WS 升级前 Auth 中间件先验 JWT，通过后才走 WsHandler。

### 九、容器全景——系统里到底有几个"装东西的容器"

被搞晕的核心原因：多个容器在各层之间传递数据，搞不清谁存什么、谁认识谁。逐个拆开：

| 容器 | 层 | 生命周期 | 存什么 | userID 在上面吗 |
|---|---|---|---|---|
| `c *gin.Context` | HTTP 层 | 一次请求（微秒~毫秒） | Request/Writer + Keys map | **临时暂存**：`c.Keys["userID"]=1` |
| `*ws.Hub` | WebSocket 层 | 进程生命周期 | Clients map 让找所有在线用户 | **间接**：`Clients[1]` 的 key 就是 userID |
| `*ws.Client` | WebSocket 层 | 连接生命周期（秒~小时） | Conn 指针 + Send chan + UserID 字段 | **永久刻在 struct 里**：`Client.UserID=1` |
| `*websocket.Conn` | 传输层 | 同 Client | TCP socket + 读写缓冲 | 没有——它只管收发字节 |
| `model.User` | 数据层 | 用完即弃（微秒） | 数据库行映射 | **有**：`User.ID=1`（但这是数据库字段，和连接无关） |

**userID 的传递链**：

```
JWT Payload  →  Auth 中间件解析  →  c.Set("userID", 1)  →  Gin 信封暂存
                                              ↓
                                   WsHandler: c.Get("userID")
                                              ↓
                                   NewClient(hub, conn, 1)
                                              ↓
                                   Client.UserID = 1  ← 刻进 struct，信封销毁也不丢
                                              ↓
                                   Hub.Clients[1] = client  ← map 的 key
```

**各层之间的隔离**：

- Hub 不认识 Gin，不知道什么叫 HTTP 请求
- Gin 不认识 Hub，不知道什么叫 WebSocket 广播
- Client 是桥梁——同时持有 Hub 指针和 Conn 指针，连通两层
- model.User 孤立存在——不认识任何连接

**为什么需要双向引用**：Hub → Client（broadcast 遍历 map 找到人），Client → Hub（断开时 Unregister 发到 Hub 的 channel）。

### 十、Day 2 收尾踩的坑

1. **NewClient 漏了 `return` 关键字**：写成了 `func NewClient(...) *Client{ Hub: hub, ... }`，没有 `return &Client{...}`。Go 的 struct 字面量必须包在 return 语句里——它不会像 Rust 那样自动把最后一行当返回值。编译器报语法错误。

## W2D3 笔记——readPump + writePump：每个 Client 的两个 goroutine

### 一、Day 3 之前 vs 之后

Day 2 结束时：Client 创建好了、注册进 Hub 的 map 了。但没有人读 Conn、没有人写 Conn。Client 像一把空椅子塞在 map 里。

Day 3：给每个 Client 配上两个 goroutine：

```
┌──── Client ────┐
│ readPump       │  ← 阻塞在 ReadMessage，收到消息 → Broadcast
│ writePump      │  ← 阻塞在 select，从 Send 取消息 → WriteMessage
└────────────────┘
```

C++ 对照：你的 Reactor 里每个 fd 有 EPOLLIN 和 EPOLLOUT 两个事件。readPump = 处理 EPOLLIN 的 goroutine，writePump = 处理 EPOLLOUT 的 goroutine。区别：C++ 用 epoll_wait 同时等所有 fd，Go 让每个连接自己阻塞等——goroutine 阻塞不占 CPU 线程。

### 二、readPump——唯一的读者

```go
func (c *Client) ReadPump() {
    defer func() {
        c.Hub.Unregister <- c  // 通知 Hub 清理
        c.Conn.Close()
    }()

    c.Conn.SetReadDeadline(time.Now().Add(pongWait))  // 60s
    c.Conn.SetPongHandler(func(string) error {
        c.Conn.SetReadDeadline(time.Now().Add(pongWait))  // 续命
        return nil
    })

    for {
        _, msg, err := c.Conn.ReadMessage()  // 阻塞等
        if err != nil { break }              // → defer 清理
        c.Hub.Broadcast <- msg
    }
}
```

**SetReadDeadline 的作用**：不是 TCP keepalive，是 gorilla 应用层的超时。如果 60 秒内 ReadMessage 没读到任何东西（包括 Pong 帧），返回 timeout error。这防止客户端网络断开但不发 close frame 时连接永远不被回收。

**PongHandler 怎么续命**：writePump 每 54 秒发一次 Ping → 客户端浏览器自动回 Pong → gorilla 收到 Pong → 调 PongHandler → SetReadDeadline(now + 60s)。只要客户端活着，deadline 就被不断往后推。

**defer 里的 Unregister**：readPump 正常或异常退出 → defer 发 Unregister 给 Hub → Hub.run() 做 delete + close(Send)。**readPump 不自己 close(Send)**——这是 Hub 架构的铁律。

### 三、writePump——唯一的写者 + 心跳发生器

```go
func (c *Client) WritePump() {
    ticker := time.NewTicker(pingPeriod)  // 54s 定时
    defer func() {
        ticker.Stop()
        c.Conn.Close()
    }()

    for {
        select {
        case msg, ok := <-c.Send:
            if !ok {
                // Send channel 被关了 → Hub 通知我退出
                c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
                return
            }
            c.Conn.WriteMessage(websocket.TextMessage, msg)

        case <-ticker.C:
            c.Conn.WriteMessage(websocket.PingMessage, nil)
        }
    }
}
```

**为什么需要一个专门的写 goroutine**：gorilla/websocket 的 WriteMessage 不支持并发调用。但发消息的来源很多——Broadcast、SendToUser、心跳 Ping。这些"想写"的人不能直接调 WriteMessage，而是往 Send channel 里塞消息。writePump 是唯一消费 Send channel 的 goroutine，把所有并发写串行化。

这就是你的 C++ WebServer 里 write_queue_ + EPOLLOUT 模式的 Go 版本——**把并发写变成排队写**。

### 四、心跳周期——54 和 60 的关系

```
pingPeriod = (pongWait * 9) / 10 = 54s

时间线：
0s ──── 54s(Ping) ──── 60s(Deadline) ──── 108s(Ping) ──── 120s(Deadline)
         ↓                          ↓
    54s Ping 发出              如果 54s 的 Ping 没有 Pong 回来，
    → 浏览器回 Pong             6s 后（60s deadline 到期）→ 连接断开
    → PongHandler 续命到 114s  留 6s = 一次网络丢包 + 客户端处理时间
```

如果 `pingPeriod >= pongWait`，Ping 发出去之前 Deadline 已经到期——连接会被误杀。

### 五、完整生命周期全景

```
① 连接建立
   GET /v1/ws (带 JWT) → Auth 中间件 → c.Set("userID")
   → upgrader.Upgrade() → HTTP → WebSocket
   → NewClient(hub, conn, userID)
   → hub.Register <- client
   → go ReadPump() + go WritePump()

② 正常运行
   writePump: 每 54s 发 Ping → 浏览器回 Pong → PongHandler 续命
   readPump: ReadMessage 阻塞 → 收到消息 → Broadcast → Hub.run() 遍历分发

③ 断开（三种触发）
   A. 浏览器关闭 → ReadMessage 返回 close error → break
   B. 网络断开 → 54s Ping 无响应 → 60s Deadline 到期 → ReadMessage timeout → break
   C. 服务端主动踢 → Hub.Unregister <- client

④ 清理链
   readPump defer → Hub.Unregister <- client
   → Hub.run() unregister case → Lock → delete(map) → close(Send) → Unlock
   → writePump: range Send 读到关闭 → WriteMessage(CloseMessage) → return
   → 两个 goroutine 退出 → conn.Close() → 零泄漏
```

### 六、Send chan 到底谁在把控？

| 操作 | 谁做 | 在哪 |
|---|---|---|
| 创建 make | NewClient | ws_handler.go |
| 写消息 | Hub.run() 的 Broadcast case + SendToUser | hub.go |
| **关 close** | **Hub.run() Unregister case** | hub.go |
| 消费 | writePump 的 `range Send` | client.go |
| 退出检测 | writePump 的 `!ok` | client.go |

创建和关闭都是 Hub.run() 说了算。readPump 只负责发 Unregister 通知——**它不开不关** Send。这就是 "写入方关闭 channel" 的 Go 惯例——Hub.run() 是唯一的写入者（代表所有人写），所以只有它能关。

### 七、C++ ↔ Go 对照（new）

| C++ WebServer | Go Hub |
|---|---|
| Epoller 管理所有 fd，epoll_wait 阻塞等事件 | Hub.run() goroutine，select 阻塞等 channel |
| 每个 fd 有 EPOLLIN / EPOLLOUT | 每个 Client 有 readPump / writePump |
| SO_RCVTIMEO socket 选项防死连接 | SetReadDeadline + Ping/Pong 应用层心跳 |
| write_queue_ + mutex → EPOLLOUT 时 write() | Send chan → writePump WriteMessage() |
| ThreadPool 多个 worker → 都往 write_queue_ 推 | Hub.run() 一个 goroutine → 往 Send chan 推 |
| close(fd) + 设置 done_ 标志通知 worker | close(Send) → writePump !ok 退出 |

### 八、Day 3 踩坑

1. **gofmt 格式化**：`const(` 后面括号不空格、`type struct{` 大括号前空格——gofmt 全部帮你修好了。Go 的格式不是风格问题，是强制执行。

2. **`websocket.IsUnexpectedCloseError` 的参数**：传入 CloseGoingAway(1001) 和 CloseNormalClosure(1000)，表示"这两个是预期中的关闭，不要给我记 error 日志"。其他 code（如 1006 异常）会触发 Error 日志。

---

## W2D4 笔记——Hub 联调：浏览器链路 + 鉴权改造 + 连接覆盖 Bug 修复

### 一、Day 4 到底做了什么

Day 1-3 搭起了 Hub + readPump + writePump 的完整架构。Day 4 是联调日——写 mock.html 让浏览器连上 WS，验证广播、心跳、断开清理。但一连线就出了三个问题，每个都牵出一层架构理解。

### 二、问题一：浏览器 WebSocket 不支持自定义 Header

**现象**：mock.html 里 `new WebSocket("ws://localhost:7777/v1/ws?token=xxx")`，服务器收到后 Auth 中间件拦截，返回 4010 "未登录"。

**根因**：`new WebSocket()` 只接受 URL 字符串——没有 headers 参数。浏览器自动填 Upgrade/Connection/Sec-WebSocket-Key 等 WS 必需的 Header，但**不让你加自定义的**。`Authorization` header 不存在 → Auth 中间件 `c.GetHeader("Authorization")` 返回空 → `c.Abort()`。

这不是 WebSocket 协议的问题——协议本身允许自定义 Header。这是**浏览器 API 的限制**——`WebSocket` 构造函数的设计就没给你留传 headers 的口子。

**对比**：

```
fetch() — REST API：
  fetch(url, { headers: { Authorization: 'Bearer xxx' } })
  → 可以随便设 Header → token 在 Header 里

new WebSocket() — 浏览器 WS：
  new WebSocket("ws://host/ws?token=xxx")
  → 只能传 URL → token 只能在 URL 里
```

**修法**：WS 路由从 auth 组移出，WsHandler 自己从 URL 解析 token：

```go
// main.go — 移出 auth 组
r.GET("/v1/ws", handler.WsHandler(hub))  // 不是 auth.GET

// ws_handler.go — 自己验 JWT
token := c.Query("token")           // 从 URL 读
claims, _ := jwtutil.ParseToken(token)  // 自己验
userID = claims.UserID
```

**REST vs WS 鉴权路径对比**：

```
REST API：
  fetch → Authorization header → Auth 中间件(c.GetHeader) → c.Set("userID") → handler

WebSocket：
  new WebSocket(url?token=) → WsHandler(c.Query) → jwtutil.ParseToken → userID
```

两条路读完都是调同一个 `jwtutil.ParseToken()`——token 从哪来（Header 还是 URL）对解析函数来说完全一样。

### 三、问题二：upgrade 后 connection hijacked panic

**现象**：连接成功（浏览器 onopen 触发），但立刻断开（1006）。服务器日志：`Error #01: http: connection has been hijacked`。

**根因链路**：

```go
func WsHandler(hub *ws.Hub) gin.HandlerFunc {
    return func(c *gin.Context) {
        token := c.Query("token")          // ① 读到 token
        userID = 1                          // ② 解析成功

        conn, _ := upgrader.Upgrade(...)   // ③ TCP 被接管（hijack）
        // ↑ 此刻 c.Writer 底层连接已被 gorilla 拿走
        //   再往 c.Writer 写任何东西 → panic

        userID, ok := getUserID(c)          // ④ 问题在这里！
        // ↑ Auth 中间件没跑（路由在 auth 组外）
        //   c.Get("userID") 不存在 → ok=false
        if !ok {
            conn.Close()                    // ⑤ 关 WS 连接
            return                          // ⑥ 还给 Gin
        }
    }
}
```

⑥ `return` 之后：Gin 的 Logger 中间件想记录响应状态码 → 调 `c.Writer.WriteHeader()` → 底层连接已被接管 → Go 标准库检测到 → panic: http: connection has been hijacked → Gin Recovery 捕获 → 尝试写 500 错误 → 又往已接管的连接写 → 日志输出 "Error #01"。

**时间线**：

```
浏览器                                  服务器
  GET /v1/ws?token=eyJ...
  ─────────────────────────────────→  upgrader.Upgrade() ← TCP hijack
  ←── 101 Switching Protocols ────   WebSocket 升级完成
                                     getUserID(c) → false → return
                                     Gin Logger → c.Writer.WriteHeader()
                                     💥 connection hijacked
  ←── TCP 已断 ──────────────────   浏览器：刚连上就断了 → 1006
```

**修法**：删掉 upgrade 之后的 `getUserID(c)` 调用——userID 已从 URL 的 token 解析出来了，不需要再读 Gin context：

```go
conn, _ := upgrader.Upgrade(...)
// userID 已从 query string 的 token 解析出来，不需要再读 Gin context
//（Auth 中间件没走——WS 路由在 auth 组外面）
client := ws.NewClient(hub, conn, userID)  // 直接用上面解析好的 userID
```

**核心规则**：`upgrader.Upgrade()` 之后，不要再做任何会触发 `c.Writer` 写操作的事。包括：不要 return 非 101 的状态、不要让中间件有机会写响应头。

### 四、问题三：多标签页广播"时有时无"——map 覆盖 bug

**现象**：三个标签页连上，有时 A 发的消息 B 收不到，有时都能收到。广播不稳定。

**根因**：Hub.Clients 是 `map[uint64]*Client`，key 是 userID。三个标签页用同一个 token（同一个 userID），第二个连接覆盖第一个：

```
标签页A 连接: Clients[1] = clientA     ← A 在 map 里
标签页B 连接: Clients[1] = clientB     ← B 覆盖了 A！A 的 goroutine 还在跑但 map 里找不到了
标签页C 连接: Clients[1] = clientC     ← C 覆盖了 B！
```

Broadcast 遍历 map → 只找到 clientC → 只有最后连的那个标签页收到广播。A 和 B 的 goroutine 在跑但永远收不到消息——它们的 Client 对象还活着（没被 GC），但 map 里没有引用指向它们。

**这就是"时有时无"的原因**——取决于你发的消息时，你是 map 里那个唯一的 Client 还是被覆盖的那个。

**修法**：`map[uint64]*Client` → `map[uint64]map[*Client]bool`。每个 userID 下不再是一个 Client，而是一个 Client 集合（set）：

```go
// 改前
Clients map[uint64]*Client

// 改后
Clients map[uint64]map[*Client]bool  // userID → set of Clients
// map[*Client]bool 是 Go 里用 map 模拟 set 的惯用写法
// bool 值忽略，只用来 O(1) 删除
```

Register 时往集合里加，Unregister 时从集合里删（删完空集合后删 key），Broadcast 时嵌套遍历：

```go
// Register
if h.Clients[client.UserID] == nil {
    h.Clients[client.UserID] = make(map[*Client]bool)
}
h.Clients[client.UserID][client] = true

// Unregister
delete(h.Clients[client.UserID], client)
if len(h.Clients[client.UserID]) == 0 {
    delete(h.Clients, client.UserID)
}
close(client.Send)

// Broadcast
for _, set := range h.Clients {
    for client := range set {
        select {
        case client.Send <- msg:
        default:
        }
    }
}
```

**为什么用 `map[*Client]bool` 而不是 `[]*Client` slice**：slice 的删除是 O(n)——需要找到元素位置然后截断。set（map）的删除是 O(1)。WS 连接频繁上下线，O(1) 更合适。

### 五、联调成功的完整链路

修复三个问题后，重新测试：

```
① 浏览器打开 mock.html，粘贴 JWT token，点"连接 WS"
   → new WebSocket("ws://localhost:7777/v1/ws?token=eyJh...")
   → 浏览器发 GET /v1/ws?token=eyJh... + Upgrade/Connection 头
   → Go: r.GET("/v1/ws") 匹配 → WsHandler
   → c.Query("token") → "eyJh..." → ParseToken → userID=1
   → upgrader.Upgrade() → 101 Switching Protocols → TCP 升级
   → NewClient → hub.Register <- client → go ReadPump + WritePump

② 3 个标签页各自连上（userID=1 的 set 里有 3 个 Client）
   → 标签页A 输入 "hello" 点发送 → ws.send("hello")
   → 服务器 readPump: ReadMessage → "hello" → hub.Broadcast <- "hello"
   → Hub.run() broadcast case → 遍历 Clients[1] 的 set → 3 个 client
   → 每个 client.Send <- "hello" → writePump → WriteMessage → 3 个标签页都收到
   → ✅ 广播 OK

③ 关掉标签页B
   → 浏览器发 Close 帧 → readPump ReadMessage 返回 close error
   → break → defer: hub.Unregister <- clientB
   → Hub.run() unregister case → delete(set, clientB) → close(clientB.Send)
   → writePump: range Send 读到关闭 → WriteMessage(CloseMessage) → return
   → 服务器不 panic
   → ✅ 断开清理 OK

④ 标签页A 再发消息 → A 和 C 收到，B 收不到（已断开）
   → ✅ 注销后不再收到广播 OK
```

### 六、今天踩的坑

1. **WS 路由在 auth 组里 → 浏览器发不出 Authorization header → Auth 中间件拒绝**。修：路由移出 auth 组，WsHandler 自己从 URL 解析 token。

2. **upgrade 后又调 `getUserID(c)` 读 Gin context → Auth 没跑所以 key 不存在 → return → Gin 中间件往已接管连接写响应 → hijacked panic**。修：删掉 upgrade 后的 getUserID 调用，userID 已在前面从 token 解析好了。

3. **`map[uint64]*Client` 同 userID 多连接互相覆盖 → 广播只送到最后连的那个标签页**。修：改为 `map[uint64]map[*Client]bool` 集合，一个 userID 下存所有连接。

4. **main.go 里 `auth.GET("/v1/ws", ...)` 路径双写**：auth 组已经有 `/v1` 前缀，又写了 `/v1/ws` 实际路径变成 `/v1/v1/ws`。修：`r.GET("/v1/ws", ...)` 不经过 auth 组。

5. **60 秒心跳"不触发"**：不是 bug——浏览器标签页开着会自动回 Pong，心跳续上了所以不断。心跳断开只在**真正断网/关标签页**时才触发。测心跳断开的方法是直接关标签页，等几秒看服务器日志的 `client unregistered`。

### 七、commit 记录

```bash
544d613 fix: W2D4 Hub联调——WS鉴权链路修复 + Clients改为多连接set
```

四个文件改动：
- `cmd/server/main.go`：WS 路由从 auth 组移出，改为 `r.GET`
- `internal/handler/ws_handler.go`：不再依赖 Auth 中间件，从 query string 读 token 自验 JWT；删掉 upgrade 后重复的 `getUserID(c)` 调用
- `internal/websocket/hub.go`：`Clients` 从 `map[uint64]*Client` 改为 `map[uint64]map[*Client]bool`
- `mock.html`：新增浏览器联调测试页面