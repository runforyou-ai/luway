// Package wechat 提供微信开放平台与公众号接口的签名、加解密、消息解析与 HTTP 调用。
package wechat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/runforyou-ai/support/random"
)

var (
	// ErrSignatureInvalid 表示推送签名与消息校验 Token 计算结果不一致。
	ErrSignatureInvalid = errors.New("wechat signature invalid")
	// ErrCiphertextInvalid 表示加密消息无法解密或解密后的结构不完整。
	ErrCiphertextInvalid = errors.New("wechat ciphertext invalid")
	// ErrReceiverMismatch 表示解密后的接收方 AppID 与预期不一致。
	ErrReceiverMismatch = errors.New("wechat receiver mismatch")

	// encodingAESKeyPattern 与 appIDPattern 分别是 EncodingAESKey 与 AppID 的格式。
	encodingAESKeyPattern = regexp.MustCompile(`^[A-Za-z0-9]{43}$`)
	appIDPattern          = regexp.MustCompile(`^wx[0-9a-f]{16}$`)
)

// pkcs7BlockSize 是微信消息加解密使用的 PKCS#7 填充块长度。
const pkcs7BlockSize = 32

// ValidAppID 判断字符串是否为微信 AppID 格式。
func ValidAppID(value string) bool {
	return appIDPattern.MatchString(value)
}

// ValidEncodingAESKey 判断 EncodingAESKey 是否为 43 位字母与数字。
func ValidEncodingAESKey(value string) bool {
	return encodingAESKeyPattern.MatchString(value)
}

// Signature 按微信规则把参数字典序排序拼接后计算 SHA-1 十六进制摘要。
func Signature(parts ...string) string {
	sorted := append([]string(nil), parts...)
	sort.Strings(sorted)
	sum := sha1.Sum([]byte(strings.Join(sorted, "")))
	return hex.EncodeToString(sum[:])
}

// Cipher 使用消息校验 Token 与 EncodingAESKey 校验签名并加解密推送消息，AppID 为消息接收方。
type Cipher struct {
	token string
	appID string
	key   []byte
}

// NewCipher 创建消息加解密器。
func NewCipher(token, encodingAESKey, appID string) (*Cipher, error) {
	if !ValidEncodingAESKey(encodingAESKey) {
		return nil, fmt.Errorf("encoding aes key invalid")
	}
	key, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		return nil, fmt.Errorf("decode encoding aes key: %w", err)
	}
	return &Cipher{token: token, appID: appID, key: key}, nil
}

// VerifyEncrypted 校验加密推送的 msg_signature。
func (c *Cipher) VerifyEncrypted(signature, timestamp, nonce, encrypted string) error {
	expected := Signature(c.token, timestamp, nonce, encrypted)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
		return ErrSignatureInvalid
	}
	return nil
}

// Decrypt 解密 Base64 密文并校验接收方 AppID，返回消息明文。
func (c *Cipher) Decrypt(encrypted string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil || len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, ErrCiphertextInvalid
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, c.key[:aes.BlockSize]).CryptBlocks(plain, data)
	// 去掉 PKCS#7 填充。
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > pkcs7BlockSize || padding > len(plain) {
		return nil, ErrCiphertextInvalid
	}
	plain = plain[:len(plain)-padding]
	// 明文结构为 16 字节随机串、4 字节网络字节序消息长度、消息与接收方 AppID。
	if len(plain) < 20 {
		return nil, ErrCiphertextInvalid
	}
	size := int(binary.BigEndian.Uint32(plain[16:20]))
	if size > len(plain)-20 {
		return nil, ErrCiphertextInvalid
	}
	message, receiver := plain[20:20+size], plain[20+size:]
	if subtle.ConstantTimeCompare(receiver, []byte(c.appID)) != 1 {
		return nil, ErrReceiverMismatch
	}
	return message, nil
}

// Encrypt 按微信规则加密消息并返回 Base64 密文。
func (c *Cipher) Encrypt(message []byte) (string, error) {
	plain := bytes.NewBuffer(random.Bytes(16))
	_ = binary.Write(plain, binary.BigEndian, uint32(len(message)))
	plain.Write(message)
	plain.WriteString(c.appID)
	padding := pkcs7BlockSize - plain.Len()%pkcs7BlockSize
	plain.Write(bytes.Repeat([]byte{byte(padding)}, padding))
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", fmt.Errorf("create aes cipher: %w", err)
	}
	data := plain.Bytes()
	cipher.NewCBCEncrypter(block, c.key[:aes.BlockSize]).CryptBlocks(data, data)
	return base64.StdEncoding.EncodeToString(data), nil
}
