// Package token 提供随机令牌的签发和哈希。
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"
)

// Issued 表示新签发的令牌明文、哈希和过期时间。
type Issued struct {
	Token     string
	TokenHash string
	ExpiresAt time.Time
}

// Issue 签发 256 位随机令牌，过期时间为当前时间加上有效期。
func Issue(validity time.Duration) (Issued, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return Issued{}, err
	}
	value := base64.RawURLEncoding.EncodeToString(buffer)
	return Issued{
		Token:     value,
		TokenHash: Hash(value),
		ExpiresAt: time.Now().Add(validity),
	}, nil
}

// Hash 计算令牌的 SHA-256 哈希。
func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
