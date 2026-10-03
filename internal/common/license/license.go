// Package license 校验 control 签发的服务器授权码并解析其中的授权声明与能力清单。
package license

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/runforyou-ai/luway/internal/common"
)

const (
	// ProductID 是本产品在 control 与 commerce 中的产品标识。
	ProductID = "luway"
	// Issuer 是授权码的签发方。
	Issuer = "runforyou-control"
	// tokenType 是授权码头部的 typ 取值。
	tokenType = "license+jwt"
	// maxCodeLength 是授权码原文的最大字节数。
	maxCodeLength = 8192
)

// 能力清单中由本产品执行的能力键。
const (
	CapabilityWorkspaceLimit = "workspace.limit"
	CapabilityCustomBranding = "branding.custom"
)

// ErrInvalid 表示授权码格式、签名、签发方、产品或能力清单无效。
var ErrInvalid = errors.New("license code invalid")

// Keys 是按 kid 索引的 control 签名公钥。
type Keys map[string]ed25519.PublicKey

// Claims 表示验签通过的授权声明；Capabilities 只含与免费取值不同的能力键。
type Claims struct {
	// Code 是去除首尾空白后的授权码原文。
	Code         string
	LicenseID    string
	Customer     string
	ServerID     string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	Capabilities map[string]json.RawMessage
}

// tokenClaims 是授权码载荷。
type tokenClaims struct {
	jwt.RegisteredClaims
	LicenseID    string                     `json:"license_id"`
	Customer     string                     `json:"customer"`
	Capabilities map[string]json.RawMessage `json:"capabilities"`
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

// Parse 按头部 kid 选择公钥校验 EdDSA 签名，并校验 typ、签发方、产品标识、必填声明和能力取值；服务器标识与有效期由调用方判断。
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
	serverID, ok := common.NormalizeUUID(claims.Subject)
	if !ok || !common.ValidUUID(claims.LicenseID) || claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return Claims{}, fmt.Errorf("%w: required claims missing", ErrInvalid)
	}
	if err := validateCapabilities(claims.Capabilities); err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	capabilities := claims.Capabilities
	if capabilities == nil {
		capabilities = map[string]json.RawMessage{}
	}
	return Claims{
		Code: code, LicenseID: strings.ToLower(claims.LicenseID), Customer: strings.TrimSpace(claims.Customer), ServerID: serverID,
		IssuedAt: claims.IssuedAt.UTC(), ExpiresAt: claims.ExpiresAt.UTC(), Capabilities: capabilities,
	}, nil
}

// validateCapabilities 校验本产品执行的能力键取值类型，其余能力键原样保留。
func validateCapabilities(capabilities map[string]json.RawMessage) error {
	if raw, ok := capabilities[CapabilityWorkspaceLimit]; ok {
		if _, err := WorkspaceLimit(raw); err != nil {
			return err
		}
	}
	if raw, ok := capabilities[CapabilityCustomBranding]; ok {
		if _, err := CustomBranding(raw); err != nil {
			return err
		}
	}
	return nil
}

// WorkspaceLimit 解析 workspace.limit 能力取值，0 表示不限。
func WorkspaceLimit(raw json.RawMessage) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return 0, fmt.Errorf("%s must be an integer", CapabilityWorkspaceLimit)
	}
	number, _ := decoded.(json.Number)
	value, err := number.Int64()
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", CapabilityWorkspaceLimit)
	}
	return int(value), nil
}

// CustomBranding 解析 branding.custom 能力取值。
func CustomBranding(raw json.RawMessage) (bool, error) {
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("%s must be a boolean", CapabilityCustomBranding)
	}
	return value, nil
}
