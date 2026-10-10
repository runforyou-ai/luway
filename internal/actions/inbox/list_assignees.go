//go:build server

package inbox

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// ServiceAssignee 定义可用于客服筛选的企业身份；ServiceAudiences 是 AI 员工服务的对象，真人成员为空。
type ServiceAssignee struct {
	IdentityID       string
	Type             domain.WorkspaceIdentityType
	DisplayName      string
	AvatarFileID     *string
	ServiceAudiences []domain.ServiceAudience
}

// ListServiceAssigneesQuery 读取有效客服身份。
type ListServiceAssigneesQuery struct{ db *bun.DB }

// NewListServiceAssigneesQuery 创建有效客服身份查询。
func NewListServiceAssigneesQuery(db *bun.DB) *ListServiceAssigneesQuery {
	return &ListServiceAssigneesQuery{db: db}
}

// Execute 返回开启接待且账号有效的真人，以及服务客户或员工的 AI 员工及其服务对象。
func (q *ListServiceAssigneesQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]ServiceAssignee, error) {
	identities, err := serviceroute.ListActiveServiceHandlingIdentities(ctx, q.db, identity.Workspace.ID, domain.ServiceAudienceCustomer, domain.ServiceAudienceEmployee)
	if err != nil {
		return nil, fmt.Errorf("list service assignees: %w", err)
	}
	var agents []struct {
		IdentityID       string                   `bun:"identity_id"`
		ServiceAudiences []domain.ServiceAudience `bun:"service_audiences,array"`
	}
	if err := q.db.NewSelect().TableExpr("agents").Column("identity_id", "service_audiences").
		Where("workspace_id = ?", identity.Workspace.ID).Scan(ctx, &agents); err != nil {
		return nil, fmt.Errorf("list service assignee audiences: %w", err)
	}
	audiences := make(map[string][]domain.ServiceAudience, len(agents))
	for _, agent := range agents {
		audiences[agent.IdentityID] = agent.ServiceAudiences
	}
	return arr.Map(identities, func(item servermodels.WorkspaceIdentity) ServiceAssignee {
		return ServiceAssignee{
			IdentityID: item.ID, Type: domain.WorkspaceIdentityType(item.Type),
			DisplayName: item.DisplayName, AvatarFileID: item.AvatarFileID, ServiceAudiences: audiences[item.ID],
		}
	}), nil
}
