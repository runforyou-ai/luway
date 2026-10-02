//go:build server

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	executionSchemaVersion     = 1
	maxSystemInstructionLength = 20000
)

// ExecutionInput 定义执行配置输入，按执行方式填写 Managed 或 LocalAgent。
type ExecutionInput struct {
	Mode       domain.AgentExecutionMode
	Managed    *ManagedExecutionInput
	LocalAgent *LocalAgentExecutionInput
}

// LocalAgentExecutionInput 定义由本机 Agent 执行的配置输入。
type LocalAgentExecutionInput struct {
	Kind              domain.LocalAgentKind
	SystemInstruction string
}

// ManagedExecutionInput 定义平台托管执行配置输入。
type ManagedExecutionInput struct {
	ModelID           string
	SystemInstruction string
	KnowledgeBaseIDs  []string
}

// Execution 定义当前生效的执行配置。
type Execution struct {
	MCPServerIDs []string
	RevisionID   string
	Mode         domain.AgentExecutionMode
	Managed      *ManagedExecution
	LocalAgent   *LocalAgentExecution
}

// LocalAgentExecution 定义由本机 Agent 执行的配置。
type LocalAgentExecution struct {
	Kind              domain.LocalAgentKind
	SystemInstruction string
}

// ManagedExecution 定义平台托管执行配置。
type ManagedExecution struct {
	Model             aimodel.Option
	SystemInstruction string
	KnowledgeBaseIDs  []string
}

// ExecutionSummary 定义当前执行配置摘要。
type ExecutionSummary struct {
	RevisionID string
	Mode       domain.AgentExecutionMode
	Managed    *ManagedExecutionSummary
	LocalAgent *LocalAgentExecutionSummary
}

// LocalAgentExecutionSummary 定义由本机 Agent 执行的配置摘要。
type LocalAgentExecutionSummary struct {
	Kind domain.LocalAgentKind
}

// ManagedExecutionSummary 定义平台托管执行配置摘要。
type ManagedExecutionSummary struct {
	Model aimodel.Option
}

type managedRevisionConfigurationV1 struct {
	MCPServerIDs      []string `json:"mcpServerIds"`
	SystemInstruction string   `json:"systemInstruction"`
	KnowledgeBaseIDs  []string `json:"knowledgeBaseIds"`
}

type localAgentRevisionConfigurationV1 struct {
	Kind              domain.LocalAgentKind `json:"kind"`
	SystemInstruction string                `json:"systemInstruction"`
}

// normalizeExecutionInput 规范化并校验执行配置，allowLocalAgent 为 false 时只接受平台托管执行。
func normalizeExecutionInput(input ExecutionInput, allowLocalAgent bool) (ExecutionInput, error) {
	switch {
	case input.Mode == domain.AgentExecutionModeManaged && input.Managed != nil && input.LocalAgent == nil:
		managed, err := normalizeManagedExecutionInput(*input.Managed)
		if err != nil {
			return ExecutionInput{}, err
		}
		input.Managed = &managed
		return input, nil
	case allowLocalAgent && input.Mode == domain.AgentExecutionModeLocalAgent && input.LocalAgent != nil && input.Managed == nil:
		localAgent := *input.LocalAgent
		localAgent.SystemInstruction = strings.TrimSpace(localAgent.SystemInstruction)
		if !domain.LocalAgentKindValid(localAgent.Kind) {
			return ExecutionInput{}, &common.FieldError{Fields: map[string]common.FieldCode{"localAgent": ValidationLocalAgentInvalid}}
		}
		if utf8.RuneCountInString(localAgent.SystemInstruction) > maxSystemInstructionLength {
			return ExecutionInput{}, &common.FieldError{Fields: map[string]common.FieldCode{"systemInstruction": ValidationSystemInstructionTooLong}}
		}
		input.LocalAgent = &localAgent
		return input, nil
	default:
		return ExecutionInput{}, &common.FieldError{Fields: map[string]common.FieldCode{"execution": ValidationExecutionInvalid}}
	}
}

// normalizeManagedExecutionInput 规范化并校验平台托管执行配置。
func normalizeManagedExecutionInput(input ManagedExecutionInput) (ManagedExecutionInput, error) {
	var modelIDValid bool
	input.ModelID, modelIDValid = common.NormalizeUUID(input.ModelID)
	input.SystemInstruction = strings.TrimSpace(input.SystemInstruction)
	fields := make(map[string]common.FieldCode)
	// 规范化知识库编号并按集合保存。
	knowledgeBaseIDs := make([]string, 0, len(input.KnowledgeBaseIDs))
	for _, id := range input.KnowledgeBaseIDs {
		normalized, valid := common.NormalizeUUID(id)
		if !valid {
			fields["knowledgeBaseIds"] = ValidationKnowledgeBaseInvalid
		}
		knowledgeBaseIDs = append(knowledgeBaseIDs, normalized)
	}
	slices.Sort(knowledgeBaseIDs)
	input.KnowledgeBaseIDs = slices.Compact(knowledgeBaseIDs)
	if !modelIDValid {
		fields["modelId"] = ValidationModelInvalid
	}
	if utf8.RuneCountInString(input.SystemInstruction) > maxSystemInstructionLength {
		fields["systemInstruction"] = ValidationSystemInstructionTooLong
	}
	if len(fields) > 0 {
		return ManagedExecutionInput{}, &common.FieldError{Fields: fields}
	}
	return input, nil
}

// lockManagedExecutionModel 校验并锁定平台托管执行配置使用的 AI 员工对话模型。
func lockManagedExecutionModel(ctx context.Context, tx bun.Tx, organizationID string, input ManagedExecutionInput) (aimodel.Option, error) {
	model, err := aimodel.Lock(ctx, tx, organizationID, input.ModelID, domain.AIModelUsageAgent)
	if errors.Is(err, aimodel.ErrUnavailable) {
		return aimodel.Option{}, &common.FieldError{Fields: map[string]common.FieldCode{"modelId": ValidationModelInvalid}}
	}
	if err != nil {
		return aimodel.Option{}, err
	}
	return model.Option(), nil
}

// lockExecutionKnowledgeBases 校验并锁定托管执行绑定的同企业知识库直至事务结束，须在锁定 AI 员工之前调用。
func lockExecutionKnowledgeBases(ctx context.Context, db bun.IDB, organizationID string, input ExecutionInput) error {
	if input.Managed == nil || len(input.Managed.KnowledgeBaseIDs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(input.Managed.KnowledgeBaseIDs))
	if err := db.NewSelect().Model((*servermodels.KnowledgeBase)(nil)).
		Column("id").Where("kb.organization_id = ?", organizationID).
		Where("kb.id IN (?)", bun.In(input.Managed.KnowledgeBaseIDs)).
		OrderExpr("kb.id ASC").For("KEY SHARE").Scan(ctx, &ids); err != nil {
		return err
	}
	if len(ids) != len(input.Managed.KnowledgeBaseIDs) {
		return &common.FieldError{Fields: map[string]common.FieldCode{"knowledgeBaseIds": ValidationKnowledgeBaseInvalid}}
	}
	return nil
}

// insertExecutionRevision 创建执行配置版本，model 只用于平台托管执行，绑定的知识库须已由 lockExecutionKnowledgeBases 锁定。
func insertExecutionRevision(ctx context.Context, db bun.IDB, identity *servermodels.Identity, agentID, revisionID string, input ExecutionInput, model aimodel.Option, mcpServerIDs []string) (Execution, error) {
	if input.Mode == domain.AgentExecutionModeLocalAgent {
		configuration, err := json.Marshal(localAgentRevisionConfigurationV1{
			Kind: input.LocalAgent.Kind, SystemInstruction: input.LocalAgent.SystemInstruction,
		})
		if err != nil {
			return Execution{}, err
		}
		if err := insertRevision(ctx, db, identity, agentID, revisionID, input.Mode, "", configuration); err != nil {
			return Execution{}, err
		}
		return Execution{
			RevisionID: revisionID, Mode: input.Mode, MCPServerIDs: []string{},
			LocalAgent: &LocalAgentExecution{Kind: input.LocalAgent.Kind, SystemInstruction: input.LocalAgent.SystemInstruction},
		}, nil
	}
	configuration, err := json.Marshal(managedRevisionConfigurationV1{
		MCPServerIDs:      mcpServerIDs,
		SystemInstruction: input.Managed.SystemInstruction,
		KnowledgeBaseIDs:  input.Managed.KnowledgeBaseIDs,
	})
	if err != nil {
		return Execution{}, err
	}
	if err := insertRevision(ctx, db, identity, agentID, revisionID, input.Mode, model.ID, configuration); err != nil {
		return Execution{}, err
	}
	return Execution{
		RevisionID: revisionID, Mode: input.Mode, MCPServerIDs: mcpServerIDs,
		Managed: &ManagedExecution{
			Model:             model,
			SystemInstruction: input.Managed.SystemInstruction,
			KnowledgeBaseIDs:  input.Managed.KnowledgeBaseIDs,
		},
	}, nil
}

// insertRevision 写入一条不可变执行配置版本，modelID 只用于平台托管执行。
func insertRevision(ctx context.Context, db bun.IDB, identity *servermodels.Identity, agentID, revisionID string, mode domain.AgentExecutionMode, modelID string, configuration json.RawMessage) error {
	revision := &servermodels.AgentRevision{
		ID: revisionID, OrganizationID: identity.Organization.ID, AgentID: agentID,
		ExecutionMode: string(mode), ModelID: modelID, SchemaVersion: executionSchemaVersion,
		Configuration: configuration, CreatedByUserID: identity.User.ID,
	}
	_, err := db.NewInsert().Model(revision).
		Column("id", "organization_id", "agent_id", "execution_mode", "model_id", "schema_version", "configuration", "created_by_user_id").
		Exec(ctx)
	return err
}

// decodeRevisionExecution 解码不可变执行配置版本，托管执行的模型只填写编号。
func decodeRevisionExecution(revision servermodels.AgentRevision) (Execution, error) {
	mode := domain.AgentExecutionMode(revision.ExecutionMode)
	if revision.SchemaVersion != executionSchemaVersion {
		return Execution{}, fmt.Errorf("unsupported %s execution schema version %d", mode, revision.SchemaVersion)
	}
	decoder := json.NewDecoder(bytes.NewReader(revision.Configuration))
	decoder.DisallowUnknownFields()
	switch mode {
	case domain.AgentExecutionModeManaged:
	case domain.AgentExecutionModeLocalAgent:
		configuration := localAgentRevisionConfigurationV1{}
		if err := decoder.Decode(&configuration); err != nil {
			return Execution{}, fmt.Errorf("decode local agent execution configuration: %w", err)
		}
		if !domain.LocalAgentKindValid(configuration.Kind) || utf8.RuneCountInString(configuration.SystemInstruction) > maxSystemInstructionLength {
			return Execution{}, errors.New("local agent execution configuration is invalid")
		}
		return Execution{
			RevisionID: revision.ID, Mode: mode, MCPServerIDs: []string{},
			LocalAgent: &LocalAgentExecution{Kind: configuration.Kind, SystemInstruction: configuration.SystemInstruction},
		}, nil
	default:
		return Execution{}, fmt.Errorf("unsupported agent execution mode %q", revision.ExecutionMode)
	}
	configuration := managedRevisionConfigurationV1{}
	if err := decoder.Decode(&configuration); err != nil {
		return Execution{}, fmt.Errorf("decode managed execution configuration: %w", err)
	}
	if !common.ValidUUID(revision.ModelID) || utf8.RuneCountInString(configuration.SystemInstruction) > maxSystemInstructionLength {
		return Execution{}, errors.New("managed execution configuration is invalid")
	}
	return Execution{
		RevisionID: revision.ID, Mode: mode, MCPServerIDs: configuration.MCPServerIDs,
		Managed: &ManagedExecution{
			Model:             aimodel.Option{ID: revision.ModelID},
			SystemInstruction: configuration.SystemInstruction,
			KnowledgeBaseIDs:  configuration.KnowledgeBaseIDs,
		},
	}, nil
}

// loadAgentExecution 读取 AI 员工当前生效的执行配置。
func loadAgentExecution(ctx context.Context, db bun.IDB, organizationID, agentID string) (Execution, error) {
	revision := &servermodels.AgentRevision{}
	if err := db.NewSelect().Model(revision).
		Join("JOIN agents AS a ON a.active_revision_id = ar.id AND a.organization_id = ar.organization_id AND a.id = ar.agent_id").
		Where("a.organization_id = ?", organizationID).
		Where("a.id = ?", agentID).
		Scan(ctx); err != nil {
		return Execution{}, err
	}
	execution, err := decodeRevisionExecution(*revision)
	if err != nil || execution.Managed == nil {
		return execution, err
	}
	// 读取已保存平台托管配置当前对应的模型。
	models, err := aimodel.LoadOptions(ctx, db, organizationID, []string{execution.Managed.Model.ID}, domain.AIModelUsageAgent)
	if err != nil {
		return Execution{}, err
	}
	model, exists := models[execution.Managed.Model.ID]
	if !exists {
		return Execution{}, fmt.Errorf("managed execution model %q is unavailable", execution.Managed.Model.ID)
	}
	execution.Managed.Model = model
	return execution, nil
}

// loadAgentExecutionSummaries 批量读取 AI 员工当前执行配置摘要。
func loadAgentExecutionSummaries(ctx context.Context, db bun.IDB, organizationID string, agentIDs []string) (map[string]ExecutionSummary, error) {
	type row struct {
		AgentID       string          `bun:"agent_id"`
		RevisionID    string          `bun:"revision_id"`
		ExecutionMode string          `bun:"execution_mode"`
		ModelID       string          `bun:"model_id"`
		SchemaVersion int             `bun:"schema_version"`
		Configuration json.RawMessage `bun:"configuration"`
	}
	if len(agentIDs) == 0 {
		return map[string]ExecutionSummary{}, nil
	}
	records := make([]row, 0, len(agentIDs))
	if err := db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("a.id::text AS agent_id, ar.id::text AS revision_id, ar.execution_mode, COALESCE(ar.model_id::text, '') AS model_id, ar.schema_version, ar.configuration").
		Join("JOIN agent_revisions AS ar ON ar.id = a.active_revision_id AND ar.organization_id = a.organization_id AND ar.agent_id = a.id").
		Where("a.organization_id = ?", organizationID).
		Where("a.id IN (?)", bun.In(agentIDs)).
		Scan(ctx, &records); err != nil {
		return nil, err
	}
	if len(records) != len(agentIDs) {
		return nil, fmt.Errorf("agent execution revision count %d does not match agent count %d", len(records), len(agentIDs))
	}
	executions := make(map[string]Execution, len(records))
	modelIDs := make([]string, 0, len(records))
	for _, record := range records {
		execution, err := decodeRevisionExecution(servermodels.AgentRevision{
			ID: record.RevisionID, ExecutionMode: record.ExecutionMode, ModelID: record.ModelID,
			SchemaVersion: record.SchemaVersion, Configuration: record.Configuration,
		})
		if err != nil {
			return nil, fmt.Errorf("decode agent %q execution: %w", record.AgentID, err)
		}
		executions[record.AgentID] = execution
		if execution.Managed != nil {
			modelIDs = append(modelIDs, execution.Managed.Model.ID)
		}
	}
	models, err := aimodel.LoadOptions(ctx, db, organizationID, modelIDs, domain.AIModelUsageAgent)
	if err != nil {
		return nil, err
	}
	summaries := make(map[string]ExecutionSummary, len(executions))
	for agentID, execution := range executions {
		if execution.LocalAgent != nil {
			summaries[agentID] = ExecutionSummary{
				RevisionID: execution.RevisionID, Mode: execution.Mode,
				LocalAgent: &LocalAgentExecutionSummary{Kind: execution.LocalAgent.Kind},
			}
			continue
		}
		model, exists := models[execution.Managed.Model.ID]
		if !exists {
			return nil, fmt.Errorf("agent %q managed execution model is unavailable", agentID)
		}
		summaries[agentID] = ExecutionSummary{
			RevisionID: execution.RevisionID, Mode: execution.Mode,
			Managed: &ManagedExecutionSummary{Model: model},
		}
	}
	return summaries, nil
}
