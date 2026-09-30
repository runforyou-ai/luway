package customeridentity

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// sign 以给定算法和载荷签发测试用签名身份。
func sign(t *testing.T, method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestVerify(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 43 {
		t.Fatalf("secret length = %d, want 43", len(secret))
	}
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	valid := func(overrides jwt.MapClaims) jwt.MapClaims {
		claims := jwt.MapClaims{"sub": "user-42", "exp": now.Add(time.Hour).Unix(), "name": "  Ada  ", "email": " Ada@Example.com "}
		for key, value := range overrides {
			if value == nil {
				delete(claims, key)
				continue
			}
			claims[key] = value
		}
		return claims
	}

	claims, err := Verify(secret, sign(t, jwt.SigningMethodHS256, []byte(secret), valid(nil)), now)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != "user-42" || claims.Name != "Ada" || claims.Email != "ada@example.com" || !claims.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("claims = %#v", claims)
	}

	claims, err = Verify(secret, sign(t, jwt.SigningMethodHS256, []byte(secret), valid(jwt.MapClaims{"email": "not-an-email", "name": strings.Repeat("名", 200)})), now)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Email != "" || len([]rune(claims.Name)) != maxNameLength {
		t.Fatalf("claims = %#v", claims)
	}

	// 可选字段类型不符时忽略该字段，身份仍然有效。
	claims, err = Verify(secret, sign(t, jwt.SigningMethodHS256, []byte(secret), valid(jwt.MapClaims{"name": 42, "email": true})), now)
	if err != nil || claims.Name != "" || claims.Email != "" {
		t.Fatalf("claims = %#v, err = %v", claims, err)
	}
	if claims.Attributes != nil || claims.Tags != nil {
		t.Fatalf("absent profile claims = %#v", claims)
	}

	// 字段取值保留字符串、数字原文与 null，标签只保留非空字符串。
	claims, err = Verify(secret, sign(t, jwt.SigningMethodHS256, []byte(secret), valid(jwt.MapClaims{
		"attributes": map[string]any{" 套餐 ": " 专业版 ", "席位": 12345678901234567, "到期": nil, "付费": true, " ": "x"},
		"tags":       []any{" VIP ", "", 3},
	})), now)
	if err != nil {
		t.Fatal(err)
	}
	wantAttributes := map[string]string{"套餐": "专业版", "席位": "12345678901234567", "到期": ""}
	if len(claims.Attributes) != len(wantAttributes) {
		t.Fatalf("attributes = %#v", claims.Attributes)
	}
	for name, value := range wantAttributes {
		if got, ok := claims.Attributes[name]; !ok || got != value {
			t.Fatalf("attributes = %#v", claims.Attributes)
		}
	}
	if len(claims.Tags) != 1 || claims.Tags[0] != "VIP" {
		t.Fatalf("tags = %#v", claims.Tags)
	}

	// 空标签数组表示网站不再给出标签。
	claims, err = Verify(secret, sign(t, jwt.SigningMethodHS256, []byte(secret), valid(jwt.MapClaims{"tags": []any{}})), now)
	if err != nil || claims.Tags == nil || len(claims.Tags) != 0 {
		t.Fatalf("claims = %#v, err = %v", claims, err)
	}

	rejected := map[string]string{
		"wrong secret":      sign(t, jwt.SigningMethodHS256, []byte("other"), valid(nil)),
		"hs512":             sign(t, jwt.SigningMethodHS512, []byte(secret), valid(nil)),
		"none":              sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, valid(nil)),
		"missing exp":       sign(t, jwt.SigningMethodHS256, []byte(secret), valid(jwt.MapClaims{"exp": nil})),
		"expired":           sign(t, jwt.SigningMethodHS256, []byte(secret), valid(jwt.MapClaims{"exp": now.Add(-2 * time.Minute).Unix()})),
		"lifetime too long": sign(t, jwt.SigningMethodHS256, []byte(secret), valid(jwt.MapClaims{"exp": now.Add(25 * time.Hour).Unix()})),
		"missing sub":       sign(t, jwt.SigningMethodHS256, []byte(secret), valid(jwt.MapClaims{"sub": nil})),
		"sub with newline":  sign(t, jwt.SigningMethodHS256, []byte(secret), valid(jwt.MapClaims{"sub": "a\r\nX-Injected: 1"})),
		"sub too long":      sign(t, jwt.SigningMethodHS256, []byte(secret), valid(jwt.MapClaims{"sub": strings.Repeat("a", 129)})),
		"malformed":         "not-a-token",
	}
	for name, token := range rejected {
		if _, err := Verify(secret, token, now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}

	// 时钟偏差内刚过期的签名仍然有效。
	if _, err := Verify(secret, sign(t, jwt.SigningMethodHS256, []byte(secret), valid(jwt.MapClaims{"exp": now.Add(-30 * time.Second).Unix()})), now); err != nil {
		t.Fatalf("within clock skew: %v", err)
	}
}
