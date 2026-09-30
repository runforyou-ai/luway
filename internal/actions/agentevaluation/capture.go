//go:build server

package agentevaluation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrQuestionNotFound 表示所选提问不是来源周期内提问人发送的文本消息。
	ErrQuestionNotFound = errors.New("evaluation question message not found")
	// ErrQuestionAlreadyEvaluated 表示同一提问已按不同的期望处理方式加入评测。
	ErrQuestionAlreadyEvaluated = errors.New("evaluation question already added with a different expectation")
	// ErrServiceSessionNotFound 表示来源周期不存在、不是由 AI 员工接待，或未被质检标记为应转人工未转。
	ErrServiceSessionNotFound = errors.New("evaluation source service session not found")
)

// KnowledgeGapCases 在待补知识加入知识库的事务中把提问加入负责 AI 员工的评测。
type KnowledgeGapCases struct{}

// AddFromKnowledgeGap 以待补知识的提问消息截取前文与客户上下文，新增期望答复、标准答案为问答答案的用例；待补知识没有提问消息时拒绝。
func (KnowledgeGapCases) AddFromKnowledgeGap(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, gap *servermodels.KnowledgeGap, expectedAnswer string) error {
	if gap.QuestionMessageID == nil {
		return ErrQuestionNotFound
	}
	_, err := addCapturedCase(ctx, tx, identity, gap.ServiceSessionID, *gap.QuestionMessageID, domain.AgentEvaluationCaseSourceKnowledgeGap,
		domain.AgentRunOutcomeReply, expectedAnswer)
	return err
}

// AddServiceSessionCaseAction 把被质检标记为应转人工未转的周期加入负责 AI 员工的评测。
type AddServiceSessionCaseAction struct{ db *bun.DB }

// NewAddServiceSessionCaseAction 创建问题会话加入评测操作。
func NewAddServiceSessionCaseAction(db *bun.DB) *AddServiceSessionCaseAction {
	return &AddServiceSessionCaseAction{db: db}
}

// Execute 以所选客户消息为提问截取前文与客户上下文，新增期望转人工的用例。
func (a *AddServiceSessionCaseAction) Execute(ctx context.Context, identity *servermodels.Identity, serviceSessionID, questionMessageID string) (*Case, error) {
	var created *Case
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if !common.ValidUUID(serviceSessionID) {
			return ErrServiceSessionNotFound
		}
		missedHandoff, err := tx.NewSelect().Model((*servermodels.ServiceSessionReview)(nil)).
			Where("ssr.organization_id = ? AND ssr.service_session_id = ? AND ssr.ai_missed_handoff", identity.Organization.ID, serviceSessionID).
			Exists(ctx)
		if err != nil {
			return fmt.Errorf("check missed handoff review: %w", err)
		}
		if !missedHandoff {
			return ErrServiceSessionNotFound
		}
		created, err = addCapturedCase(ctx, tx, identity, serviceSessionID, questionMessageID, domain.AgentEvaluationCaseSourceServiceSession,
			domain.AgentRunOutcomeHandoff, "")
		return err
	})
	return created, err
}

// addCapturedCase 为来源周期的负责 AI 员工新增一条带前文快照的用例；周期没有由有服务对象的 AI 员工接待时拒绝。
// 资格规则与待补知识详情的 evaluable 判断一致，修改时同步 knowledgegap 的 GetQuery。
func addCapturedCase(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, serviceSessionID, questionMessageID string,
	source domain.AgentEvaluationCaseSource, expectedAction domain.AgentRunOutcome, expectedAnswer string) (*Case, error) {
	organizationID := identity.Organization.ID
	session := struct {
		AgentID  *string `bun:"agent_id"`
		Source   string  `bun:"source"`
		Audience string  `bun:"audience"`
	}{}
	err := tx.NewSelect().TableExpr("service_sessions AS ss").
		ColumnExpr("a.id AS agent_id, svc.source, svc.audience").
		Join("JOIN service_conversations AS svc ON svc.id = ss.service_conversation_id AND svc.organization_id = ss.organization_id").
		Join("LEFT JOIN agents AS a ON a.identity_id = ss.agent_identity_id AND a.organization_id = ss.organization_id").
		Where("ss.organization_id = ? AND ss.id = ?", organizationID, serviceSessionID).
		Scan(ctx, &session)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && session.AgentID == nil) {
		return nil, ErrServiceSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load evaluation source service session: %w", err)
	}
	agent, err := lockEvaluatedAgent(ctx, tx, organizationID, *session.AgentID)
	if err != nil {
		return nil, err
	}
	audience := domain.ServiceAudience(session.Audience)
	if !slices.Contains(agent.ServiceAudiences, audience) {
		return nil, ErrAgentNotFound
	}
	captured, err := captureQuestion(ctx, tx, organizationID, serviceSessionID, questionMessageID, domain.ServiceSource(session.Source) == domain.ServiceSourceChannel)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(captured.context)
	if err != nil {
		return nil, err
	}
	record := &servermodels.AgentEvaluationCase{
		OrganizationID: organizationID, AgentID: agent.ID, Version: 1, Audience: string(audience), Question: captured.question,
		ExpectedAction: string(expectedAction), ExpectedAnswer: expectedAnswer, CreatedByIdentityID: identity.OrganizationIdentity.ID,
		Source: string(source), ServiceSessionID: &serviceSessionID, QuestionMessageID: &questionMessageID, OccurredAt: &captured.occurredAt, Context: encoded,
	}
	result, err := tx.NewInsert().Model(record).
		Column("organization_id", "agent_id", "version", "audience", "question", "expected_action", "expected_answer", "created_by_identity_id",
			"source", "service_session_id", "question_message_id", "occurred_at", "context").
		On("CONFLICT (organization_id, question_message_id) WHERE question_message_id IS NOT NULL DO NOTHING").
		Returning("*").Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("create captured evaluation case: %w", err)
	}
	// 同一提问消息已加入过评测时，期望处理方式相同视为重试并返回已有用例，不同则拒绝。
	if affected, _ := result.RowsAffected(); affected == 0 {
		record = &servermodels.AgentEvaluationCase{}
		if err := tx.NewSelect().Model(record).
			Where("aec.organization_id = ? AND aec.question_message_id = ?", organizationID, questionMessageID).
			Scan(ctx); err != nil {
			return nil, fmt.Errorf("load existing captured evaluation case: %w", err)
		}
		if domain.AgentRunOutcome(record.ExpectedAction) != expectedAction {
			return nil, ErrQuestionAlreadyEvaluated
		}
	}
	output, err := caseFromRecord(record)
	if err != nil {
		return nil, err
	}
	return &output, nil
}

// capturedQuestion 是从来源周期截取的提问、提问时间与快照。
type capturedQuestion struct {
	question   string
	occurredAt time.Time
	context    CaseContext
}

// captureQuestion 以来源周期内提问人发送的一条文本消息为提问，截取它之前的对客往来为前文；渠道来源另外保存客户上下文与客户已验证身份，客户上下文中的历史小结只取提问之前关闭的周期。
func captureQuestion(ctx context.Context, tx bun.Tx, organizationID, serviceSessionID, questionMessageID string, channel bool) (capturedQuestion, error) {
	if !common.ValidUUID(questionMessageID) {
		return capturedQuestion{}, ErrQuestionNotFound
	}
	message := struct {
		MessageSeq   int64     `bun:"message_seq"`
		OriginatedAt time.Time `bun:"originated_at"`
	}{}
	err := tx.NewSelect().TableExpr("messages AS msg").
		ColumnExpr("msg.message_seq, msg.originated_at").
		Join("JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.organization_id = msg.organization_id AND cp.conversation_id = msg.conversation_id").
		Join("JOIN service_conversations AS svc ON svc.organization_id = msg.organization_id AND svc.conversation_id = msg.conversation_id").
		Where("msg.organization_id = ? AND msg.id = ? AND msg.service_session_id = ?", organizationID, questionMessageID, serviceSessionID).
		Where("msg.type = ? AND msg.visibility = ? AND msg.deleted_at IS NULL", domain.MessageTypeText, domain.MessageVisibilityShared).
		Where("cp.subject_id = svc.requester_subject_id").
		Scan(ctx, &message)
	if errors.Is(err, sql.ErrNoRows) {
		return capturedQuestion{}, ErrQuestionNotFound
	}
	if err != nil {
		return capturedQuestion{}, fmt.Errorf("load evaluation question message: %w", err)
	}
	transcript, err := servicesummary.LoadTranscript(ctx, tx, organizationID, serviceSessionID, message.MessageSeq)
	if err != nil {
		return capturedQuestion{}, err
	}
	if len(transcript) == 0 || transcript[len(transcript)-1].MessageID != questionMessageID || transcript[len(transcript)-1].Content == "" {
		return capturedQuestion{}, ErrQuestionNotFound
	}
	captured := capturedQuestion{
		question: transcript[len(transcript)-1].Content, occurredAt: message.OriginatedAt,
		context: CaseContext{Messages: make([]ContextMessage, 0, len(transcript)-1)},
	}
	for _, entry := range transcript[:len(transcript)-1] {
		captured.context.Messages = append(captured.context.Messages, ContextMessage{Sender: entry.Sender, Body: entry.Content})
	}
	if !channel {
		return captured, nil
	}
	captured.context.Channel = true
	if captured.context.CustomerContext, err = agentrunaction.CustomerContextContent(ctx, tx, organizationID, serviceSessionID, &message.OriginatedAt); err != nil {
		return capturedQuestion{}, err
	}
	customer, err := agentrunaction.LoadServiceSessionCustomer(ctx, tx, organizationID, serviceSessionID)
	if err != nil {
		return capturedQuestion{}, err
	}
	if customer.UserID != "" {
		captured.context.Customer = &ContextCustomer{UserID: customer.UserID, Email: customer.Email}
	}
	return captured, nil
}
