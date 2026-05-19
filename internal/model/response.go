package model

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 规定handler返回给客户端的JSON，用struct tag来指定每行内容转化为JSON的指定字段
type Response struct {
	Code int         `json:"code"` // 0=成功，非 0=具体错误码
	Msg  string      `json:"msg"`  // 人类可读的提示信息
	Data interface{} `json:"data"` // 可以是对象、数组、nil
}

// Gin框架的运用：用c.JSON可以跳过标准http库设content-type，Marshal（序列化）和write的操作，并且不必用responsewriter和request
// JSON:直接传入状态码和结构体即可！对象是gin.Context结构体，相当于一张白纸，里面包含了http类的w和r
func OK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{Code: 0, Msg: "ok", Data: data}) //直接传入状态码和结构体即可！
}

// 失败情况：请求处理是成功的，所以依旧是statusOK。
func Fail(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, Response{Code: code, Msg: msg, Data: nil})
}

func FailWithStatus(c *gin.Context, httpStatus int, code int, msg string) {
	c.JSON(httpStatus, Response{Code: code, Msg: msg, Data: nil})
}
