package service

import (
	"encoding/json"
	"log/slog"

	"github.com/SinnerK9/my-iot-server/internal/client"
	ws "github.com/SinnerK9/my-iot-server/internal/websocket"
)

// Intent LLM 返回的意图 JSON 结构
type Intent struct {
	Action string                 `json:"action"`
	Target string                 `json:"target"`
	Params map[string]interface{} `json:"params"`
}

// ChatOrchestrator 编排 WS → LLM → 意图解析 → MQTT → Redis → Hub 的全链路。
type ChatOrchestrator struct {
	llm  *client.LLMClient
	mqtt *client.MQTTClient
	hub  *ws.Hub
}

func NewChatOrchestrator(llm *client.LLMClient, mqtt *client.MQTTClient, hub *ws.Hub) *ChatOrchestrator {
	return &ChatOrchestrator{llm: llm, mqtt: mqtt, hub: hub}
}

func (o *ChatOrchestrator) HandleMessage(userID uint64, payload []byte) {
	text := string(payload)
	slog.Info("orchestrator handling message", "userID", userID, "text", text)

	//调llm解析意图
	llmText, err := o.llm.Chat(text)
	if err != nil {
		slog.Error("llm failed", "err", err)
		o.hub.SendToUser(userID, []byte(`{"type":"error","msg":"大模型服务不可用，请稍后重试"}`))
		return
	}

	//解析意图JSON
	var intent Intent
	if err := json.Unmarshal([]byte(llmText), &intent); err != nil {
		slog.Error("parse intent failed", "err", err, "llmText", llmText)
		//LLM 返回了非 JSON 内容——把原文推给用户看看
		o.hub.SendToUser(userID, []byte(`{"type":"llm_response","text":"`+llmText+`"}`))
		return
	}

	// unknown → 没有可执行的动作，直接返回 LLM 文本
	if intent.Action == "unknown" {
		o.hub.SendToUser(userID, []byte(`{"type":"llm_response","text":"不太明白你的意思"}`))
		return
	}

	//构造控制指令，MQTT下发
	deviceID := ""
	room := ""
	if intent.Params != nil {
		if id, ok := intent.Params["device_id"].(string); ok && id != "" {
			deviceID = id
		}
		if r, ok := intent.Params["room"].(string); ok {
			room = r
		}
	}
	cmd := map[string]interface{}{
		"action": intent.Action,
		"target": intent.Target,
		"room":   room,
	}
	cmdBytes, _ := json.Marshal(cmd)

	// 如果有明确设备 ID，下发 MQTT 指令
	if deviceID != "" {
		if err := o.mqtt.PublishCommand(deviceID, cmdBytes); err != nil {
			slog.Error("mqtt publish failed", "err", err)
		}
	}

	// 没有 deviceID 但有 room → 将来可以查 room 下的所有设备然后逐个下发（Week 3 Day 6）
	_ = room

	//Hub广播结果给所有在线用户
	result := map[string]interface{}{
		"type":   "device_command",
		"action": intent.Action,
		"target": intent.Target,
		"detail": llmText,
	}
	resultBytes, _ := json.Marshal(result)
	o.hub.Broadcast <- resultBytes

	slog.Info("orchestrator done", "userID", userID, "action", intent.Action, "target", intent.Target)
}
