package handler

import (
	"log/slog"

	"github.com/SinnerK9/my-iot-server/internal/client"
	"github.com/SinnerK9/my-iot-server/internal/model"
	"github.com/gin-gonic/gin"
)

// ChatReq 聊天请求体
type ChatReq struct {
	Message string `json:"message" binding:"required"`
}

// ChatHandler 返回一个Gin handler——接收用户文本，调 LLM，返回意图。
// llm 从 main.go 注入,整个进程一个 LLM 客户端实例。
func ChatHandler(llm *client.LLMClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ChatReq
		if err := c.ShouldBindJSON(&req); err != nil {
			model.Fail(c, 4001, "参数错误: "+err.Error())
			return
		}

		llmText, err := llm.Chat(req.Message)
		if err != nil {
			slog.Error("llm chat failed", "err", err)
			// 不可用时不给用户报 500，返回固定文本
			model.Fail(c, 5001, "大模型服务不可用，请稍后重试")
			return
		}

		model.OK(c, gin.H{"llm_response": llmText})
	}
}

func ChatStreamHandler(llm *client.LLMClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ChatReq
		if err := c.ShouldBindJSON(&req); err != nil {
			model.Fail(c, 4001, "参数错误："+err.Error())
			return
		}

		c.Header("Content-type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")

		errCh := make(chan error, 1)

		go func() {
			err := llm.ChatStream(req.Message, func(delta string) error {
				c.SSEvent("delta", delta)
				c.Writer.Flush()
				return nil
			})
			errCh <- err
		}()

		err := <-errCh

		if err != nil {
			slog.Error("llm stream failed", "err", err)
			c.SSEvent("fallback", "好的，已收到您的指令，正在为您处理。")
			c.Writer.Flush()
		}
		c.SSEvent("done", "")
		c.Writer.Flush()
	}
}
