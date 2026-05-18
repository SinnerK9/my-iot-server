package model

import "time"

// 数据库和Go的映射，通过struct tag给数据库驱动进行对应（sqlx规定）
type User struct {
	ID        uint64    `db:"id"`
	Phone     string    `db:"phone"`
	Email     string    `db:"email"`
	Password  string    `db:"password"` // bcrypt hash，永远不序列化到 JSON 响应里
	Nickname  string    `db:"nickname"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}
