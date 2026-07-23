package service

import (
	"encoding/json"
	"log/slog"

	"github.com/SinnerK9/my-iot-server/internal/client"
	ws "github.com/SinnerK9/my-iot-server/internal/websocket"
)

// Intent LLM 返回的意图 JSON 结构 如{"action":"turn_on","target":"light","params":{"room":"客厅"}}
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
	//新增对目标设备的归属校验
	targetDevice := o.resolveDevice(userID, &intent)
	if targetDevice == nil{
		o.hub.SendToUser(userID, []byte(`{"type":"error","msg":"未找到可控制的设备，请先绑定设备或指定正确的设备名/房间"}`))
		return
	}

	cmd := map[string]interface{}{
		"action": intent.Action,
		"target": intent.Target,
	}

	if intent.Params != nil{
		if v,ok := intent.Params["value"] ok && v != nil{
			cmd["value"] = v
		}
	}
	cmdBytes, _ := json.Marshal(cmd)

	if err := o.mqtt.PublishCommand(targetDevice.DeviceID,cmdbytes); err!= nil{
		slog.Error("mqtt publish failed","err",err,"deviceID",targetDevice.DeviceID)
		o.hub.SendToUser(userID,[]byte(`{"type":"error","msg":"设备通信失败"}`))
		return
	}

	// 没有 deviceID 但有 room → 将来可以查 room 下的所有设备然后逐个下发（Week 3 Day 6）
	_ = room

	//Hub广播结果给所有在线用户
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

func(o *ChatOrchestrator) resolveDevice(userID uint64, intent *Intent) *model.Device{
	if intent.Params == nil{
		return nil
	}
	//1》LLM给出明确的deviceID
	if deviceID ,ok := intent.Params["device_id"].(string); ok && deviceID != ""{
		device,err := repository.GetDeviceByDeviceID(deviceID)
		if err != nil {
			//并不是库里没有这种预设好的错误
			if !error.Is(err,sql.ErrNoRows){
				slog.Error("lookup device failed", "err", err, "deviceID", deviceID)
			}
			return nil
		}
		if device.OwnerID != userID{
			slog.Warn("ownership check failed", "userID", userID, "deviceID", deviceID, "ownerID", device.OwnerID)
			return nil
		}
		return device
	}
	//2.只有room，找第一个device
	if room, ok := intent.Params["room"].(string); ok && room != ""{
		devices,err := repository.ListDevicesByOwner(userID)
		if err != nil{
			slog.Error("list devices failed", "err", err, "userID", userID)
			return nil
		}
		for i := range devices{
			if devices[i].Room == room && devices[i].Status == "online"【
			return &devices[i]
		}
	}
	//3.没有指定设备和房间：从用户在线设备里找第一个匹配的
	devices,err := repository.ListDevicesByOwner(userID)
	if err != nil{
		return nil
	}
	for i := range devices{
		if devices[i].Type == intent.Target && devices[i].Status == "online" {
		return &devices[i]
		}
	}
	return nil
}