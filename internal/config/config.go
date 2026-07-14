package config

import "os"

// 定义配置结构体，首位大写使得其可以被外界访问
type Config struct {
	Port      string
	DBHost    string
	DBPort    string
	DBUser    string
	DBPass    string
	DBName    string
	JWTSecret string //新增JWT签名密钥，用于鉴权，该密钥全局共享
	RedisAddr string //新增Redis地址"host:port"
	LLMKey string //API密钥
	LLMURL string //API地址
	LLMModel string //模型名字
}

// 从环境变量读取配置，读不到就用指定的默认值，最后返回配置结构体的指针
func Load() *Config {
	//Go中允许直接返回局部变量的指针，发现返回指针则将其分配在堆上
	return &Config{
		Port:      getenv("PORT", "7777"), //结构体变量的赋值应该用冒号
		DBHost:    getenv("DB_HOST", "127.0.0.1"),
		DBPort:    getenv("DB_PORT", "3307"),
		DBUser:    getenv("DB_USER", "root"),
		DBPass:    getenv("DB_PASS", "123456"),
		DBName:    getenv("DB_NAME", "iot_gateway"),
		JWTSecret: getenv("JWT_SECRET", "dev-secret-change-in-production"),
		RedisAddr: getenv("REDIS_ADDR", "127.0.0.1:6379"),
		LLMKey:   getenv("LLM_KEY", ""),
		LLMURL: getenv("LLM_URL","https://api.deepseek.com"),
		LLMModel: getenv("LLM_MODEL","deepseek-chat"),
	}
}

func getenv(key, def string) string {
	//getenv读取环境变量，读不到则返回默认值
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
