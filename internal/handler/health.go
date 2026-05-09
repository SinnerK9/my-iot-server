package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Ping 是我们的健康检查接口处理函数
func Ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "My proper layered server is running!",
	})
}
