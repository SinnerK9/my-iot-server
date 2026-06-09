package repository

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

var RDB *redis.Client //包级全局变量

// InitRedis连接redis，ping验证
func InitRedis(addr string) error {
	//创建一个Redis客户端对象，准备好连接参数
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: "", //Docker Redis默认无密码
		DB:       0,  //redis内的默认数据库
		PoolSize: 10, //连接池大小
	})
	//创建一个带有3s超时的context
	//传入的context.Background()表示“所有context的起点”
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel() //无论ping成功或失败，函数返回前调用cancel释放context资源，防止泄露
	//go-redis的命令返回一个 *StatusCmd(命令对象，包含result和error)，err()返回其中的error
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}

	RDB = rdb
	slog.Info("redis connected")
	return nil
}

func CloseRedis() {
	if RDB != nil {
		RDB.Close()
		slog.Info("redis closed")
	}
}

const deviceOnlineTTL = 120 * time.Second

func SetDeviceOnline(deviceID string, info map[string]interface{}) error {
	//用于标记设备在线
	ctx := context.Background()
	key := "device:" + deviceID
	// HSET：写入Hash。info 包含设备信息如type, name, status等字段，用于存储设备的详细信息
	if err := RDB.HSet(ctx, key, info).Err(); err != nil {
		return fmt.Errorf("HSET %s: %w", key, err)
	}
	//EXPIRE：设TTL，redis收到命令后，120秒后key自动删除（如果心跳没续的话）
	if err := RDB.Expire(ctx, key, deviceOnlineTTL); err != nil {
		return fmt.Errorf("EXPIRE:%s: %w", key, err)
	}
	//SADD：将其加入在线集合，方便查询哪些设备在线
	if err := RDB.SAdd(ctx, "online_devices", deviceID).Err(); err != nil {
		return fmt.Errorf("SADD online_devices: %w", err)
	}
	return nil
}

// SetDeviceOffline标记设备离线。
func SetDeviceOffline(deviceID string) error {
	ctx := context.Background()
	key := "device:" + deviceID

	// 删除 Hash
	if err := RDB.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("DEL %s: %w", key, err)
	}
	// 从在线集合移除
	if err := RDB.SRem(ctx, "online_devices", deviceID).Err(); err != nil {
		return fmt.Errorf("SREM online_devices: %w", err)
	}
	return nil
}

// GetOnlineDevices 返回所有在线设备 ID。
func GetOnlineDevices() ([]string, error) {
	ctx := context.Background()
	return RDB.SMembers(ctx, "online_devices").Result()
}

func GetDeviceInfo(deviceID string) (map[string]string, error) {
	ctx := context.Background()
	key := "device:" + deviceID
	result, err := RDB.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("HGETALL device:%s: %w", deviceID, err)
	}
	if len(result) == 0 {
		return nil, nil //找不到key：设备离线
	}
	return result, nil
}
