package common

import "strings"

// likeEscaper 把 LIKE 模式中的反斜杠、百分号和下划线转义为字面字符。
var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// OptionalString 把空字符串转换为空指针，用于写入数据库可空列。
func OptionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// StringValue 把可空字符串指针转换为字符串，空指针返回空串。
func StringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// ContainsPattern 返回按字面包含关键词匹配的 LIKE 与 ILIKE 模式，关键词中的通配符按普通字符处理。
func ContainsPattern(keyword string) string {
	return "%" + likeEscaper.Replace(keyword) + "%"
}
