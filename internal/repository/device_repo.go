package repository

import(
	"fmt"
	"time"
	"github.com/SinnerK9/my-iot-server/internal/model"
)

func GetDeviceByDeviceID(deviceID string) (*model.Device, error) {
        var device model.Device
        err := DB.Get(&device, `SELECT * FROM devices WHERE device_id=?`, deviceID)
        if err != nil {
                return nil, fmt.Errorf("GetDeviceByDeviceID: %w", err)
        }
        return &device, nil
}

// ListDevicesByOwner 查某个用户拥有的所有设备
func ListDevicesByOwner(ownerID uint64) ([]model.Device, error) {
        var devices []model.Device
        err := DB.Select(&devices,
                `SELECT * FROM devices WHERE owner_id=? ORDER BY id DESC`, ownerID)
        if err != nil {
                return nil, fmt.Errorf("ListDevicesByOwner: %w", err)
        }
        return devices, nil
}

// CreateDevice 向系统注册新设备（还未绑定任何人）
func CreateDevice(device *model.Device) (uint64, error) {
        result, err := DB.NamedExec(`INSERT INTO devices (device_id, owner_id, type, name, room, status)
                VALUES (:device_id, :owner_id, :type, :name, :room, :status)`, device)
        if err != nil {
                return 0, fmt.Errorf("CreateDevice: %w", err)
        }
        id, _ := result.LastInsertId()
        return uint64(id), nil
}

// UpdateDevice 修改设备名称、房间
func UpdateDevice(deviceID string, name, room string) error {
        _, err := DB.Exec(`UPDATE devices SET name=?, room=? WHERE device_id=?`,
                name, room, deviceID)
        if err != nil {
                return fmt.Errorf("UpdateDevice: %w", err)
        }
        return nil
}

// UnbindDevice 解绑设备——清掉 owner_id，状态切回 offline
func UnbindDevice(deviceID string) error {
        _, err := DB.Exec(`UPDATE devices SET owner_id=0, bound_at=NULL, status='offline' WHERE device_id=?`, deviceID)
        if err != nil {
                return fmt.Errorf("UnbindDevice: %w", err)
        }
        return nil
}
//在事务中绑定设备，防止并发绑定带来的绑定错误
///在这里绑定操作必须原子完成，保证不会出现同时绑定导致的错误
func BindDevice(deviceID string, ownerID uint64) error{
        //此处开始事务，得到的tx和db性质相同
        tx,err := DB.Beginx()
        if err != nil {
                return fmt.Errorf("Beginx: %w",err)
        }
        //延后执行RollBack，无论如何，函数退出时必须执行
        //对于commit成功的，RollBack会提示已提交的事务不能回滚，反之(return err)则会真正执行，事务中所有修改被撤销
        //类似RAII
        defer tx.Rollback()
        //语句中包含FOR UPDATE代表MYSQL行级锁，第一个事务锁住这行后，在其COMMIT前第二个事务在这行只能阻塞等待
        //第一个绑定成功后第二个进入发现owner_id已经不为0
        var device model.Device
        err =  tx.Get(&device,`SELECT * FROM devices WHERE device_id=? FOR UPDATE`, deviceID)
        if err != nil{
                return fmt.Errorf("select device: %w",err)
        }
        if device.OwnerID != 0{
                return fmt.Errorf("设备 %s 已被绑定",deviceID)
        }
        // 更新归属——在事务内执行
        now := time.Now()
        _, err = tx.Exec(`UPDATE devices SET owner_id=?, bound_at=?, status='online' WHERE device_id=?`,
                ownerID, now, deviceID)
        if err != nil {
                return fmt.Errorf("update device: %w", err)
        }
        // 提交事务——成功则所有改动生效
        err = tx.Commit()
        if err != nil {
                return fmt.Errorf("commit: %w", err)
        }
        return nil
}