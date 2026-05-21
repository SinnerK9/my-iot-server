package jwtutil

import "testing"

func TestGenerateAndParse(t *testing.T) {
	Init("test-secret")

	// 生成
	token, err := GenerateAccessToken(42)
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}

	// 验证
	claims, err := ParseToken(token)
	if err != nil {
		t.Fatalf("验证失败: %v", err)
	}

	if claims.UserID != 42 {
		t.Fatalf("userID 期望 42，实际 %d", claims.UserID)
	}
}

func TestTamperedToken(t *testing.T) {
	Init("test-secret")

	token, _ := GenerateAccessToken(1)
	// 改最后一个字符——模拟篡改
	tampered := token[:len(token)-1] + "X"

	_, err := ParseToken(tampered)
	if err == nil {
		t.Fatal("篡改后的 token 应该验证失败")
	}
}
