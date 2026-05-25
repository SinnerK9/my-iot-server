package handler

import (
	"log/slog"

	"github.com/SinnerK9/my-iot-server/internal/model"
	"github.com/SinnerK9/my-iot-server/internal/repository"
	"github.com/gin-gonic/gin"
)

// 返回当前登录用户的信息，在Auth中间件之后执行
func GetProfile(c *gin.Context) {
	//从Context中取出中间件注入的userID
	raw, exists := c.Get("userID") //get返回查询到的key和其是否存在
	if !exists {
		model.Fail(c, 4010, "未登录")
		return
	}

	userID, ok := raw.(uint64)
	if !ok {
		slog.Error("userID类型断言失败", "raw", raw)
		model.Fail(c, 5000, "服务器内部错误")
		return
	}

	user, err := repository.GetUserByID(userID)
	if err != nil {
		slog.Error("查库失败", "err", err, "userID", userID)
		model.Fail(c, 5000, "服务器内部错误")
		return
	}
	//不能返回密码！！
	model.OK(c, gin.H{
		"user_id":  user.ID,
		"phone":    user.Phone,
		"email":    user.Email,
		"nickname": user.Nickname,
	})
}
