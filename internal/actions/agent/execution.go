//go:build server

package agent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
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
	ProviderID        string
	ModelIdentifier   string
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
	ProviderID        string
	ProviderName      string
	ModelIdentifier   string
	ModelName         string
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
	ProviderID      string
	ProviderName    string
	ModelIdentifier string
	ModelName       string
}

// ModelOption 定义 AI 员工可用的对话模型。
type ModelOption struct {
	ProviderID      string `bun:"provider_id"`
	ProviderName    string `bun:"provider_name"`
	ModelIdentifier string `bun:"model_identifier"`
	ModelName       string `bun:"model_name"`
}

type managedRevisionConfigurationV1 struct {
	MCPServerIDs      []string               `json:"mcpServerIds"`
	Model             managedRevisionModelV1 `json:"model"`
	SystemInstruction string                 `json:"systemInstruction"`
	KnowledgeBaseIDs  []string               `json:"knowledgeBaseIds"`
}

type localAgentRevisionConfigurationV1 struct {
	Kind              domain.LocalAgentKind `json:"kind"`
	SystemInstruction string                `json:"systemInstruction"`
}

type managedRevisionModelV1 struct {
	ProviderID   string `json:"providerId"`
	ProviderName string `json:"providerName"`
	Identifier   string `json:"identifier"`
	Name         string `json:"name"`
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
	var providerIDValid bool
	input.ProviderID, providerIDValid = common.NormalizeUUID(input.ProviderID)
	input.ModelIdentifier = strings.TrimSpace(input.ModelIdentifier)
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
	if !providerIDValid {
		fields["providerId"] = ValidationModelInvalid
	}
	if input.ModelIdentifier == "" {
		fields["modelIdentifier"] = ValidationModelInvalid
	}
	if utf8.RuneCountInString(input.SystemInstruction) > maxSystemInstructionLength {
		fields["systemInstruction"] = ValidationSystemInstructionTooLong
	}
	if len(fields) > 0 {
		return ManagedExecutionInput{}, &common.FieldError{Fields: fields}
	}
	return input, nil
}

// loadManagedExecutionModel 校验平台托管执行配置使用的文本对话模型。
func loadManagedExecutionModel(ctx context.Context, db bun.IDB, organizationID string, input ManagedExecutionInput) (ModelOption, error) {
	model := ModelOption{}
	if err := managedExecutionModelQuery(db, organizationID, input.ProviderID, input.ModelIdentifier).
		For("KEY SHARE OF aip, aipm").
		Scan(ctx, &model); errors.Is(err, sql.ErrNoRows) {
		return ModelOption{}, &common.FieldError{Fields: map[string]common.FieldCode{"modelIdentifier": ValidationModelInvalid}}
	} else if err != nil {
		return ModelOption{}, err
	}
	return model, nil
}

// managedExecutionModelQuery 构造平台托管执行配置的模型目录查询。
func managedExecutionModelQuery(db bun.IDB, organizationID, providerID, modelIdentifier string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("ai_provider_models AS aipm").
		ColumnExpr("aip.id::text AS provider_id, aip.name AS provider_name, aipm.identifier AS model_identifier, aipm.name AS model_name").
		Join("JOIN ai_providers AS aip ON aip.id = aipm.provider_id AND aip.organization_id = aipm.organization_id").
		Where("aipm.organization_id = ?", organizationID).
		Where("aipm.provider_id = ?", providerID).
		Where("aipm.identifier = ?", modelIdentifier).
		Where("aipm.model_type = ?", domain.AIModelTypeChat).
		Where("aipm.input_modalities @> ?::jsonb", `["text"]`)
}

// lockExecutionKnowledgeBases 校验并锁定托管执行绑定的同企业知识库直至事务结束，须在锁定员工或助理之前调用。
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
func insertExecutionRevision(ctx context.Context, db bun.IDB, identity *servermodels.Identity, agentID, revisionID string, input ExecutionInput, model ModelOption, mcpServerIDs []string) (Execution, error) {
	if input.Mode == domain.AgentExecutionModeLocalAgent {
		configuration, err := json.Marshal(localAgentRevisionConfigurationV1{
			Kind: input.LocalAgent.Kind, SystemInstruction: input.LocalAgent.SystemInstruction,
		})
		if err != nil {
			return Execution{}, err
		}
		if err := insertRevision(ctx, db, identity, agentID, revisionID, input.Mode, configuration); err != nil {
			return Execution{}, err
		}
		return Execution{
			RevisionID: revisionID, Mode: input.Mode, MCPServerIDs: []string{},
			LocalAgent: &LocalAgentExecution{Kind: input.LocalAgent.Kind, SystemInstruction: input.LocalAgent.SystemInstruction},
		}, nil
	}
	configuration, err := json.Marshal(managedRevisionConfigurationV1{
		MCPServerIDs: mcpServerIDs,
		Model: managedRevisionModelV1{
			ProviderID: model.ProviderID, ProviderName: model.ProviderName,
			Identifier: model.ModelIdentifier, Name: model.ModelName,
		},
		SystemInstruction: input.Managed.SystemInstruction,
		KnowledgeBaseIDs:  input.Managed.KnowledgeBaseIDs,
	})
	if err != nil {
		return Execution{}, err
	}
	if err := insertRevision(ctx, db, identity, agentID, revisionID, input.Mode, configuration); err != nil {
		return Execution{}, err
	}
	return Execution{
		RevisionID: revisionID, Mode: input.Mode, MCPServerIDs: mcpServerIDs,
		Managed: &ManagedExecution{
			ProviderID: model.ProviderID, ProviderName: model.ProviderName,
			ModelIdentifier: model.ModelIdentifier, ModelName: model.ModelName,
			SystemInstruction: input.Managed.SystemInstruction,
			KnowledgeBaseIDs:  input.Managed.KnowledgeBaseIDs,
		},
	}, nil
}

// insertRevision 写入一条不可变执行配置版本。
func insertRevision(ctx context.Context, db bun.IDB, identity *servermodels.Identity, agentID, revisionID string, mode domain.AgentExecutionMode, configuration json.RawMessage) error {
	revision := &servermodels.AgentRevision{
		ID: revisionID, OrganizationID: identity.Organization.ID, AgentID: agentID,
		ExecutionMode: string(mode), SchemaVersion: executionSchemaVersion,
		Configuration: configuration, CreatedByUserID: identity.User.ID,
	}
	_, err := db.NewInsert().Model(revision).
		Column("id", "organization_id", "agent_id", "execution_mode", "schema_version", "configuration", "created_by_user_id").
		Exec(ctx)
	return err
}

// decodeRevisionExecution 解码不可变执行配置版本。
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
	if !common.ValidUUID(configuration.Model.ProviderID) ||
		strings.TrimSpace(configuration.Model.ProviderName) == "" ||
		strings.TrimSpace(configuration.Model.Identifier) == "" ||
		strings.TrimSpace(configuration.Model.Name) == "" ||
		utf8.RuneCountInString(configuration.SystemInstruction) > maxSystemInstructionLength {
		return Execution{}, errors.New("managed execution configuration is invalid")
	}
	return Execution{
		RevisionID: revision.ID, Mode: mode, MCPServerIDs: configuration.MCPServerIDs,
		Managed: &ManagedExecution{
			ProviderID: configuration.Model.ProviderID, ProviderName: configuration.Model.ProviderName,
			ModelIdentifier: configuration.Model.Identifier, ModelName: configuration.Model.Name,
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
	// 读取已保存平台托管配置当前对应的模型目录项。
	model := ModelOption{}
	if err := managedExecutionModelQuery(db, organizationID, execution.Managed.ProviderID, execution.Managed.ModelIdentifier).Scan(ctx, &model); errors.Is(err, sql.ErrNoRows) {
		return Execution{}, fmt.Errorf("managed execution model %q/%q is unavailable", execution.Managed.ProviderID, execution.Managed.ModelIdentifier)
	} else if err != nil {
		return Execution{}, err
	}
	execution.Managed.ProviderName = model.ProviderName
	execution.Managed.ModelName = model.ModelName
	return execution, nil
}

// loadAgentExecutionSummaries 批量读取 AI 员工当前执行配置摘要。
func loadAgentExecutionSummaries(ctx context.Context, db bun.IDB, organizationID string, agentIDs []string) (map[string]ExecutionSummary, error) {
	type row struct {
		AgentID       string          `bun:"agent_id"`
		RevisionID    string          `bun:"revision_id"`
		ExecutionMode string          `bun:"execution_mode"`
		SchemaVersion int             `bun:"schema_version"`
		Configuration json.RawMessage `bun:"configuration"`
	}
	if len(agentIDs) == 0 {
		return map[string]ExecutionSummary{}, nil
	}
	records := make([]row, 0, len(agentIDs))
	if err := db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("a.id::text AS agent_id, ar.id::text AS revision_id, ar.execution_mode, ar.schema_version, ar.configuration").
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
	providerIDs := make(map[string]struct{})
	for _, record := range records {
		execution, err := decodeRevisionExecution(servermodels.AgentRevision{
			ID: record.RevisionID, ExecutionMode: record.ExecutionMode,
			SchemaVersion: record.SchemaVersion, Configuration: record.Configuration,
		})
		if err != nil {
			return nil, fmt.Errorf("decode agent %q execution: %w", record.AgentID, err)
		}
		executions[record.AgentID] = execution
		if execution.Managed != nil {
			providerIDs[execution.Managed.ProviderID] = struct{}{}
		}
	}
	providerIDList := make([]string, 0, len(providerIDs))
	for providerID := range providerIDs {
		providerIDList = append(providerIDList, providerID)
	}
	models := make([]ModelOption, 0)
	if len(providerIDList) > 0 {
		if err := db.NewSelect().TableExpr("ai_provider_models AS aipm").
			ColumnExpr("aip.id::text AS provider_id, aip.name AS provider_name, aipm.identifier AS model_identifier, aipm.name AS model_name").
			Join("JOIN ai_providers AS aip ON aip.id = aipm.provider_id AND aip.organization_id = aipm.organization_id").
			Where("aipm.organization_id = ?", organizationID).
			Where("aipm.provider_id IN (?)", bun.In(providerIDList)).
			Where("aipm.model_type = ?", domain.AIModelTypeChat).
			Where("aipm.input_modalities @> ?::jsonb", `["text"]`).
			Scan(ctx, &models); err != nil {
			return nil, err
		}
	}
	modelByKey := make(map[string]ModelOption, len(models))
	for _, model := range models {
		modelByKey[executionModelKey(model.ProviderID, model.ModelIdentifier)] = model
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
		model, exists := modelByKey[executionModelKey(execution.Managed.ProviderID, execution.Managed.ModelIdentifier)]
		if !exists {
			return nil, fmt.Errorf("agent %q managed execution model is unavailable", agentID)
		}
		summaries[agentID] = ExecutionSummary{
			RevisionID: execution.RevisionID, Mode: execution.Mode,
			Managed: &ManagedExecutionSummary{
				ProviderID: model.ProviderID, ProviderName: model.ProviderName,
				ModelIdentifier: model.ModelIdentifier, ModelName: model.ModelName,
			},
		}
	}
	return summaries, nil
}

// executionModelKey 返回模型服务和模型标识的组合键。
func executionModelKey(providerID, modelIdentifier string) string {
	return providerID + "\x00" + modelIdentifier
}
