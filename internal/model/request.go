package model

// 用struct tag给JSON库和validator读，json:后的由json库用于把JSON和Go对应，binding后的给Gin解析请求时读，给出特定的需求
type LoginReq struct {
	Account  string `json:"account" binding:"required"`
	Password string `json:"password" binding:"required,min=8"`
}
type RegisterReq struct {
	Phone    string `json:"phone" binding:"required,len=11"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8,max=20"`
	Nickname string `json:"nickname" binding:"required,min=1,max=20"`
}
