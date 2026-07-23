package service

import (
	"encoding/json"
	"strings"
)

// extractJSON 从 LLM 的原始输出中提取 JSON。
// LLM 经常在 JSON 外包裹 markdown 代码块或说明文字——这个函数做容错提取。
//
// 三级策略：
//  1. 直接解析——LLM 乖乖返回了纯 JSON
//  2. 从 ```json ... ``` 代码块中提取
//  3. 从文本中找到第一个 { 到最后一个 } 截取——最后手段
func extractJSON(raw string) string {
	raw = strings.TrimSpace(raw)

	// 策略 1：直接是 JSON
	if strings.HasPrefix(raw, "{") {
		return raw
	}

	// 策略 2：被包在 ```json ... ``` 里
	if idx := strings.Index(raw, "```json"); idx != -1 {
		start := strings.Index(raw[idx:], "{")
		if start == -1 {
			return raw
		}
		start += idx
		end := strings.Index(raw[start:], "```")
		if end == -1 {
			return raw[start:]
		}
		return strings.TrimSpace(raw[start : start+end])
	}

	// 策略 3：从第一个 { 到最后一个 }
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start != -1 && end != -1 && end > start {
		return raw[start : end+1]
	}

	return raw
}

// parseIntent 尝试把 LLM 返回的文本解析为 Intent。
// 返回 nil 表示不可识别——调用方应做降级处理。
func parseIntent(llmText string) *Intent {
	jsonStr := extractJSON(llmText)

	var intent Intent
	if err := json.Unmarshal([]byte(jsonStr), &intent); err != nil {
		return nil
	}

	// 校验必要字段：没有 action 等于没法执行
	if intent.Action == "" {
		return nil
	}

	return &intent
}
