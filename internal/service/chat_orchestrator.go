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

	// 第 1 步：调 LLM 解析意图
	llmText, err := o.llm.Chat(text)
	if err != nil {
		slog.Error("llm failed", "err", err)
		o.hub.SendToUser(userID, []byte(`{"type":"error","msg":"大模型服务不可用，请稍后重试"}`))
		return
	}

	// 第 2 步：解析意图 JSON
	intent := parseIntent(llmText)
	if intent == nil {
		o.hub.SendToUser(userID, []byte(`{"type":"llm_response","text":"`+llmText+`"}`))
		return
	}

	if intent.Action == "unknown" {
		o.hub.SendToUser(userID, []byte(`{"type":"llm_response","text":"不太明白你的意思"}`))
		return
	}

	// 第 3 步：找目标设备 + 归属校验
	targetDevice := o.resolveDevice(userID, intent)
	if targetDevice == nil {
		o.hub.SendToUser(userID, []byte(`{"type":"error","msg":"未找到可控制的设备"}`))
		return
	}

	// 第 4 步：MQTT 下发（离线时跳过，演示模式不影响）
	if o.mqtt.IsConnected() {
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
			slog.Error("mqtt publish failed", "err", err)
		}
	} else {
		slog.Warn("mqtt not connected, skipping publish")
	}

	// 第 5 步：Hub 广播结果给所有在线用户
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
		if device.OwnerID != userID {
			slog.Warn("ownership check failed", "userID", userID, "deviceID", deviceID)
			return nil
		}
		return device
	}

	// 情况 B：LLM 给了 room——找该房间第一台设备
	if room, ok := intent.Params["room"].(string); ok && room != "" {
		devices, err := repository.ListDevicesByOwner(userID)
		if err != nil {
			slog.Error("list devices failed", "err", err)
			return nil
		}
		for i := range devices {
			if devices[i].Room == room {
				return &devices[i]
			}
		}
	}

	// 情况 C：找第一台匹配类型的设备
	devices, err := repository.ListDevicesByOwner(userID)
	if err != nil {
		return nil
	}
	for i := range devices {
		if devices[i].Type == intent.Target {
			return &devices[i]
		}
	}

	return nil
}
