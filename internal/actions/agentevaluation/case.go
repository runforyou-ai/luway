//go:build server

// Package agentevaluation 实现 AI 员工的回答质量评测：评测用例、离线回放运行、判定与结果对比。
package agentevaluation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrAgentNotFound 表示当前企业中不存在指定 AI 员工，或它没有服务对象。
	ErrAgentNotFound = errors.New("evaluated agent not found")
	// ErrCaseNotFound 表示 AI 员工下不存在指定评测用例。
	ErrCaseNotFound = errors.New("evaluation case not found")
)

const (
	ValidationAudienceInvalid        common.FieldCode = "AGENT_EVALUATION_AUDIENCE_INVALID"
	ValidationQuestionRequired       common.FieldCode = "AGENT_EVALUATION_QUESTION_REQUIRED"
	ValidationExpectedActionInvalid  common.FieldCode = "AGENT_EVALUATION_EXPECTED_ACTION_INVALID"
	ValidationExpectedAnswerRequired common.FieldCode = "AGENT_EVALUATION_EXPECTED_ANSWER_REQUIRED"
)

// CaseInput 定义手动填写的评测用例：期望转人工时标准答案为空。
type CaseInput struct {
	Audience       domain.ServiceAudience
	Question       string
	ExpectedAction domain.AgentRunOutcome
	ExpectedAnswer string
}

// contextSenderCustomer 是前文中提问人消息的发送方取值，与质检沟通记录一致。
const contextSenderCustomer = "customer"

// ContextMessage 是用例前文中的一条消息，Sender 为 customer 提问人、ai AI 员工或 staff 真人处理人。
type ContextMessage struct {
	Sender string `json:"sender"`
	Body   string `json:"body"`
}

// ContextCustomer 是提问客户的已验证身份，供按客户查询的 MCP 服务使用。
type ContextCustomer struct {
	UserID string `json:"userId"`
	Email  string `json:"email,omitempty"`
}

// CaseContext 是从服务周期加入的用例保存的快照：前文、客户上下文正文、客户已验证身份与来源是否为渠道会话；渠道来源的用例回放时与线上一样提供客户历史检索。
type CaseContext struct {
	Channel         bool             `json:"channel,omitempty"`
	Messages        []ContextMessage `json:"messages"`
	CustomerContext string           `json:"customerContext,omitempty"`
	Customer        *ContextCustomer `json:"customer,omitempty"`
}

// Case 定义一条评测用例；ServiceSessionID 与 OccurredAt 是来源周期与提问时间，手动用例为空。
type Case struct {
	ID               string
	AgentID          string
	Source           domain.AgentEvaluationCaseSource
	ServiceSessionID *string
	OccurredAt       *time.Time
	Context          CaseContext
	Version          int
	Audience         domain.ServiceAudience
	Question         string
	ExpectedAction   domain.AgentRunOutcome
	ExpectedAnswer   string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// CreateCaseAction 为 AI 员工新建手动评测用例。
type CreateCaseAction struct{ db *bun.DB }

// NewCreateCaseAction 创建新建评测用例操作。
func NewCreateCaseAction(db *bun.DB) *CreateCaseAction { return &CreateCaseAction{db: db} }

// Execute 校验并保存手动用例。
func (a *CreateCaseAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID string, input CaseInput) (*Case, error) {
	record := &servermodels.AgentEvaluationCase{}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		agent, err := lockEvaluatedAgent(ctx, tx, identity.Organization.ID, agentID)
		if err != nil {
			return err
		}
		if input, err = normalizeCaseInput(agent, input); err != nil {
			return err
		}
		record = &servermodels.AgentEvaluationCase{
			OrganizationID: identity.Organization.ID, AgentID: agent.ID, Version: 1, Audience: string(input.Audience), Question: input.Question,
			ExpectedAction: string(input.ExpectedAction), ExpectedAnswer: input.ExpectedAnswer, CreatedByIdentityID: identity.OrganizationIdentity.ID,
			Source: string(domain.AgentEvaluationCaseSourceManual),
		}
		if _, err := tx.NewInsert().Model(record).
			Column("organization_id", "agent_id", "version", "audience", "question", "expected_action", "expected_answer", "created_by_identity_id", "source").
			Returning("*").Exec(ctx); err != nil {
			return fmt.Errorf("create evaluation case: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	output, err := caseFromRecord(record)
	if err != nil {
		return nil, err
	}
	return &output, nil
}

// UpdateCaseAction 修改评测用例的提问、期望处理方式与标准答案。
type UpdateCaseAction struct{ db *bun.DB }

// NewUpdateCaseAction 创建修改评测用例操作。
func NewUpdateCaseAction(db *bun.DB) *UpdateCaseAction { return &UpdateCaseAction{db: db} }

// Execute 保存修改并在内容变化时把用例版本号加一。
func (a *UpdateCaseAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID, caseID string, input CaseInput) (*Case, error) {
	record := &servermodels.AgentEvaluationCase{}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		agent, err := lockEvaluatedAgent(ctx, tx, identity.Organization.ID, agentID)
		if err != nil {
			return err
		}
		if input, err = normalizeCaseInput(agent, input); err != nil {
			return err
		}
		if record, err = lockCase(ctx, tx, identity.Organization.ID, agent.ID, caseID); err != nil {
			return err
		}
		if record.Audience == string(input.Audience) && record.Question == input.Question &&
			record.ExpectedAction == string(input.ExpectedAction) && record.ExpectedAnswer == input.ExpectedAnswer {
			return nil
		}
		if _, err := tx.NewUpdate().Model(record).
			Set("audience = ?", input.Audience).
			Set("question = ?", input.Question).
			Set("expected_action = ?", input.ExpectedAction).
			Set("expected_answer = ?", input.ExpectedAnswer).
			Set("version = version + 1").
			Set("updated_at = now()").
			WherePK().Returning("*").Exec(ctx); err != nil {
			return fmt.Errorf("update evaluation case: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	output, err := caseFromRecord(record)
	if err != nil {
		return nil, err
	}
	return &output, nil
}

// DeleteCaseAction 删除评测用例。
type DeleteCaseAction struct{ db *bun.DB }

// NewDeleteCaseAction 创建删除评测用例操作。
func NewDeleteCaseAction(db *bun.DB) *DeleteCaseAction { return &DeleteCaseAction{db: db} }

// Execute 删除用例；历史结果保留运行时的快照，用例不再参与后续运行。
func (a *DeleteCaseAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID, caseID string) error {
	return a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		agent, err := lockEvaluatedAgent(ctx, tx, identity.Organization.ID, agentID)
		if err != nil {
			return err
		}
		record, err := lockCase(ctx, tx, identity.Organization.ID, agent.ID, caseID)
		if err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model(record).WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("delete evaluation case: %w", err)
		}
		return nil
	})
}

// normalizeCaseInput 去除首尾空白并校验服务对象、提问、期望处理方式与标准答案；期望转人工时清空标准答案。
func normalizeCaseInput(agent *servermodels.Agent, input CaseInput) (CaseInput, error) {
	input.Question = strings.TrimSpace(input.Question)
	input.ExpectedAnswer = strings.TrimSpace(input.ExpectedAnswer)
	fields := map[string]common.FieldCode{}
	if !slices.Contains(agent.ServiceAudiences, input.Audience) {
		fields["audience"] = ValidationAudienceInvalid
	}
	if input.Question == "" {
		fields["question"] = ValidationQuestionRequired
	}
	if !slices.Contains(domain.AgentEvaluationActions, input.ExpectedAction) {
		fields["expectedAction"] = ValidationExpectedActionInvalid
	}
	if input.ExpectedAction == domain.AgentRunOutcomeHandoff {
		input.ExpectedAnswer = ""
	} else if input.ExpectedAnswer == "" {
		fields["expectedAnswer"] = ValidationExpectedAnswerRequired
	}
	if len(fields) != 0 {
		return CaseInput{}, &common.FieldError{Fields: fields}
	}
	return input, nil
}

// lockEvaluatedAgent 锁定当前企业中有服务对象的 AI 员工，助理与没有服务对象的 AI 员工视为不存在。
func lockEvaluatedAgent(ctx context.Context, tx bun.Tx, organizationID, agentID string) (*servermodels.Agent, error) {
	return scanEvaluatedAgent(ctx, tx, organizationID, agentID, true)
}

// lockCase 锁定 AI 员工下的评测用例。
func lockCase(ctx context.Context, tx bun.Tx, organizationID, agentID, caseID string) (*servermodels.AgentEvaluationCase, error) {
	if !common.ValidUUID(caseID) {
		return nil, ErrCaseNotFound
	}
	record := &servermodels.AgentEvaluationCase{}
	err := tx.NewSelect().Model(record).
		Where("aec.organization_id = ? AND aec.agent_id = ? AND aec.id = ?", organizationID, agentID, caseID).
		For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCaseNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock evaluation case: %w", err)
	}
	return record, nil
}

// caseFromRecord 把存储记录转换为评测用例，手动用例的前文为空。
func caseFromRecord(record *servermodels.AgentEvaluationCase) (Case, error) {
	output := Case{
		ID: record.ID, AgentID: record.AgentID, Source: domain.AgentEvaluationCaseSource(record.Source), ServiceSessionID: record.ServiceSessionID, OccurredAt: record.OccurredAt,
		Context: CaseContext{Messages: []ContextMessage{}}, Version: record.Version, Audience: domain.ServiceAudience(record.Audience), Question: record.Question,
		ExpectedAction: domain.AgentRunOutcome(record.ExpectedAction), ExpectedAnswer: record.ExpectedAnswer,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if len(record.Context) > 0 {
		if err := json.Unmarshal(record.Context, &output.Context); err != nil {
			return Case{}, fmt.Errorf("decode evaluation case context: %w", err)
		}
	}
	return output, nil
}
