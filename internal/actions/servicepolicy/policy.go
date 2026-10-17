//go:build server

// Package servicepolicy 管理按服务对象独立配置的工作区服务策略。
package servicepolicy

import (
	"context"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrAudienceInvalid 表示服务对象不属于客户或员工服务。
	ErrAudienceInvalid = errors.New("invalid service policy audience")
	// ErrForbidden 表示成员缺少对应服务对象的管理权限。
	ErrForbidden = errors.New("service policy management permission required")
)

// ValidAudience 判断服务策略支持的服务对象。
func ValidAudience(audience domain.ServiceAudience) bool {
	return audience == domain.ServiceAudienceCustomer || audience == domain.ServiceAudienceEmployee
}

// RequireManage 校验成员管理指定服务对象的权限。
func RequireManage(identity *servermodels.Identity, audience domain.ServiceAudience) error {
	if !ValidAudience(audience) {
		return ErrAudienceInvalid
	}
	permission := domain.PermissionCustomerServiceManage
	if audience == domain.ServiceAudienceEmployee {
		permission = domain.PermissionEmployeeServiceManage
	}
	if !identity.HasPermission(permission) {
		return ErrForbidden
	}
	return nil
}

// ForSession 按服务周期所属会话读取对应服务策略。
func ForSession(ctx context.Context, db bun.IDB, workspaceID, sessionID string) (*servermodels.ServicePolicy, error) {
	policy := &servermodels.ServicePolicy{}
	if err := db.NewSelect().Model(policy).Column("sp.*").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = sp.workspace_id AND svc.audience = sp.audience").
		Join("JOIN service_sessions AS ss ON ss.workspace_id = svc.workspace_id AND ss.service_conversation_id = svc.id").
		Where("sp.workspace_id = ? AND ss.id = ?", workspaceID, sessionID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("load session service policy: %w", err)
	}
	return policy, nil
}

// ForConversation 按服务会话的服务对象读取对应策略。
func ForConversation(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (*servermodels.ServicePolicy, error) {
	policy := &servermodels.ServicePolicy{}
	if err := db.NewSelect().Model(policy).Column("sp.*").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = sp.workspace_id AND svc.audience = sp.audience").
		Where("sp.workspace_id = ? AND svc.conversation_id = ?", workspaceID, conversationID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("load conversation service policy: %w", err)
	}
	return policy, nil
}

// Timeouts 返回策略中的服务超时时长。
func Timeouts(policy *servermodels.ServicePolicy) domain.ServiceTimeouts {
	return domain.ServiceTimeouts{ResponseReminderMinutes: policy.ResponseReminderMinutes, ResponseReclaimMinutes: policy.ResponseReclaimMinutes, QueueReminderMinutes: policy.QueueReminderMinutes, AIFollowUpMinutes: policy.AIFollowUpMinutes, AICloseMinutes: policy.AICloseMinutes}
}

// SummaryForSession 返回服务周期所属对象的小结设置。
func SummaryForSession(ctx context.Context, db bun.IDB, workspaceID, sessionID string) (domain.ServiceSummarySettings, error) {
	policy, err := ForSession(ctx, db, workspaceID, sessionID)
	if err != nil {
		return domain.ServiceSummarySettings{}, err
	}
	return domain.ServiceSummarySettings{DecisionModelID: policy.DecisionModelID, SummaryModelID: policy.SummaryModelID, Locale: domain.Locale(policy.SummaryLocale)}, nil
}
