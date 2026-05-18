package repository

import (
	"fmt"
	"log/slog"
	"time"

	_ "github.com/go-sql-driver/mysql" // 匿名 import，只执行 init() 注册驱动的初始化逻辑，注册表里有了mysql这个名字，sqlx.open才能查到驱动。
	"github.com/jmoiron/sqlx"
)

var DB *sqlx.DB // 包级变量，大写 D 表示公开（跨包可访问）

// InitDB 打开数据库连接并配置连接池。
// 返回 error 使得调用方可以处理——而不是在内部 panic 或 os.Exit。
func InitDB(dsn string) error {
	db, err := sqlx.Open("mysql", dsn) //传入驱动类型MySQL和一串含有信息的字符串，创建sqlx.db对象，并且进行连接池结构的初始化（在dsn中）
	if err != nil {
		return fmt.Errorf("sqlx.Open: %w", err) // %w是错误包装，用errors.Is()解包后判断错误类型
	}

	// SetMaxOpenConns：同时最多打开多少个连接（包括正在用的 + 空闲的）。
	db.SetMaxOpenConns(25)

	// SetMaxIdleConns：连接池里保留部分空闲连接，而非用完直接关闭
	// 新请求来了不用重新 TCP 握手 + MySQL 认证，直接拿空闲连接用。否则每次都要重新连接
	db.SetMaxIdleConns(5)

	// SetConnMaxLifetime：一个连接最多活多久。
	// 设置为 1 小时可以在 MySQL 断开之前由 Go 主动关闭并重建。（MySQL默认8小时断开空闲连接）
	db.SetConnMaxLifetime(time.Hour)

	// Ping 验证连接参数（DSN）是否正确
	// 真正的连接是由ping执行的，open只是建立这么一个交互对象
	if err := db.Ping(); err != nil {
		return fmt.Errorf("db.Ping: %w", err)
	}

	DB = db
	slog.Info("database connected")
	return nil
}

// CloseDB 在 main.go 里 defer 调用，确保程序退出前归还所有连接
func CloseDB() {
	if DB != nil {
		DB.Close()
		slog.Info("database closed")
	}
}
