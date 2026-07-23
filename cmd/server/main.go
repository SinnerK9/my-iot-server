package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SinnerK9/my-iot-server/internal/client"
	"github.com/SinnerK9/my-iot-server/internal/config"
	"github.com/SinnerK9/my-iot-server/internal/handler"
	"github.com/SinnerK9/my-iot-server/internal/middleware"
	"github.com/SinnerK9/my-iot-server/internal/repository"
	"github.com/SinnerK9/my-iot-server/internal/service"
	ws "github.com/SinnerK9/my-iot-server/internal/websocket"
	"github.com/SinnerK9/my-iot-server/pkg/jwtutil"
	"github.com/gin-gonic/gin"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// 加载配置 + 初始化数据库
	cfg := config.Load()
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true&loc=Local",
		cfg.DBUser, cfg.DBPass, cfg.DBHost, cfg.DBPort, cfg.DBName)
	if err := repository.InitDB(dsn); err != nil {
		slog.Error("init db failed", "err", err)
		os.Exit(1)
	}
	defer repository.CloseDB()

	if err := repository.InitRedis(cfg.RedisAddr); err != nil {
		slog.Error("init redis failed", "err", err)
		os.Exit(1)
	}
	defer repository.CloseRedis()

	jwtutil.Init(cfg.JWTSecret)

	// 初始化 Hub
	hub := ws.NewHub()
	hub.Start()

	// 初始化 LLM 客户端
	llmClient := client.NewLLMClient(cfg.LLMKey, cfg.LLMURL, cfg.LLMModel)

	// 初始化 MQTT 客户端
	mqttClient, err := client.NewMQTTClient(cfg.MQTTBroker, "go-server-"+randomID())
	if err != nil {
		slog.Error("init mqtt failed", "err", err)
		os.Exit(1)
	}
	defer mqttClient.Disconnect()

	// 订阅设备状态——回调里更新 Redis + Hub 广播
	mqttClient.SubscribeDeviceStatus(func(deviceID string, payload []byte) {
		slog.Info("device status", "deviceID", deviceID, "payload", string(payload))

		info := map[string]interface{}{"status": "online"}
		var status map[string]interface{}
		if json.Unmarshal(payload, &status) == nil {
			for k, v := range status {
				info[k] = v
			}
		}
		repository.SetDeviceOnline(deviceID, info)

		msg := fmt.Sprintf(`{"type":"device_status","device_id":"%s","data":%s}`, deviceID, string(payload))
		hub.Broadcast <- []byte(msg)
	})

	// 注入编排器——WebSocket 消息 → LLM → MQTT → Hub 广播
	orchestrator := service.NewChatOrchestrator(llmClient, mqttClient, hub)
	hub.OnMessage = orchestrator.HandleMessage

	r := gin.Default()
	r.Use(middleware.CORS()) // CORS——开发阶段必备，否则浏览器拦截跨域请求

	// 公开路由
	r.GET("/v1/health", handler.Ping)
	r.GET("/v1/health/deps", handler.HealthCheck) // 依赖健康检查：DB + Redis
	r.POST("/v1/auth/register", handler.Register)
	r.POST("/v1/auth/login", handler.Login)
	r.POST("/v1/auth/refresh", handler.RefreshToken)

	// 受保护路由
	auth := r.Group("/v1")
	auth.Use(middleware.Auth())
	{
		auth.GET("/users/me", handler.GetProfile)
		auth.GET("/devices", handler.ListDevices)
		auth.POST("/devices", handler.CreateDevice)
		auth.GET("/devices/:device_id", handler.GetDevice)
		auth.PUT("/devices/:device_id", handler.UpdateDevice)
		auth.POST("/devices/:device_id/bind", handler.BindDevice)
		auth.DELETE("/devices/:device_id", handler.UnbindDevice)
		auth.GET("/online/users", handler.GetOnlineUsers)
		auth.POST("/chat", handler.ChatHandler(llmClient))
		auth.POST("/chat/stream", handler.ChatStreamHandler(llmClient))
	}
	r.GET("/v1/ws", handler.WsHandler(hub))

	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("Server starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	slog.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Shutdown error", "err", err)
	}
	slog.Info("bye")
}

// randomID 生成 8 位随机十六进制字符串——确保 MQTT clientID 唯一。
func randomID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}
