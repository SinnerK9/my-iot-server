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