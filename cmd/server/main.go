package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// 结构化日志:new方法用JSON格式日志处理器创建一个Logger对象
	// 将这个Logger对象设置为全局默认日志器
	// 这个日志处理器将JSON输出到标准输出stdout，并且只输出info级别以上的日志
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))
	//监听本机8080
	addr := ":8080"

	//ServeMux是Go的路由管理器，其作用是根据请求路径找到对应的处理函数
	mux := http.NewServeMux()
	//前一个字符串指定路径
	//后面的匿名函数即为处理函数，w用于给客户端返回数据，r是客户端发来的请求
	mux.HandleFunc("v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json") //返回格式是JSON
		w.Write([]byte(`{"status":"ok"}`))
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,              //交给上面创造的路由mux处理请求
		ReadHeaderTimeout: 10 * time.Second, //十秒超时
	}

	//开启一个goroutine，防止阻塞的ListenAndServe影响后续关闭代码的执行
	go func() {
		slog.Info("Server starting", "addr", addr)
		//如果不是正常关闭的错误，打印日志并且退出
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()
	//用signal.NotifyContext来监听ctrl + c和SIGTERM这两个关闭信号
	//注意必须传入一个context.Background作为根通知器
	//返回一个context用于等待信号，一个stop函数用于触发停止
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	<-ctx.Done()                  //在这里阻塞住，等待关闭信号的到来
	slog.Info("shutting down...") //得到信号了，从这里开始关闭流程
	//一个新的停止通知器shutdownCtx，超过五秒就不等了，cancel和stop同理
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	//执行关闭服务器，并将是否出错存到err里，有错误则打印日志
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Shutdown error", "err", err)
	}
	slog.Info("bye")
}
