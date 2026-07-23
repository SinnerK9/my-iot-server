package middleware

import (
	"github.com/gin-gonic/gin"
)

// CORS 允许跨域请求——开发阶段必备，否则 file:// 打开的 HTML 和 mock 工具无法调 API。
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type,Authorization")
		c.Header("Access-Control-Max-Age", "86400") // 预检缓存 24 小时

		// 浏览器在跨域 POST/PUT 前会发 OPTIONS 预检请求——直接返回 204
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
