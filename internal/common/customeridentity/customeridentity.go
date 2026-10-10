// Package customeridentity 校验企业网站签发的登录用户签名身份。
package customeridentity

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/runforyou-ai/support/str"
)

const (
	// MaxLifetime 是签名身份从校验时刻到过期时间的最长间隔。
	MaxLifetime = 24 * time.Hour
	// ClockSkew 是校验过期时间与有效期上限时允许的时钟偏差。
	ClockSkew = time.Minute
	// maxUserIDLength 是企业用户编号的最大字符数。
	maxUserIDLength = 128
	// maxNameLength 是显示名称保留的最大字符数。
	maxNameLength = 128
)

// ErrInvalid 表示签名身份无效、过期或超出有效期上限。
var ErrInvalid = errors.New("customer identity invalid")

// Claims 表示验签通过的登录用户身份；Attributes 以字段名称为键，取值为空表示清除，Tags 为 nil 表示载荷未提供标签；ChannelID 与 ExternalID 是渠道身份断言绑定的渠道编号与对方在渠道中的外部编号，载荷未提供时为空。
type Claims struct {
	UserID     string
	Name       string
	Email      string
	ChannelID  string
	ExternalID string
	Attributes map[string]string
	Tags       []string
	ExpiresAt  time.Time
}

// tokenClaims 是签名身份载荷，可选字段类型不符时按缺省处理。
type tokenClaims struct {
	jwt.RegisteredClaims
	Name       any `json:"name"`
	Email      any `json:"email"`
	Attributes any `json:"attributes"`
	Tags       any `json:"tags"`
	ChannelID  any `json:"channel_id"`
	ExternalID any `json:"external_id"`
}

// Verify 以密钥字符串的 UTF-8 字节按 HS256 校验签名身份：exp 必填且距 now 不超过有效期上限，sub 为合法企业用户编号；name 超长时截断，name 与 email 类型不符或 email 非法时忽略；attributes 只保留字符串、数字和 null 取值，null 记为空值；tags 只保留非空字符串；channel_id 与 external_id 只接受字符串。
func Verify(secret, token string, now time.Time) (Claims, error) {
	claims := &tokenClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(ClockSkew),
		jwt.WithTimeFunc(func() time.Time { return now }),
		jwt.WithJSONNumber(),
	)
	if _, err := parser.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return []byte(secret), nil }); err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	expiresAt := claims.ExpiresAt.Time
	if expiresAt.Sub(now) > MaxLifetime+ClockSkew {
		return Claims{}, fmt.Errorf("%w: lifetime exceeds %s", ErrInvalid, MaxLifetime)
	}
	if !ValidUserID(claims.Subject) {
		return Claims{}, fmt.Errorf("%w: subject is invalid", ErrInvalid)
	}
	result := Claims{UserID: claims.Subject, ExpiresAt: expiresAt}
	// 渠道身份断言的绑定目标只接受字符串。
	result.ChannelID, _ = claims.ChannelID.(string)
	result.ExternalID, _ = claims.ExternalID.(string)
	// 显示名称去除首尾空白并按字符数截断。
	name, _ := claims.Name.(string)
	result.Name = str.Substr(strings.TrimSpace(name), 0, maxNameLength)
	address, _ := claims.Email.(string)
	if address = strings.ToLower(strings.TrimSpace(address)); address != "" && str.IsEmail(address) {
		result.Email = address
	}
	// 字段取值按原文保留数字文本，其他类型忽略。
	if attributes, ok := claims.Attributes.(map[string]any); ok {
		result.Attributes = make(map[string]string, len(attributes))
		for name, raw := range attributes {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			switch value := raw.(type) {
			case nil:
				result.Attributes[name] = ""
			case string:
				result.Attributes[name] = strings.TrimSpace(value)
			case json.Number:
				result.Attributes[name] = value.String()
			}
		}
	}
	if tags, ok := claims.Tags.([]any); ok {
		result.Tags = make([]string, 0, len(tags))
		for _, raw := range tags {
			if tag, ok := raw.(string); ok && strings.TrimSpace(tag) != "" {
				result.Tags = append(result.Tags, strings.TrimSpace(tag))
			}
		}
	}
	return result, nil
}

// ValidUserID 判断企业用户编号为 1–128 个字符，且只含字母、数字和 -_.:@。
func ValidUserID(value string) bool {
	if value == "" || len(value) > maxUserIDLength {
		return false
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character >= '0' && character <= '9':
		case strings.ContainsRune("-_.:@", character):
		default:
			return false
		}
	}
	return true
}
