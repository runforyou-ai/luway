//go:build server

package contact

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/runforyou-ai/luway/internal/actions/contactprofile"
)

// numberQuery 匹配以编号结尾的检索词，编号前可带 #。
var numberQuery = regexp.MustCompile(`(?:^|#)\s*(\d+)$`)

// Expr 返回联系人在会话上下文中的 LIVE 名称表达式（档案名 / 当前渠道身份名 / 首选邮箱）；实现见 contactprofile.NameExpr。
// 目录、搜人与 Agent 上下文应使用 contactprofile.ResolvedColumn（DENORM），勿与 LIVE 混用。
func Expr(contact, identityName string) string {
	return contactprofile.NameExpr(contact, identityName)
}

// LatestIdentityName 返回联系人最近更新且带名称的渠道身份名称的 SQL 表达式；实现见 contactprofile.LatestIdentityName。
func LatestIdentityName(contact string) string {
	return contactprofile.LatestIdentityName(contact)
}

// Number 从检索词末尾取出联系人编号，检索词为纯数字或以「#数字」结尾时返回编号，否则返回 nil；nil 作为 SQL 参数时不匹配任何联系人。
func Number(query string) *int64 {
	match := numberQuery.FindStringSubmatch(strings.TrimSpace(query))
	if match == nil {
		return nil
	}
	number, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return nil
	}
	return &number
}
