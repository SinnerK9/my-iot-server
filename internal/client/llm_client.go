package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type llmResp struct {
	Choices []struct{
		Message llmMsg `json:"message"`
	} `json:"choices"`
}

// LLMClient 封装一次配置后的 LLM 调用能力。
// 对外只暴露 Chat 方法，隐藏 HTTP 细节。
type LLMClient struct{
	apikey string
	baseURL string
	model string
	http *http.Client
}

func NewLLMClient(apikey,baseURL,model string) *LLMClient{
	return &http.Client{
		apiKey: apikey,
		baseURL: baseURL,
		model: model,
		http: &http.Client{
			// Transport 级超时——整个 HTTP 事务（DNS + TCP + TLS + 等待响应体）
            // 不能超过 10 秒。和 context.WithTimeout 双重保障。
            Timeout: 10 * time.Second,
		},
	}
}

func (c *LLMClient) Chat(userMessage string) (string,error){
	//构造prompt
	systemPrompt := `你是一个智能家居语音助手。分析用户的自然语言指令，识别意图，返回严格 JSON。
支持的设备类型：light（灯）、aircon（空调）、curtain（窗帘）、socket（插座）
支持的操作：turn_on、turn_off、set_brightness（灯光亮度0-100）、set_temp（空调温度16-30）、set_mode（cool/heat/auto）、open、close
如果无法识别意图，action 填 "unknown"。

返回格式（不要加任何额外文字）：
{"action":"操作","target":"设备类型","params":{"device_id":"如果用户指定了具体设备名或房间则填，否则填null","room":"房间名或null","value":数字或null}}`

	userPrompt := fmt.Sprintf("用户指令：%s", userMessage)
	//构造请求体
	reqBody := llmReq{
		Model: c.model,
		//[]llmMsg{...} 是多行 struct 字面量——创建一个 llmMsg 切片，含两个元素。
		Messages: []llmMsg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}
	//go struct -> JSON文本([]byte)，按照struct tag决定key名称
	jsonBody , err:= json.Marshal(reqBody)
	if err != nil{
		return "", fmt.Errorf("marshal request: %w",err)
	}

	//构建http请求（带有超时context）
	ctx,cancel := context.WithTimeout(context.Background(),10 * time.Second)
	defer cancel()

	url := c.baseURL + "v1/chat/completions"
	//bytes.NewReader将[]byte转化为io.Reader，适应http.NewRequestWithContext的参数需求
	httpReq,err := http.NewRequestWithContext(ctx,http.MethodPost,url,bytes.NewReader(jsonBody))
	if err != nil{
		return "", fmt.Errorf("create request: %w",err)
	}
	httpReq.Header.Set("Content-Type","application/json")
	httpReq.Header.Set("Authorization","Bearer "+c.apiKey)

	slog.Info("llm calling","url",url,"model",c.model)
	resp.err := c.http.Do(httpReq)
	if err != nil{
		return "",fmt.Errorf("read body: %w",err)
	}

	if resp.StatusCode != http.StatusOK{
		return "",fmt.Errorf("llm returned %d: %s",resp.StatusCode,string(bodyBytes))
	}
	//解析JSON响应
	var result llmResp
	if err := json.Unmarshal(bodyBytes, &result); err != nil{
		return "",fmt.Errorf("unmarshal response: %w",err)
	}

	if len(result.Choices) == 0 {
              return "", fmt.Errorf("llm returned no choices")
    }

    content := result.Choices[0].Message.Content
    slog.Info("llm response", "content", content)
    return content, nil
}