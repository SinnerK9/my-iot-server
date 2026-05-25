package handler

import (
	"log/slog"
	"strings"

	"github.com/SinnerK9/my-iot-server/internal/model"
	"github.com/SinnerK9/my-iot-server/internal/service"
	"github.com/SinnerK9/my-iot-server/pkg/jwtutil"
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
	//新增：返回双token
	accessToken, err := jwtutil.GenerateAccessToken(user.ID)
	if err != nil {
		slog.Error("GenerateAccessToken Failed", "err", err)
		model.Fail(c, 5000, "服务器内部错误")
		return
	}
	refreshToken, err := jwtutil.GenerateRefreshToken(user.ID)
	if err != nil {
		slog.Error("GenerateRefreshToken Failed", "err", err)
		model.Fail(c, 5000, "服务器内部错误")
		return
	}
	model.OK(c, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "Bearer",
		"expires_in":    900, // 15 分钟 = 900 秒，方便前端倒计时，在将要过期时自动刷新
		"user": gin.H{
			"user_id":  user.ID,
			"phone":    user.Phone,
			"email":    user.Email,
			"nickname": user.Nickname,
		},
	})
}

// 这个类只在这个函数里用一次，不需要定义到model包里
type RefreshTokenReq struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

func RefreshToken(c *gin.Context) {
	var req RefreshTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		model.Fail(c, 4001, "参数错误: "+err.Error())
		return
	}
	//验证refreshtoken
	claims, err := jwtutil.ParseToken(req.RefreshToken)
	if err != nil {
		model.Fail(c, 4011, "Token 过期或无效")
		return
	}

	//签发新的 Access Token并同时旋转 Refresh Token
	newAccess, err := jwtutil.GenerateAccessToken(claims.UserID)
	if err != nil {
		slog.Error("GenerateAccessToken failed", "err", err)
		model.Fail(c, 5000, "服务器内部错误")
		return
	}
	newRefresh, err := jwtutil.GenerateRefreshToken(claims.UserID)
	if err != nil {
		slog.Error("GenerateRefreshToken failed", "err", err)
		model.Fail(c, 5000, "服务器内部错误")
		return
	}

	model.OK(c, gin.H{
		"access_token":  newAccess,
		"refresh_token": newRefresh,
		"token_type":    "Bearer",
		"expires_in":    900,
	})
}
