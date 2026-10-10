//go:build server

package identity

import (
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// ApplyActiveMemberConditions 给以 oi 为别名的企业身份查询追加有效成员条件：真人成员账号有效，或服务客户、员工的 AI 员工有效。
func ApplyActiveMemberConditions(query *bun.SelectQuery) *bun.SelectQuery {
	return query.Where(`((oi.type = ? AND EXISTS (
			SELECT 1 FROM users AS mu WHERE mu.identity_id = oi.id AND mu.workspace_id = oi.workspace_id AND mu.status = ?))
		OR (oi.type = ? AND EXISTS (
			SELECT 1 FROM agents AS ma WHERE ma.identity_id = oi.id AND ma.workspace_id = oi.workspace_id AND ma.status = ?
				AND NOT ? = ANY(ma.service_audiences))))`,
		domain.WorkspaceIdentityTypeUser, domain.IdentityStatusActive, domain.WorkspaceIdentityTypeAgent, domain.IdentityStatusActive,
		domain.ServiceAudiencePersonal)
}
