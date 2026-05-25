package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/SinnerK9/my-iot-server/internal/model"
	"github.com/SinnerK9/my-iot-server/pkg/jwtutil"
)

func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 拿 Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			model.Fail(c, 4010, "未登录")
			c.Abort()
			return
		}

		// 去掉 "Bearer " 前缀,即可得到纯token字符串
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			// TrimPrefix 什么都没去掉 → 没 Bearer 前缀
			model.Fail(c, 4010, "Token 格式错误")
			c.Abort()
			return
		}

		// 解析 + 验证 JWT
		claims, err := jwtutil.ParseToken(tokenString)
		if err != nil {
			model.Fail(c, 4011, "Token 过期或无效")
			c.Abort()
			return
		}

		// 注入 userID 到 context，供后续 handler 使用
		c.Set("userID", claims.UserID)

		// 放行给下一个 handler
		c.Next()
	}
}
