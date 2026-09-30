//go:build server

package conversation

import (
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// AssistantOwnerName 返回别名企业身份为助理时其主人的名称，其他身份返回 NULL。
func AssistantOwnerName(alias string) schema.QueryWithArgs {
	return AssistantOwnerColumn(alias, "display_name")
}

// AssistantOwnerColumn 返回别名企业身份为助理时其主人企业身份的指定列，其他身份返回 NULL。
func AssistantOwnerColumn(alias, column string) schema.QueryWithArgs {
	name := bun.Ident(alias)
	return bun.SafeQuery(`(SELECT owner_oi.?::text FROM agents AS owner_a
 JOIN users AS owner_u ON owner_u.id = owner_a.owner_user_id AND owner_u.organization_id = owner_a.organization_id
 JOIN organization_identities AS owner_oi ON owner_oi.id = owner_u.identity_id AND owner_oi.organization_id = owner_u.organization_id
 WHERE owner_a.organization_id = ?.organization_id AND owner_a.identity_id = ?.id)`, bun.Ident(column), name, name)
}
