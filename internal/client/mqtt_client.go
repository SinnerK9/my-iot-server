package client

import (
	"fmt"
	"log/slog"
	"strings"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// MQTTClient 封装 MQTT 连接——发布指令 + 订阅设备状态。
type MQTTClient struct {
	client mqtt.Client
}

// NewMQTTClient 连接 MQTT Broker 并返回客户端。
// brokerURL 格式：tcp://127.0.0.1:1883
func NewMQTTClient(brokerURL, clientID string) (*MQTTClient, error) {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(brokerURL)
	opts.SetClientID(clientID) // 客户端 ID 在 broker 下不能重复
	opts.SetAutoReconnect(true) // 断线自动重连，库自带默认间隔

	// Day 6：监听连接状态变化
	opts.OnConnectionLost = func(_ mqtt.Client, err error) {
		slog.Warn("mqtt connection lost", "err", err)
	}
	opts.OnConnect = func(_ mqtt.Client) {
		slog.Info("mqtt reconnected")
	}

	c := mqtt.NewClient(opts)
	token := c.Connect()
	// token.Wait() 阻塞直到连接完成
	if token.Wait() && token.Error() != nil {
		return nil, fmt.Errorf("mqtt connect: %w", token.Error())
	}

	slog.Info("mqtt connected", "broker", brokerURL)
	return &MQTTClient{client: c}, nil
}

// IsConnected 返回 MQTT 是否处于连接状态。
// 调用方在发指令前检查——如果断了，给用户即时反馈。
func (m *MQTTClient) IsConnected() bool {
	return m.client.IsConnected()
}

// PublishCommand 向指定设备下发控制指令。
// topic：shva/device/{deviceID}/cmd，QoS 1
func (m *MQTTClient) PublishCommand(deviceID string, payload []byte) error {
	topic := fmt.Sprintf("shva/device/%s/cmd", deviceID)
	token := m.client.Publish(topic, 1, false, payload)
	token.Wait() // 阻塞等 Broker ACK
	if err := token.Error(); err != nil {
		return fmt.Errorf("publish to %s: %w", topic, err)
	}
	slog.Info("mqtt published", "topic", topic, "payload", string(payload))
	return nil
}

// SubscribeDeviceStatus 订阅所有设备的状态上报。
// topic 通配符 + 匹配任意一个设备 ID，一次性覆盖所有设备。
func (m *MQTTClient) SubscribeDeviceStatus(onStatus func(deviceID string, payload []byte)) {
	topic := "shva/device/+/status"
	token := m.client.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		deviceID := extractDeviceID(msg.Topic())
		onStatus(deviceID, msg.Payload())
	})
	token.Wait()
	if err := token.Error(); err != nil {
		slog.Error("mqtt subscribe failed", "err", err)
		return
	}
	slog.Info("mqtt subscribed", "topic", topic)
}

// Disconnect 断开 MQTT 连接——defer 在 main.go 里调。
func (m *MQTTClient) Disconnect() {
	m.client.Disconnect(250) // 等 250ms 让未发完的消息发出去
	slog.Info("mqtt disconnected")
}

// extractDeviceID 从 MQTT topic 中提取设备 ID。
// "shva/device/light-001/status" → "light-001"
func extractDeviceID(topic string) string {
	parts := strings.Split(topic, "/")
	if len(parts) >= 3 {
		return parts[2] // [shva, device, light-001, status] → index 2
	}
	return ""
}
