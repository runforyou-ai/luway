//go:build server

// Package contactname 提供联系人在成员界面的名称表达式与按编号检索的解析。
package contactname

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// numberQuery 匹配以编号结尾的检索词，编号前可带 #。
var numberQuery = regexp.MustCompile(`(?:^|#)\s*(\d+)$`)

// Expr 返回联系人在成员界面的名称表达式：档案名称、渠道身份名称、首选邮箱依次取第一个非空值，均为空时为 NULL；contact 是联系人表别名，identityName 是渠道身份名称的 SQL 表达式。
func Expr(contact, identityName string) string {
	return fmt.Sprintf(`COALESCE(NULLIF(btrim(%[1]s.display_name), ''), NULLIF(btrim(%[2]s), ''), %[1]s.primary_email)`, contact, identityName)
}

// LatestIdentityName 返回联系人最近更新且带名称的渠道身份名称的 SQL 表达式；contact 是联系人表别名。
func LatestIdentityName(contact string) string {
	return fmt.Sprintf(`(SELECT latest_cci.display_name FROM channel_identities AS latest_cci
		WHERE latest_cci.workspace_id = %[1]s.workspace_id AND latest_cci.contact_id = %[1]s.id AND btrim(latest_cci.display_name) <> ''
		ORDER BY latest_cci.updated_at DESC, latest_cci.id DESC LIMIT 1)`, contact)
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
