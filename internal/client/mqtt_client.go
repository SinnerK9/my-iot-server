package client

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

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
	opts.SetClientID(clientID) //客户端id在broker下不能重复
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectDelay(10 * time.Second)
	c := mqtt.NewClient(opts)
	token := c.Connect()
	// token.Wait() 阻塞直到连接完成
	if token.Wait() && token.Error() != nil {
		return nil, fmt.Errorf("mqtt connect: %w", token.Error())
	}
	slog.Info("mqtt connected", "broker", brokerURL)
	return &MQTTClient{client: c}, nil
}

func (m *MQTTClient) PublishCommand(deviceID string, payload []byte) error {
	topic := fmt.Sprintf("shva/device/%s/cmd", deviceID) //拼接MQTT消息主题，格式为 shva/device/{设备ID}/cmd
	token := m.client.Publish(topic, 1, false, payload)  //返回一个异步操作句柄，Publish本身异步非阻塞，Token的作用是等待后续操作完成获取结果
	//publish设置QoS1，至少会送达一次，必须受到broker返回的ACK包才不再发送
	token.Wait() //没有wait，协程会一直运行，在执行完毕之前err大概率一直是nil，会导致误读
	if err := token.Error(); err != nil {
		return fmt.Errorf("publish to %s: %w", topic, err)
	}
	slog.Info("mqtt published", "topic", topic, "payload", string(payload))
	return nil
}

// SubscribeDeviceStatus 订阅所有设备的状态上报。
func (m *MQTTClient) SubscribeDeviceStatus(onStatus func(deviceID string, payload []byte)) {
	topic := "shva/device/+/status" //+为通配符，匹配任意一个设备id，一次性订阅所有设备的状态主题
	//定义一个回调匿名函数，当设备上报状态时调用
	token := m.client.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		// 从 topic 中提取设备 ID
		deviceID := extractDeviceID(msg.Topic())
		//onstatus应为上层业务函数
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
func extractDeviceID(topic string) string {
	parts := strings.Split(topic, "/")
	if len(parts) >= 3 {
		return parts[2] // [shva, device, light-001, status] → index 2
	}
	return ""
}
