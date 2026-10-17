// Package license 校验 control 签发的服务器授权码并解析其中的授权声明。
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/runforyou-ai/support/str"
)

const (
	// ProductID 是本产品在 control 中的产品标识。
	ProductID = "luway"
	// Issuer 是授权码的签发方。
	Issuer = "runforyou-control"
	// tokenType 是授权码头部的 typ 取值。
	tokenType = "license+jwt"
	// maxCodeLength 是授权码原文的最大字节数。
	maxCodeLength = 8192
)

// ErrInvalid 表示授权码格式、签名、签发方或产品无效。
var ErrInvalid = errors.New("license code invalid")

// Keys 是按 kid 索引的 control 签名公钥。
type Keys map[string]ed25519.PublicKey

// Claims 表示验签通过的授权声明；有效授权即解锁全部产品功能。
type Claims struct {
	// Code 是去除首尾空白后的授权码原文。
	Code         string
	LicenseID    string
	Customer     string
	DeploymentID string
	IssuedAt     time.Time
	ExpiresAt    time.Time
}

// tokenClaims 是授权码载荷。
type tokenClaims struct {
	jwt.RegisteredClaims
	LicenseID string `json:"license_id"`
	Customer  string `json:"customer"`
}

// DecodeKeys 把按 kid 索引的 base64url 公钥解码为签名公钥。
func DecodeKeys(encoded map[string]string) (Keys, error) {
	keys := make(Keys, len(encoded))
	for kid, value := range encoded {
		raw, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("license public key %q is invalid", kid)
		}
		keys[kid] = ed25519.PublicKey(raw)
	}
	return keys, nil
}

// Parse 按头部 kid 选择公钥校验 EdDSA 签名，并校验 typ、签发方、产品标识与必填声明；部署标识与有效期由调用方判断。
func Parse(code string, keys Keys) (Claims, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > maxCodeLength {
		return Claims{}, fmt.Errorf("%w: length out of range", ErrInvalid)
	}
	claims := &tokenClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithoutClaimsValidation(),
	)
	_, err := parser.ParseWithClaims(code, claims, func(token *jwt.Token) (any, error) {
		if token.Header["typ"] != tokenType {
			return nil, errors.New("unexpected token type")
		}
		kid, _ := token.Header["kid"].(string)
		key, ok := keys[kid]
		if !ok {
			return nil, fmt.Errorf("unknown signing key %q", kid)
		}
		return key, nil
	})
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if claims.Issuer != Issuer || len(claims.Audience) != 1 || claims.Audience[0] != ProductID {
		return Claims{}, fmt.Errorf("%w: issuer or audience mismatch", ErrInvalid)
	}
	deploymentID, ok := str.NormalizeUUID(claims.Subject)
	if !ok || !str.IsUUID(claims.LicenseID) || claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return Claims{}, fmt.Errorf("%w: required claims missing", ErrInvalid)
	}
	return Claims{
		Code: code, LicenseID: strings.ToLower(claims.LicenseID), Customer: strings.TrimSpace(claims.Customer), DeploymentID: deploymentID,
		IssuedAt: claims.IssuedAt.UTC(), ExpiresAt: claims.ExpiresAt.UTC(),
	}, nil
}
