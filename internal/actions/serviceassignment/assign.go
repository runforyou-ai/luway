//go:build server

// Package serviceassignment 把队列中的客服处理周期自动分配给可接待的真人成员。
package serviceassignment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

const (
	// AssignActionName 为队列中的单个客服处理周期分配负责人。
	AssignActionName = "service_session.assign"
	// BackfillActionName 为单个成员从其所在队列补充分配等待中的客服处理周期。
	BackfillActionName = "service_session.backfill"
)

// taskMaxAttempts 是分配任务的最大尝试次数；执行时在锁内重新判断，重复执行得到相同结果。
const taskMaxAttempts = 3

// AssignInput 定义单个客服处理周期的分配任务；ExcludeIdentityID 非空时不分配给该成员。
type AssignInput struct {
	OrganizationID    string `json:"organizationId"`
	ServiceSessionID  string `json:"serviceSessionId"`
	ExcludeIdentityID string `json:"excludeIdentityId,omitempty"`
}

// BackfillInput 定义单个成员的补分配任务；ExcludeServiceSessionID 非空时不补入该周期。
type BackfillInput struct {
	OrganizationID          string `json:"organizationId"`
	IdentityID              string `json:"identityId"`
	ExcludeServiceSessionID string `json:"excludeServiceSessionId,omitempty"`
}

// Member 是已锁定并复核为可分配的真人成员。
type Member struct {
	IdentityID  string `bun:"identity_id"`
	UserID      string `bun:"user_id"`
	DisplayName string `bun:"display_name"`
}

// EnqueueAssign 在调用方事务中批量投递客服处理周期的分配任务。
func EnqueueAssign(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, inputs ...AssignInput) error {
	if len(inputs) == 0 {
		return nil
	}
	requests := make([]servertask.EnqueueRequest, len(inputs))
	for index, input := range inputs {
		requests[index] = servertask.EnqueueRequest{ActionName: AssignActionName, Payload: input, Options: servertask.EnqueueOptions{MaxAttempts: taskMaxAttempts}}
	}
	if _, err := enqueuer.EnqueueManyIn(ctx, db, requests); err != nil {
		return fmt.Errorf("enqueue service session assignment: %w", err)
	}
	return nil
}

// EnqueueBackfill 在调用方事务中投递成员的补分配任务。
func EnqueueBackfill(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, input BackfillInput) error {
	if _, err := enqueuer.EnqueueIn(ctx, db, BackfillActionName, input, servertask.EnqueueOptions{MaxAttempts: taskMaxAttempts}); err != nil {
		return fmt.Errorf("enqueue service session backfill: %w", err)
	}
	return nil
}

// assignableMemberQuery 构造企业内可分配真人成员的查询：开启接待、账号有效、工作中且接待量未满，按接待量、最近分配时间和身份编号排序。
func assignableMemberQuery(db bun.IDB, organizationID string) *bun.SelectQuery {
	load := "(SELECT count(*) FROM service_sessions AS ls WHERE ls.organization_id = oi.organization_id AND ls.assignee_identity_id = oi.id AND ls.status = '" + string(domain.ServiceSessionStatusOpen) + "')"
	return identityaction.ApplyServiceHandlingConditions(db.NewSelect().
		TableExpr("organization_identities AS oi").
		ColumnExpr("oi.id AS identity_id, u.id AS user_id, oi.display_name").
		Join("JOIN users AS u ON u.organization_id = oi.organization_id AND u.identity_id = oi.id").
		Where("oi.organization_id = ? AND oi.type = ? AND oi.work_status = ?", organizationID, domain.OrganizationIdentityTypeUser, domain.WorkStatusWorking).
		Where(load + " < u.max_service_sessions")).
		OrderExpr(load + " ASC, u.last_service_assigned_at ASC NULLS FIRST, oi.id ASC")
}

// candidateAttempts 是锁定后要求候选仍排在第一位的尝试次数，超过后只要求候选仍可分配。
const candidateAttempts = 5

// lockMember 锁定成员账号行后以新的语句快照复核其仍可分配；账号行是工作状态、接待开关与账号状态修改共同的串行点。
func lockMember(ctx context.Context, db bun.IDB, organizationID, userID string, scope func(*bun.SelectQuery) *bun.SelectQuery) (*Member, error) {
	if _, err := db.NewSelect().Model((*servermodels.User)(nil)).Column("u.id").
		Where("u.organization_id = ? AND u.id = ?", organizationID, userID).
		For("NO KEY UPDATE").Exec(ctx); err != nil {
		return nil, fmt.Errorf("lock assignable member: %w", err)
	}
	member := &Member{}
	err := scope(assignableMemberQuery(db, organizationID)).Where("u.id = ?", userID).Scan(ctx, member)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("recheck assignable member: %w", err)
	}
	return member, nil
}

// firstQueueMember 按分配排序读取指定范围内的第一位可分配成员，没有时返回 nil。
func firstQueueMember(ctx context.Context, db bun.IDB, organizationID string, scope func(*bun.SelectQuery) *bun.SelectQuery) (*Member, error) {
	member := &Member{}
	err := scope(assignableMemberQuery(db, organizationID)).Limit(1).Scan(ctx, member)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load assignable member: %w", err)
	}
	return member, nil
}

// LockQueueMember 为指定服务周期所在队列挑选并锁定接待量最少的可分配成员，teamID 为空表示公共队列，该周期的企业成员发起人不参与分配；调用方须在事务中且在进入会话锁之前调用，范围内没有可分配成员时返回 nil。
// 每次只持有一名成员的锁：锁定后以新的语句快照复核，候选不再排在第一位或已不可分配时回滚到保存点释放锁并重新挑选；尝试超过 candidateAttempts 次后接受仍可分配的候选。
func LockQueueMember(ctx context.Context, db bun.IDB, organizationID, serviceSessionID string, teamID *string, excludeIdentityID string) (*Member, error) {
	// 团队队列只分配给该团队成员，并排除指定成员与该服务周期的企业成员发起人。
	scope := func(query *bun.SelectQuery) *bun.SelectQuery {
		query = query.Where(`NOT EXISTS (
			SELECT 1 FROM service_sessions AS requested_ss
			JOIN service_conversations AS requested_svc ON requested_svc.organization_id = requested_ss.organization_id AND requested_svc.id = requested_ss.service_conversation_id
			JOIN chat_subjects AS requested_cs ON requested_cs.organization_id = requested_svc.organization_id AND requested_cs.id = requested_svc.requester_subject_id
			WHERE requested_ss.organization_id = oi.organization_id AND requested_ss.id = ? AND requested_cs.kind = ? AND requested_cs.source_id = oi.id)`,
			serviceSessionID, domain.ChatSubjectKindOrganizationIdentity)
		if teamID != nil {
			query = query.Where("EXISTS (SELECT 1 FROM team_members AS tm WHERE tm.organization_id = oi.organization_id AND tm.identity_id = oi.id AND tm.team_id = ?)", *teamID)
		}
		if excludeIdentityID != "" {
			query = query.Where("oi.id <> ?", excludeIdentityID)
		}
		return query
	}
	for attempt := 1; ; attempt++ {
		candidate, err := firstQueueMember(ctx, db, organizationID, scope)
		if err != nil || candidate == nil {
			return nil, err
		}
		if _, err := db.ExecContext(ctx, "SAVEPOINT service_assignment_candidate"); err != nil {
			return nil, fmt.Errorf("create assignment candidate savepoint: %w", err)
		}
		locked, err := lockMember(ctx, db, organizationID, candidate.UserID, scope)
		if err != nil {
			return nil, err
		}
		accepted := locked != nil && attempt >= candidateAttempts
		if locked != nil && !accepted {
			first, err := firstQueueMember(ctx, db, organizationID, scope)
			if err != nil {
				return nil, err
			}
			accepted = first != nil && first.UserID == locked.UserID
		}
		if accepted {
			if _, err := db.ExecContext(ctx, "RELEASE SAVEPOINT service_assignment_candidate"); err != nil {
				return nil, fmt.Errorf("release assignment candidate savepoint: %w", err)
			}
			return locked, nil
		}
		if _, err := db.ExecContext(ctx, "ROLLBACK TO SAVEPOINT service_assignment_candidate"); err != nil {
			return nil, fmt.Errorf("rollback assignment candidate savepoint: %w", err)
		}
	}
}

// Assign 在调用方持有成员锁与会话锁的事务中把队列中的服务周期分配给成员：写入负责人与 service_session_assigned 事件，为企业成员发起人写入处理中进度，并提醒该成员。
func Assign(ctx context.Context, db bun.IDB, locked chatstate.LockedServiceSession, member *Member) error {
	conversation, session := locked.Conversation, locked.Session
	source, err := chatstate.ServiceSessionQueueTarget(ctx, db, session)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := chatstate.AssignServiceSession(ctx, db, session, member.IdentityID, now); err != nil {
		return err
	}
	payload, err := json.Marshal(domain.ServiceSessionAssignedEvent{
		ServiceSessionID: session.ID,
		Target:           domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetMember, IdentityID: &member.IdentityID, DisplayName: &member.DisplayName},
		Source:           source,
	})
	if err != nil {
		return fmt.Errorf("encode service session assigned event: %w", err)
	}
	eventType := string(domain.ConversationSystemEventServiceSessionAssigned)
	if _, _, err := chatstate.AppendMessage(ctx, db, conversation, &servermodels.Message{
		ID: uuid.NewV7().String(), OrganizationID: session.OrganizationID, ConversationID: session.ConversationID,
		ServiceSessionID: &session.ID, Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityInternal),
		SystemEventType: &eventType, SystemEventPayload: payload, OriginatedAt: now,
	}); err != nil {
		return fmt.Errorf("append service session assigned event: %w", err)
	}
	processing := domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetMember, IdentityID: &member.IdentityID, DisplayName: &member.DisplayName}
	if _, err := chatstate.AppendRequesterStatus(ctx, db, conversation, session, locked.Source(), domain.ServiceRequestStatusProcessing, &processing, nil); err != nil {
		return err
	}
	if err := MarkAssigned(ctx, db, session, member); err != nil {
		return err
	}
	slog.Info("客服处理周期已自动分配",
		"organization_id", session.OrganizationID, "conversation_id", session.ConversationID,
		"service_session_id", session.ID, "assignee_identity_id", member.IdentityID, "source_kind", source.Kind)
	return nil
}

// MarkAssigned 记录成员最近一次被自动分配的时间，并在事务提交后提醒该成员处理新分配的客服处理周期。
func MarkAssigned(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession, member *Member) error {
	if _, err := db.NewUpdate().Model((*servermodels.User)(nil)).
		Set("last_service_assigned_at = now()").
		Where("organization_id = ? AND id = ?", session.OrganizationID, member.UserID).
		Exec(ctx); err != nil {
		return fmt.Errorf("record member last service assignment: %w", err)
	}
	realtime.Notify(ctx, realtime.UserServiceAttention(session.OrganizationID, member.UserID, session.ConversationID, session.ID, domain.ServiceAttentionAssigned))
	return nil
}
