package repository

import (
	"fmt"

	"github.com/SinnerK9/my-iot-server/internal/model"
)

func GetUserByPhone(phone string) (*model.User, error) {
	var user model.User
	// Get：查单行使用的高级封装，自动 StructScan（填到结构体里）。没找到返回 sql.ErrNoRows。
	//?是Go的占位符，并不是一般的字符串拼接，MySQL会先解析SQL的结构，再后填入参数，其不会被当作SQL关键字执行，防止了SQL注入的风险
	err := DB.Get(&user, "SELECT id,phone,email,password,nickname,created_at,updated_at FROM users WHERE phone=?", phone)
	if err != nil {
		return nil, fmt.Errorf("GetUserByPhone: %w", err)
	}
	return &user, nil
}

func GetUserByEmail(email string) (*model.User, error) {
	var user model.User
	err := DB.Get(&user, "SELECT id,phone,email,nickname,created_at,updated_at FROM users WHERE email=?", email)
	if err != nil {
		return nil, fmt.Errorf("GetUserByEmail: %w", err)
	}
	return &user, nil
}

func ListUsers() ([]model.User, error) {
	var users []model.User
	// Select：查多行，自动 append + StructScan 每行。
	err := DB.Select(&users, "SELECT id,phone,email,nickname,created_at FROM users ORDER BY id DESC")
	if err != nil {
		return nil, fmt.Errorf("ListUsers: %w", err)
	}
	return users, nil
}

// 传入用户结构体指针，将这个结构体里的信息写入表中
func CreateUser(user *model.User) (uint64, error) {
	// NamedExec：专门用于对SQL进行增删改，用 struct 字段名自动匹配 SQL 的 :field 占位符。
	result, err := DB.NamedExec(`INSERT INTO users (phone, email, password, nickname)
            VALUES (:phone, :email, :password, :nickname)`, user) //名字占位符，匹配结构体字段
	if err != nil {
		return 0, fmt.Errorf("CreateUser: %w", err)
	}
	id, _ := result.LastInsertId()
	return uint64(id), nil
}

func UpdateUser(id uint64, nickname string) error {
	// Exec：执行任意 SQL，返回 Result（RowsAffected + LastInsertId）。不需要返回值版本的NamedExec
	_, err := DB.Exec("UPDATE users SET nickname=? WHERE id=?", nickname, id)
	if err != nil {
		return fmt.Errorf("UpdateUser: %w", err)
	}
	return nil
}

func DeleteUser(id uint64) error {
	_, err := DB.Exec("DELETE FROM users WHERE id=?", id)
	return err
}
