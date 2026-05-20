package handler

import (
	"log/slog"
	"strings"

	"github.com/SinnerK9/my-iot-server/internal/model"
	"github.com/SinnerK9/my-iot-server/internal/service"
	"github.com/gin-gonic/gin"
)

func isBusinessError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "已被注册") ||
		strings.Contains(msg, "密码错误") ||
		strings.Contains(msg, "账号或密码错误")
}

func Register(c *gin.Context) {
	var req model.RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		model.Fail(c, 4001, "参数错误: "+err.Error())
		return
	}
	id, err := service.Register(&req)
	if err != nil {
		if isBusinessError(err) {
			model.Fail(c, 4090, err.Error())
			return
		}
		slog.Error("Register failed", "err", err)
		model.Fail(c, 5000, "服务器内部错误")
		return
	}
	model.OK(c, gin.H{"user_id": id})
}

func Login(c *gin.Context) {
	var req model.LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		model.Fail(c, 4001, "参数错误: "+err.Error())
		return
	}
	user, err := service.Login(&req)
	if err != nil {
		if isBusinessError(err) {
			model.Fail(c, 4010, err.Error())
			return
		}
		slog.Error("Login failed", "err", err)
		model.Fail(c, 5000, "服务器内部错误")
		return
	}
	model.OK(c, gin.H{
		"user_id":  user.ID,
		"phone":    user.Phone,
		"email":    user.Email,
		"nickname": user.Nickname,
	})
}
