package common

import (
	"crypto/sha1"
	"strings"

	"uuid"
)

// NewUUIDv5 按 RFC 9562 §5.5 生成命名空间内由名称确定的 UUID。
func NewUUIDv5(namespace uuid.UUID, name string) uuid.UUID {
	sum := sha1.Sum(append(namespace[:], name...))
	var result uuid.UUID
	copy(result[:], sum[:])
	result[6] = result[6]&0x0f | 0x50
	result[8] = result[8]&0x3f | 0x80
	return result
}

// ValidUUID 判断记录标识是否为规范 UUID。
func ValidUUID(value string) bool {
	_, valid := canonicalUUID(value)
	return valid
}

// NormalizeUUID 去除记录标识首尾空白并返回规范的小写 UUID。
func NormalizeUUID(value string) (string, bool) {
	return canonicalUUID(strings.TrimSpace(value))
}

// NormalizeUUIDs 规范化记录标识列表、保持顺序并去重。
func NormalizeUUIDs(values []string) ([]string, bool) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized, valid := NormalizeUUID(value)
		if !valid {
			return nil, false
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, true
}

// canonicalUUID 校验并返回规范的小写 UUID。
func canonicalUUID(value string) (string, bool) {
	parsed, err := uuid.Parse(value)
	if err != nil || !strings.EqualFold(parsed.String(), value) {
		return "", false
	}
	return parsed.String(), true
}
