package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/SinnerK9/my-iot-server/internal/repository"
	"github.com/gin-gonic/gin"
)

// Ping 基础健康检查——验证服务进程存活。
func Ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "My proper layered server is running!",
	})
}

// HealthCheck 返回各依赖的健康状态。
// 运维监控端点——返回具体哪个组件挂了，而非笼统的 500。
func HealthCheck(c *gin.Context) {
	status := gin.H{
		"server": "ok",
		"db":     "ok",
		"redis":  "ok",
	}

	// 检查 DB
	if err := repository.DB.Ping(); err != nil {
		status["db"] = "unreachable"
		slog.Warn("health check: db unreachable", "err", err)
	}

	// 检查 Redis
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := repository.RDB.Ping(ctx).Err(); err != nil {
		status["redis"] = "unreachable"
		slog.Warn("health check: redis unreachable", "err", err)
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": status})
}
