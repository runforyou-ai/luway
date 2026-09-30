//go:build server

package servicesession

import (
	"context"
	"fmt"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// rejectServiceRequester 在企业身份是服务会话发起人时返回冲突，发起人不能领取、承接或以处理方身份回复自己的请求。
func rejectServiceRequester(ctx context.Context, db bun.IDB, service *servermodels.ServiceConversation, identityID string) error {
	requester, err := db.NewSelect().TableExpr("chat_subjects AS cs").
		Where("cs.organization_id = ? AND cs.id = ? AND cs.kind = ? AND cs.source_id = ?", service.OrganizationID, service.RequesterSubjectID, domain.ChatSubjectKindOrganizationIdentity, identityID).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check service requester: %w", err)
	}
	if requester {
		return &conversationaction.ConflictError{Reason: ConflictReasonServiceSessionOwnRequest}
	}
	return nil
}
