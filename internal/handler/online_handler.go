package handler

import (
	"log/slog"

	"github.com/SinnerK9/my-iot-server/internal/model"
	"github.com/SinnerK9/my-iot-server/internal/repository"
	"github.com/gin-gonic/gin"
)

// GetOnlineUsers 返回当前在线用户列表（从 Redis 读）。
func GetOnlineUsers(c *gin.Context) {
	users, err := repository.GetOnlineUsers()
	if err != nil {
		slog.Error("GetOnlineUsers failed", "err", err)
		model.Fail(c, 5000, "服务器内部错误")
		return
	}
	model.OK(c, users)
}
