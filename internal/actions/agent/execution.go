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
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

const (
	// executionSchemaVersion 是执行配置版本 configuration 的结构版本。
	executionSchemaVersion = 1
	// maxSystemInstructionLength 是企业指令的最大字符数。
	maxSystemInstructionLength = 20000
)

// ExecutionInput 定义执行配置输入，按执行方式填写 Managed。
type ExecutionInput struct {
	Mode    domain.AgentExecutionMode
	Managed *ManagedExecutionInput
}

// ManagedExecutionInput 定义平台托管执行配置输入。
type ManagedExecutionInput struct {
	ModelID           string
	SystemInstruction string
	KnowledgeBaseIDs  []string
}

// Execution 定义当前生效的执行配置。
type Execution struct {
	BusinessSystems []domain.BusinessSystemGrant
	RevisionID      string
	Mode            domain.AgentExecutionMode
	Managed         *ManagedExecution
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
}

// ManagedExecutionSummary 定义平台托管执行配置摘要。
type ManagedExecutionSummary struct {
	Model aimodel.Option
}

// managedRevisionConfigurationV1 是平台托管执行配置版本保存的 configuration 结构。
type managedRevisionConfigurationV1 struct {
	BusinessSystems   []domain.BusinessSystemGrant `json:"businessSystems"`
	SystemInstruction string                       `json:"systemInstruction"`
	KnowledgeBaseIDs  []string                     `json:"knowledgeBaseIds"`
}

// normalizeExecutionInput 规范化执行配置，执行方式须为平台托管并给出托管配置。
func normalizeExecutionInput(input ExecutionInput) (ExecutionInput, error) {
	if input.Mode != domain.AgentExecutionModeManaged || input.Managed == nil {
		return ExecutionInput{}, &common.FieldError{Fields: map[string]common.FieldCode{"execution": ValidationExecutionInvalid}}
	}
	// 规范化托管执行的模型编号、系统指令，知识库编号按集合保存。
	managed := *input.Managed
	managed.ModelID, _ = str.NormalizeUUID(managed.ModelID)
	managed.SystemInstruction = strings.TrimSpace(managed.SystemInstruction)
	managed.KnowledgeBaseIDs, _ = common.NormalizeUUIDs(managed.KnowledgeBaseIDs)
	slices.Sort(managed.KnowledgeBaseIDs)
	input.Managed = &managed
	return input, nil
}

// lockManagedExecutionModel 校验并锁定平台托管执行配置使用的 AI 员工对话模型。
func lockManagedExecutionModel(ctx context.Context, tx bun.Tx, workspaceID string, input ManagedExecutionInput) (aimodel.Option, error) {
	model, err := aimodel.Lock(ctx, tx, workspaceID, input.ModelID, domain.AIModelUsageAgent)
	if errors.Is(err, aimodel.ErrUnavailable) {
		return aimodel.Option{}, &common.FieldError{Fields: map[string]common.FieldCode{"modelId": ValidationModelInvalid}}
	}
	if err != nil {
		return aimodel.Option{}, err
	}
	return model.Option(), nil
}

// lockExecutionKnowledgeBases 校验并锁定托管执行绑定的同企业知识库直至事务结束，须在锁定 AI 员工之前调用。
func lockExecutionKnowledgeBases(ctx context.Context, db bun.IDB, workspaceID string, input ExecutionInput) error {
	if input.Managed == nil || len(input.Managed.KnowledgeBaseIDs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(input.Managed.KnowledgeBaseIDs))
	if err := db.NewSelect().Model((*servermodels.KnowledgeBase)(nil)).
		Column("id").Where("kb.workspace_id = ?", workspaceID).
		Where("kb.id IN (?)", bun.List(input.Managed.KnowledgeBaseIDs)).
		OrderExpr("kb.id ASC").For("KEY SHARE").Scan(ctx, &ids); err != nil {
		return err
	}
	if len(ids) != len(input.Managed.KnowledgeBaseIDs) {
		return &common.FieldError{Fields: map[string]common.FieldCode{"knowledgeBaseIds": ValidationKnowledgeBaseInvalid}}
	}
	return nil
}

// insertExecutionRevision 创建执行配置版本，绑定的知识库须已由 lockExecutionKnowledgeBases 锁定。
func insertExecutionRevision(ctx context.Context, db bun.IDB, identity *servermodels.Identity, agentID, revisionID string, input ExecutionInput, model aimodel.Option, businessSystems []domain.BusinessSystemGrant) (Execution, error) {
	configuration := managedRevisionConfigurationV1{
		BusinessSystems:   businessSystems,
		SystemInstruction: input.Managed.SystemInstruction,
		KnowledgeBaseIDs:  input.Managed.KnowledgeBaseIDs,
	}
	if err := insertRevision(ctx, db, identity, agentID, revisionID, input.Mode, model.ID, configuration); err != nil {
		return Execution{}, err
	}
	return Execution{
		RevisionID: revisionID, Mode: input.Mode, BusinessSystems: businessSystems,
		Managed: &ManagedExecution{
			Model:             model,
			SystemInstruction: input.Managed.SystemInstruction,
			KnowledgeBaseIDs:  input.Managed.KnowledgeBaseIDs,
		},
	}, nil
}

// insertRevision 写入一条不可变执行配置版本及其绑定的知识库与授权的业务系统。
func insertRevision(ctx context.Context, db bun.IDB, identity *servermodels.Identity, agentID, revisionID string, mode domain.AgentExecutionMode, modelID string, configuration managedRevisionConfigurationV1) error {
	encoded, err := json.Marshal(configuration)
	if err != nil {
		return err
	}
	revision := &servermodels.AgentRevision{
		ID: revisionID, WorkspaceID: identity.Workspace.ID, AgentID: agentID,
		ExecutionMode: string(mode), ModelID: modelID, SchemaVersion: executionSchemaVersion,
		Configuration: encoded, CreatedByUserID: identity.User.ID,
	}
	if _, err := db.NewInsert().Model(revision).
		Column("id", "workspace_id", "agent_id", "execution_mode", "model_id", "schema_version", "configuration", "created_by_user_id").
		Exec(ctx); err != nil {
		return err
	}
	// 按配置快照写入知识库引用。
	if len(configuration.KnowledgeBaseIDs) > 0 {
		references := arr.Map(configuration.KnowledgeBaseIDs, func(knowledgeBaseID string) servermodels.AgentRevisionKnowledgeBase {
			return servermodels.AgentRevisionKnowledgeBase{
				WorkspaceID: identity.Workspace.ID, AgentID: agentID, RevisionID: revisionID, KnowledgeBaseID: knowledgeBaseID,
			}
		})
		if _, err := db.NewInsert().Model(&references).
			Column("workspace_id", "agent_id", "revision_id", "knowledge_base_id").Exec(ctx); err != nil {
			return err
		}
	}
	// 按配置快照写入业务系统引用。
	if len(configuration.BusinessSystems) > 0 {
		references := arr.Map(configuration.BusinessSystems, func(grant domain.BusinessSystemGrant) servermodels.AgentRevisionBusinessSystem {
			return servermodels.AgentRevisionBusinessSystem{
				WorkspaceID: identity.Workspace.ID, AgentID: agentID, RevisionID: revisionID, BusinessSystemID: grant.BusinessSystemID,
			}
		})
		if _, err := db.NewInsert().Model(&references).
			Column("workspace_id", "agent_id", "revision_id", "business_system_id").Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// decodeRevisionConfiguration 校验执行方式与结构版本并解码平台托管执行配置快照。
func decodeRevisionConfiguration(revision servermodels.AgentRevision) (managedRevisionConfigurationV1, error) {
	mode := domain.AgentExecutionMode(revision.ExecutionMode)
	if revision.SchemaVersion != executionSchemaVersion {
		return managedRevisionConfigurationV1{}, fmt.Errorf("unsupported %s execution schema version %d", mode, revision.SchemaVersion)
	}
	if mode != domain.AgentExecutionModeManaged {
		return managedRevisionConfigurationV1{}, fmt.Errorf("unsupported agent execution mode %q", revision.ExecutionMode)
	}
	decoder := json.NewDecoder(bytes.NewReader(revision.Configuration))
	decoder.DisallowUnknownFields()
	configuration := managedRevisionConfigurationV1{}
	if err := decoder.Decode(&configuration); err != nil {
		return managedRevisionConfigurationV1{}, fmt.Errorf("decode managed execution configuration: %w", err)
	}
	return configuration, nil
}

// decodeRevisionExecution 解码不可变执行配置版本，托管执行的模型只填写编号。
func decodeRevisionExecution(revision servermodels.AgentRevision) (Execution, error) {
	mode := domain.AgentExecutionMode(revision.ExecutionMode)
	configuration, err := decodeRevisionConfiguration(revision)
	if err != nil {
		return Execution{}, err
	}
	if !str.IsUUID(revision.ModelID) || utf8.RuneCountInString(configuration.SystemInstruction) > maxSystemInstructionLength {
		return Execution{}, errors.New("managed execution configuration is invalid")
	}
	return Execution{
		RevisionID: revision.ID, Mode: mode, BusinessSystems: configuration.BusinessSystems,
		Managed: &ManagedExecution{
			Model:             aimodel.Option{ID: revision.ModelID},
			SystemInstruction: configuration.SystemInstruction,
			KnowledgeBaseIDs:  configuration.KnowledgeBaseIDs,
		},
	}, nil
}

// loadAgentExecution 读取 AI 员工当前生效的执行配置。
func loadAgentExecution(ctx context.Context, db bun.IDB, workspaceID, agentID string) (Execution, error) {
	revision := &servermodels.AgentRevision{}
	if err := db.NewSelect().Model(revision).
		Join("JOIN agents AS a ON a.active_revision_id = ar.id AND a.workspace_id = ar.workspace_id AND a.id = ar.agent_id").
		Where("a.workspace_id = ?", workspaceID).
		Where("a.id = ?", agentID).
		Scan(ctx); err != nil {
		return Execution{}, err
	}
	execution, err := decodeRevisionExecution(*revision)
	if err != nil {
		return execution, err
	}
	// 读取已保存平台托管配置当前对应的模型。
	models, err := aimodel.LoadOptions(ctx, db, workspaceID, []string{execution.Managed.Model.ID}, domain.AIModelUsageAgent)
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
func loadAgentExecutionSummaries(ctx context.Context, db bun.IDB, workspaceID string, agentIDs []string) (map[string]ExecutionSummary, error) {
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
		Join("JOIN agent_revisions AS ar ON ar.id = a.active_revision_id AND ar.workspace_id = a.workspace_id AND ar.agent_id = a.id").
		Where("a.workspace_id = ?", workspaceID).
		Where("a.id IN (?)", bun.List(agentIDs)).
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
		modelIDs = append(modelIDs, execution.Managed.Model.ID)
	}
	models, err := aimodel.LoadOptions(ctx, db, workspaceID, modelIDs, domain.AIModelUsageAgent)
	if err != nil {
		return nil, err
	}
	summaries := make(map[string]ExecutionSummary, len(executions))
	for agentID, execution := range executions {
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
