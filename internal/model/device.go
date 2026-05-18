package model

import "time"

type Device struct {
	ID           uint64     `db:"id"`
	DeviceID     string     `db:"device_id"` // MQTT client id
	OwnerID      uint64     `db:"owner_id"`
	Type         string     `db:"type"` // light / aircon / curtain / socket
	Name         string     `db:"name"`
	Room         string     `db:"room"`
	Status       string     `db:"status"` // online / offline / error
	BoundAt      *time.Time `db:"bound_at"`
	LastOnlineAt *time.Time `db:"last_online_at"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
}

//BoundAt和LastOnlineAt在绑定前是NULL，用指针类型的time.Time空值为nil，对应SQL的NULL。值类型的Time是0001-01-01，扫进去会报错
