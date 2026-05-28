package handler

import (
	"log/slog"

	"github.com/SinnerK9/my-iot-server/internal/model"
	"github.com/SinnerK9/my-iot-server/internal/service"
	"github.com/gin-gonic/gin"
)

func getUserID(c *gin.Context)  (uint64,bool){
	raw,exists := c.Get("userID")
	if !exists{
		model.Fail(c,4010,"未登录")
		return 0,false
	}
	userID,ok := raw.(uint64)
	if !ok{
		slog.Error("userID类型断言失败","raw",raw)
		model.Fail(c,5000,"服务器内部错误")
		return 0,false
	}
	return userID,true
}

func ListDevices(c *gin.Context){
	userID,ok :=  getUserID(c)
	if !ok {
		return
	}
	devices,err := service.ListDevices(userID)
	if err != nil{
		slog.Error("ListDevices 失败","err",err)
		model.Fail(c,5000,"服务器内部错误")
		return
	}
	model.OK(c,devices)
}

func CreateDevice(c *gin.Context){
	userID,ok := getUserID(c)
	if !ok{
		return
	}

	var req model.CreateDeviceReq
	if err := c.ShouldBindJSON(&req); err != nil{
		model.Fail(c,4001,"参数错误" + err.Error())
		return
	} 
	id,err := service.CreateDevice(userID,&req)
	if err != nil{
		if isBusinessError(err){
			model.Fail(c,4001,err.Error())
			return
		}
		slog.Error("CreateDevice 失败","err",err)
		model.Fail(c,5000,"服务器内部错误")
		return
	}
	model.OK(c,gin.H{"id": id})
}

func GetDevice(c *gin.Context){
	userID, ok := getUserID(c)
	if !ok{
		return
	}
	//从路由里得到deviceID
	deviceID := c.Param("device_id")
	if deviceID == ""{
		model.Fail(c,4001,"缺少device_id")
		return
	}

device , err := service.GetDevice(userID,deviceID)
	if err != nil{
		if isBusinessError(err){
			model.Fail(c,4030,err.Error())
			return
		}
		slog.Error("GetDevice失败","err",err)
		model.Fail(c,5000,"服务器内部错误")
		return
	}
	model.OK(c,device)
}

func UpdateDevice(c *gin.Context){
	userID,ok := getUserID(c)
	if !ok{
		return
	}

	deviceID := c.Param("device_id")
	var req model.UpdateDeviceReq
	if err := c.ShouldBindJSON(&req); err != nil{
		model.Fail(c,4001,"参数错误: " + err.Error())
		return
	}
	if err := service.UpdateDevice(userID, deviceID, &req); err != nil {
		if isBusinessError(err){
			model.Fail(c,4030,err.Error())
			return
		}
		slog.Error("UpdateDevice失败: ","err",err)
		model.Fail(c,5000,"服务器内部错误")
		return
	}
	model.OK(c,nil)
}

func BindDevice(c *gin.Context) {
    userID, ok := getUserID(c)
    if !ok {
            return
    }

    deviceID := c.Param("device_id")
    if err := service.BindDevice(userID, deviceID); err != nil {
            if isBusinessError(err) {
                    model.Fail(c, 4090, err.Error())
                    return
            }
            slog.Error("BindDevice 失败", "err", err)
            model.Fail(c, 5000, "服务器内部错误")
            return
    }
    model.OK(c, nil)
}

  func UnbindDevice(c *gin.Context) {
    userID, ok := getUserID(c)
    if !ok {
            return
    }

    deviceID := c.Param("device_id")
    if err := service.UnbindDevice(userID, deviceID); err != nil {
            if isBusinessError(err) {
                    model.Fail(c, 4030, err.Error())
                    return
            }
            slog.Error("UnbindDevice 失败", "err", err)
            model.Fail(c, 5000, "服务器内部错误")
            return
    }
    model.OK(c, nil)
}