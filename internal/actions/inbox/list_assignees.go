//go:build server

package inbox

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ServiceAssignee 定义可用于客服筛选的企业身份。
type ServiceAssignee struct {
	IdentityID   string                          `bun:"identity_id"`
	Type         domain.OrganizationIdentityType `bun:"type"`
	DisplayName  string                          `bun:"display_name"`
	AvatarFileID *string                         `bun:"avatar_file_id"`
}

// ListServiceAssigneesQuery 读取有效客服身份。
type ListServiceAssigneesQuery struct{ db *bun.DB }

// NewListServiceAssigneesQuery 创建有效客服身份查询。
func NewListServiceAssigneesQuery(db *bun.DB) *ListServiceAssigneesQuery {
	return &ListServiceAssigneesQuery{db: db}
}

// Execute 返回开启接待且账号有效的真人和 AI 员工。
func (q *ListServiceAssigneesQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]ServiceAssignee, error) {
	identities, err := identityaction.ListActiveServiceHandlingIdentities(ctx, q.db, identity.Organization.ID)
	if err != nil {
		return nil, fmt.Errorf("list customer service assignees: %w", err)
	}
	assignees := make([]ServiceAssignee, 0, len(identities))
	for _, item := range identities {
		assignees = append(assignees, ServiceAssignee{
			IdentityID: item.ID, Type: domain.OrganizationIdentityType(item.Type),
			DisplayName: item.DisplayName, AvatarFileID: item.AvatarFileID,
		})
	}
	return assignees, nil
}
