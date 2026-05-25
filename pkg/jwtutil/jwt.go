package jwtutil

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

//jwt.go的构建方法取决于这个包的构建目的：这个包需要管理token的生成和验证，因此需要三个暴露在外的接口
//生成access和refresh token，以及token的验证

// 双token：这样的设计用于解决一个矛盾：
// 对于单token的情况，只有一个15min的token会导致频繁鉴权，只有一个7day的token会导致被盗用后影响巨大
const (
	AccessTTL  = 15 * time.Minute
	RefreshTTL = 7 * 24 * time.Hour //
)

var secret []byte // 包级变量，Init() 之后全局可用

// 在main启动时调用，设置签名密钥
func Init(secretKey string) {
	secret = []byte(secretKey)
}

// 自定义JWT载荷，嵌入jwt.RegisteredClaims，类似cpp的继承，但是效果上表现为直接获得对应的字段
// 构造思路：token里的信息，应当足以验证身份，但是在这个基础上越少越好，减小保存的负担，因此只有UserID
// 在UserID之外，还应当有token的两个属性：什么时候过期，什么时候签发，这两个属性从jwt.RegisteredClaims中来
type Claims struct {
	UserID uint64 `json:"user_id"`
	jwt.RegisteredClaims
}

func GenerateAccessToken(userID uint64) (string, error) {
	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(AccessTTL)), //NewNumericDate用于将time转化为jwt自己的NumericDate类型，用于JSON序列化成符合jwt的exp字段要求的形式
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims) //把 Header+Payload(Claims)+Method 打包成一个 jwt.Token 对象，尚未签名
	return token.SignedString(secret)                          //真正产生签名并拼接成整个Token的算法，payload谁都能看，但是签名只有服务器能够产生
}

func GenerateRefreshToken(userID uint64) (string, error) {
	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(RefreshTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// token验证函数，接收token字符串并输出验证后的claims
func ParseToken(tokenString string) (*Claims, error) {
	//传入客户端传来的tokenstring和空Claims实例指针，用于让库读取器字段名和类型信息，进行Payload的JSON反序列化
	//传入一个回调匿名函数，得到密钥
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims) //使用类型断言从interface中将真正的指针取出来
	//Payload反序列化错误/token整体不有效（签名不对/过期）
	if !ok || !token.Valid {
		return nil, jwt.ErrSignatureInvalid
	}
	return claims, nil
}
