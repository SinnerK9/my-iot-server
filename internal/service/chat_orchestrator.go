package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/SinnerK9/my-iot-server/internal/client"
	"github.com/SinnerK9/my-iot-server/internal/model"
	"github.com/SinnerK9/my-iot-server/internal/repository"
	ws "github.com/SinnerK9/my-iot-server/internal/websocket"
)

// Intent LLM 返回的意图 JSON 结构。
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

// HandleMessage 是 Hub.OnMessage 的回调实现。
// userID 来自 ReadPump，payload 是用户发来的原始文本。
func (o *ChatOrchestrator) HandleMessage(userID uint64, payload []byte) {
	text := string(payload)
	slog.Info("orchestrator handling message", "userID", userID, "text", text)

	// 第 1 步：调 LLM 解析意图
	llmText, err := o.llm.Chat(text)
	if err != nil {
		slog.Error("llm failed", "err", err)
		o.hub.SendToUser(userID, []byte(`{"type":"error","msg":"大模型服务不可用，请稍后重试"}`))
		return
	}

	// 第 2 步：解析意图——容错提取 JSON
	intent := parseIntent(llmText)
	if intent == nil {
		// LLM 返回了非 JSON 内容——把原文推给用户
		o.hub.SendToUser(userID, []byte(`{"type":"llm_response","text":"`+llmText+`"}`))
		return
	}

	// 无法识别的意图 → 礼貌回复
	if intent.Action == "unknown" {
		o.hub.SendToUser(userID, []byte(`{"type":"llm_response","text":"不太明白你的意思"}`))
		return
	}

	// 第 3 步：MQTT 在线检查
	if !o.mqtt.IsConnected() {
		o.hub.SendToUser(userID, []byte(`{"type":"error","msg":"设备通信暂不可用，请稍后重试"}`))
		return
	}

	// 第 4 步：找到目标设备 + 归属校验
	targetDevice := o.resolveDevice(userID, intent)
	if targetDevice == nil {
		o.hub.SendToUser(userID, []byte(`{"type":"error","msg":"未找到可控制的设备，请先绑定设备或指定正确的设备名/房间"}`))
		return
	}

	// 第 5 步：构造控制指令，MQTT 下发
	cmd := map[string]interface{}{
		"action": intent.Action,
		"target": intent.Target,
	}
	if intent.Params != nil {
		if v, ok := intent.Params["value"]; ok && v != nil {
			cmd["value"] = v
		}
	}
	cmdBytes, _ := json.Marshal(cmd)

	if err := o.mqtt.PublishCommand(targetDevice.DeviceID, cmdBytes); err != nil {
		slog.Error("mqtt publish failed", "err", err, "deviceID", targetDevice.DeviceID)
		o.hub.SendToUser(userID, []byte(`{"type":"error","msg":"设备通信失败"}`))
		return
	}

	// 第 6 步：Hub 广播结果给所有在线用户
	result := map[string]interface{}{
		"type":      "device_command",
		"action":    intent.Action,
		"target":    intent.Target,
		"device_id": targetDevice.DeviceID,
		"name":      targetDevice.Name,
		"room":      targetDevice.Room,
	}
	resultBytes, _ := json.Marshal(result)
	o.hub.Broadcast <- resultBytes

	slog.Info("orchestrator done", "userID", userID, "action", intent.Action, "target", intent.Target)
}

// resolveDevice 根据 LLM 返回的意图找到目标设备，并校验归属。
// 三级查找策略：
//   A. LLM 给出了明确的 device_id
//   B. LLM 只给了 room——查该房间该用户的第一台在线设备
//   C. 什么都没给——查该用户第一台匹配类型的在线设备
func (o *ChatOrchestrator) resolveDevice(userID uint64, intent *Intent) *model.Device {
	if intent.Params == nil {
		return nil
	}

	// 情况 A：LLM 给出了明确的 device_id
	if deviceID, ok := intent.Params["device_id"].(string); ok && deviceID != "" {
		device, err := repository.GetDeviceByDeviceID(deviceID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				slog.Error("lookup device failed", "err", err, "deviceID", deviceID)
			}
			return nil
		}
		// 归属校验：设备必须属于当前用户
		if device.OwnerID != userID {
			slog.Warn("ownership check failed", "userID", userID, "deviceID", deviceID, "ownerID", device.OwnerID)
			return nil
		}
		return device
	}

	// 情况 B：LLM 只给了 room——查该用户在该房间的第一台在线设备
	if room, ok := intent.Params["room"].(string); ok && room != "" {
		devices, err := repository.ListDevicesByOwner(userID)
		if err != nil {
			slog.Error("list devices failed", "err", err, "userID", userID)
			return nil
		}
		for i := range devices {
			if devices[i].Room == room && devices[i].Status == "online" {
				return &devices[i]
			}
		}
	}

	// 情况 C：没指定设备也没指定房间——找第一台匹配类型的在线设备
	devices, err := repository.ListDevicesByOwner(userID)
	if err != nil {
		return nil
	}
	for i := range devices {
		if devices[i].Type == intent.Target && devices[i].Status == "online" {
			return &devices[i]
		}
	}

	return nil
}
