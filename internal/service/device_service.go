package service

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/SinnerK9/my-iot-server/internal/model"
	"github.com/SinnerK9/my-iot-server/internal/repository"
)

// 注册新设备到系统
func CreateDevice(userID uint64, req *model.CreateDeviceReq) (uint64, error) {
	existing, err := repository.GetDeviceByDeviceID(req.DeviceID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("CreateDevice: %w", err)
	}
	if existing != nil {
		return 0, errors.New("设备ID已存在")
	}
	device := &model.Device{
		DeviceID: req.DeviceID,
		OwnerID:  userID,
		Type:     req.Type,
		Name:     req.Name,
		Room:     req.Room,
		Status:   "offline",
	}
	id, err := repository.CreateDevice(device)
	if err != nil {
		return 0, fmt.Errorf("CreateDevice: %w", err)
	}
	return id, nil
}

// 列举一个用户的所有设备列表
func ListDevices(userID uint64) ([]model.Device, error) {
	return repository.ListDevicesByOwner(userID)
}

// 从ID获得设备信息
func GetDevice(userID uint64, deviceID string) (*model.Device, error) {
	device, err := repository.GetDeviceByDeviceID(deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("设备不存在")
		}
		return nil, fmt.Errorf("GetDevice: %w", err)
	}
	if device.OwnerID != userID {
		return nil, errors.New("无权操作该设备")
	}
	return device, nil
}

func UpdateDevice(userID uint64, deviceID string, req *model.UpdateDeviceReq) error {
	device, err := repository.GetDeviceByDeviceID(deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("该设备不存在")
		}
		return fmt.Errorf("UpdateDevice: %w", err)
	}
	if device.OwnerID != userID {
		return errors.New("无权操作该设备")
	}
	return repository.UpdateDevice(deviceID, req.Name, req.Room)
}

func BindDevice(userID uint64, deviceID string) error {
	device, err := repository.GetDeviceByDeviceID(deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("设备不存在")
		}
		return fmt.Errorf("BindDevice: %w", err)
	}
	if device.OwnerID != 0 && device.OwnerID != userID {
		return errors.New("设备已被他人绑定")
	}
	return repository.BindDevice(deviceID, userID)
}

func UnbindDevice(userID uint64, deviceID string) error {
	device, err := repository.GetDeviceByDeviceID(deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("设备不存在")
		}
		return fmt.Errorf("UnbindDevice: %w", err)
	}
	if device.OwnerID != userID {
		return errors.New("无权操作该设备")
	}
	return repository.UnbindDevice(deviceID)
}
