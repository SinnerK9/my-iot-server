package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SinnerK9/my-iot-server/internal/config"
	"github.com/SinnerK9/my-iot-server/internal/handler"
	"github.com/SinnerK9/my-iot-server/internal/middleware"
	"github.com/SinnerK9/my-iot-server/internal/repository"
	ws "github.com/SinnerK9/my-iot-server/internal/websocket"
	"github.com/SinnerK9/my-iot-server/pkg/jwtutil"
	"github.com/gin-gonic/gin"
)

func main() {
	// 结构化日志:new方法用JSON格式日志处理器创建一个Logger对象
	// 将这个Logger对象设置为全局默认日志器
	// 这个日志处理器将JSON输出到标准输出stdout，并且只输出info级别以上的日志
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	//加载配置 + 初始化数据库
	cfg := config.Load()
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true&loc=Local",
		cfg.DBUser, cfg.DBPass, cfg.DBHost, cfg.DBPort, cfg.DBName)
	if err := repository.InitDB(dsn); err != nil {
		slog.Error("init db failed", "err", err)
		os.Exit(1)
	}
	defer repository.CloseDB() //延后执行closedb

	// 初始化 JWT——必须在路由注册之前调用，否则 Login 生成 token 时 secret 是 nil
	jwtutil.Init(cfg.JWTSecret)

	//初始化HUB
	hub := ws.NewHub()
	hub.Start()
	r := gin.Default()
	//这部分为公开路由
	r.GET("/v1/health", handler.Ping)                // 健康检查，复用已有的
	r.POST("/v1/auth/register", handler.Register)    // 注册
	r.POST("/v1/auth/login", handler.Login)          // 登录
	r.POST("/v1/auth/refresh", handler.RefreshToken) //刷新token

	//受保护路由
	auth := r.Group("/v1")      //创立路由组，组里所有路由共享/v1前缀，并且共享use(auth)
	auth.Use(middleware.Auth()) //Use对前面已经注册的路由不起作用
	{
		auth.GET("/users/me", handler.GetProfile)
		auth.GET("/devices", handler.ListDevices)                 // 查设备列表
		auth.POST("/devices", handler.CreateDevice)               // 注册新设备
		auth.GET("/devices/:device_id", handler.GetDevice)        // 查单台设备
		auth.PUT("/devices/:device_id", handler.UpdateDevice)     // 修改设备信息
		auth.POST("/devices/:device_id/bind", handler.BindDevice) // 绑定设备（事务）
		auth.DELETE("/devices/:device_id", handler.UnbindDevice)  // 解绑设备
		auth.GET("/ws", handler.WsHandler(hub))                   //新增，升级前必须先验证JWT
	}
	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,                //Gin实现了Handler接口，直接传给server
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
