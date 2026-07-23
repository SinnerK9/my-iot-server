package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// 请求
type llmReq struct {
	Model    string   `json:"model"`
	Messages []llmMsg `json:"messages"`
	Stream   bool     `json:"stream"` // true=流式，false=非流式
}

type llmMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// 非流式响应
type llmResp struct {
	Choices []struct {
		Message llmMsg `json:"message"`
	} `json:"choices"`
}

// 流式响应（每个 chunk 的结构和非流式不一样）

type llmStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// 客户端

type LLMClient struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

func NewLLMClient(apiKey, baseURL, model string) *LLMClient {
	return &LLMClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		http: &http.Client{
			Timeout: 30 * time.Second, // 流式可能持续较久，放宽到 30s
		},
	}
}

// Chat 非流式调用 LLM——一次性返回完整结果。
func (c *LLMClient) Chat(userMessage string) (string, error) {
	// 没有配置 API Key → 降级为关键词匹配（开发/演示用）
	if c.apiKey == "" {
		slog.Warn("llm api key empty, using fallback keyword match")
		time.Sleep(200 * time.Millisecond) // 模拟网络延迟
		return fallbackIntent(userMessage), nil
	}

	systemPrompt := buildSystemPrompt()
	userPrompt := fmt.Sprintf("用户指令：%s", userMessage)

	reqBody := llmReq{
		Model:  c.model,
		Stream: false,
		Messages: []llmMsg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := c.baseURL + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result llmResp
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}

	content := result.Choices[0].Message.Content
	slog.Info("llm response", "content", content)
	return content, nil
}

// ChatStream 以 SSE 流式方式调用 LLM。
// 每次收到一个文本增量（delta），就调用一次 onChunk 回调。
func (c *LLMClient) ChatStream(userMessage string, onChunk func(delta string) error) error {
	systemPrompt := buildSystemPrompt()
	userPrompt := fmt.Sprintf("用户指令：%s", userMessage)

	reqBody := llmReq{
		Model:  c.model,
		Stream: true, // 流式唯一区别
		Messages: []llmMsg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	url := c.baseURL + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("llm returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// bufio.Scanner 逐行读响应体——每个 SSE 事件是一行
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 64*1024) // 初始 4KB，最大 64KB

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue // 跳过空行和非 data 行
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break // LLM 生成完毕
		}

		var chunk llmStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			slog.Warn("unmarshal stream chunk failed", "err", err, "data", data)
			continue // 某一行 JSON 坏了不中止——继续读下一行
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta.Content
		if delta == "" {
			continue // 有些 chunk 不带 content，只带 finish_reason
		}

		if err := onChunk(delta); err != nil {
			return err // 调用方可返回 error 中止流式读取
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}
	return nil
}

func buildSystemPrompt() string {
	return `你是一个智能家居语音助手。分析用户的自然语言指令，识别意图，返回严格 JSON。
支持的设备类型：light（灯）、aircon（空调）、curtain（窗帘）、socket（插座）
支持的操作：turn_on、turn_off、set_brightness（灯光亮度0-100）、set_temp（空调温度16-30）、set_mode（cool/heat/auto）、open、close
如果无法识别意图，action 填 "unknown"。

返回格式（不要加任何额外文字）：
{"action":"操作","target":"设备类型","params":{"device_id":"如果用户指定了具体设备名或房间则填，否则填null","room":"房间名或null","value":数字或null}}`
}

// fallbackIntent 无 API Key 时的关键词降级匹配。
// 开发阶段不依赖外部 LLM，简单关键词匹配即可验证全链路。
func fallbackIntent(text string) string {
	action := "unknown"
	target := "unknown"
	room := "null"

	// 动作匹配
	switch {
	case strings.Contains(text, "开灯") || strings.Contains(text, "打开灯") ||
		(strings.Contains(text, "开") && strings.Contains(text, "灯")):
		action = "turn_on"
		target = "light"
	case strings.Contains(text, "关灯") || strings.Contains(text, "关闭灯") ||
		(strings.Contains(text, "关") && strings.Contains(text, "灯")):
		action = "turn_off"
		target = "light"
	case strings.Contains(text, "开空调") || strings.Contains(text, "打开空调"):
		action = "turn_on"
		target = "aircon"
	case strings.Contains(text, "关空调") || strings.Contains(text, "关闭空调"):
		action = "turn_off"
		target = "aircon"
	case strings.Contains(text, "开窗帘") || strings.Contains(text, "打开窗帘"):
		action = "open"
		target = "curtain"
	case strings.Contains(text, "关窗帘") || strings.Contains(text, "关闭窗帘") || strings.Contains(text, "拉上窗帘"):
		action = "close"
		target = "curtain"
	case strings.Contains(text, "温度") || strings.Contains(text, "度"):
		action = "set_temp"
		target = "aircon"
	case strings.Contains(text, "打开") || strings.Contains(text, "开"):
		action = "turn_on"
		target = "light"
	case strings.Contains(text, "关闭") || strings.Contains(text, "关"):
		action = "turn_off"
		target = "light"
	}

	// 房间匹配
	switch {
	case strings.Contains(text, "客厅"):
		room = `"客厅"`
	case strings.Contains(text, "卧室"):
		room = `"卧室"`
	case strings.Contains(text, "厨房"):
		room = `"厨房"`
	}

	return fmt.Sprintf(
		`{"action":"%s","target":"%s","params":{"device_id":null,"room":%s,"value":null}}`,
		action, target, room,
	)
}
