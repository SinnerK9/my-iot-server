package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	// 这里替换成你的模块名，比如 github.com/yourname/my-iot-server/internal/handler
	"github.com/SinnerK9/my-iot-server/internal/handler"
)

func main() {
	r := gin.Default()

	// 注册路由，把具体的逻辑甩给 handler 层去处理
	// 这就是分层！main.go 变得极其干净！
	r.GET("/v1/health", handler.Ping)

	srv := &http.Server{
		Addr:    ":8080",
		Handler: r,
	}

	go func() {
		log.Println("启动服务器...")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("收到关闭信号，准备优雅退出...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("强制关闭:", err)
	}
	log.Println("优雅退出完成")
}
