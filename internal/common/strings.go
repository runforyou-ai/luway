package common

import "strings"

// likeEscaper 把 LIKE 模式中的反斜杠、百分号和下划线转义为字面字符。
var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// ContainsPattern 返回按字面包含关键词匹配的 LIKE 与 ILIKE 模式，关键词中的通配符按普通字符处理。
func ContainsPattern(keyword string) string {
	return "%" + likeEscaper.Replace(keyword) + "%"
}
