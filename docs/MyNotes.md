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