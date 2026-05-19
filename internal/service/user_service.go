package service

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/SinnerK9/my-iot-server/internal/model"
	"github.com/SinnerK9/my-iot-server/internal/repository"
	"github.com/SinnerK9/my-iot-server/pkg/hashutil"
)

// Register处理注册业务逻辑，返回新创建的用户id
func Register(req *model.RegisterReq) (uint64, error) {
	existing, err := repository.GetUserByPhone(req.Phone)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("Register: %w", err)
	}
	if existing != nil {
		return 0, errors.New("手机号已被注册")
	}
	//检测邮箱是否已被注册
	existing, err = repository.GetUserByEmail(req.Email)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("Register: %w", err)
	}
	if existing != nil {
		return 0, errors.New("邮箱已被注册")
	}
	//哈希密码
	hashed, err := hashutil.HashPassword(req.Password)
	if err != nil {
		return 0, fmt.Errorf("hash password: %w", err)
	}
	//写入结构体指针，用于后续创建用户
	user := &model.User{
		Phone:    req.Phone,
		Email:    req.Email,
		Password: hashed,
		Nickname: req.Nickname,
	}
	id, err := repository.CreateUser(user)
	if err != nil {
		return 0, fmt.Errorf("create user: %w", err)
	}
	return id, nil
}

// Login处理登录请求：传入账户密码，先判断是邮箱还是手机号，再看密码是否可以对应
func Login(req *model.LoginReq) (*model.User, error) {
	var user *model.User
	var err error

	user, err = repository.GetUserByPhone(req.Account)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("Login: %w", err)
	}
	if user == nil {
		// 手机号没找到，尝试邮箱
		user, err = repository.GetUserByEmail(req.Account)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("Login: %w", err)
		}
	}
	if user == nil {
		return nil, errors.New("账号或密码错误")
	}
	//验证密码
	if !hashutil.CheckPassword(req.Password, user.Password) {
		return nil, errors.New("账号或密码错误")
	}
	return user, nil
}
