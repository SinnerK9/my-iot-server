package hashutil

import "golang.org/x/crypto/bcrypt"

const cost = 10 //bcrypt工作因子
// 将明文密码转为哈希密文，数据库里不存明文
func HashPassword(plain string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(plain), cost)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// 比对明文密码和哈希，返回密码是否正确
func CheckPassword(plain, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
	return err == nil
}
