//go:build server

package chatstate

import (
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// PersonalResponsibleName 返回别名企业身份为个人 AI 员工时其负责人的名称，其他身份返回 NULL。
func PersonalResponsibleName(alias string) schema.QueryWithArgs {
	return PersonalResponsibleColumn(alias, "display_name")
}

// PersonalResponsibleColumn 返回别名企业身份为个人 AI 员工时其负责人企业身份的指定列，其他身份返回 NULL。
func PersonalResponsibleColumn(alias, column string) schema.QueryWithArgs {
	name := bun.Ident(alias)
	return bun.SafeQuery(`(SELECT responsible_oi.?::text FROM agents AS personal_a
 JOIN users AS responsible_u ON responsible_u.id = personal_a.responsible_user_id AND responsible_u.workspace_id = personal_a.workspace_id
 JOIN workspace_identities AS responsible_oi ON responsible_oi.id = responsible_u.identity_id AND responsible_oi.workspace_id = responsible_u.workspace_id
 WHERE personal_a.workspace_id = ?.workspace_id AND personal_a.identity_id = ?.id AND ? = ANY(personal_a.service_audiences))`, bun.Ident(column), name, name, domain.ServiceAudiencePersonal)
}
