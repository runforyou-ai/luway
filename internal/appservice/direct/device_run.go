//go:build server

package direct

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	deviceaction "github.com/runforyou-ai/luway/internal/actions/device"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/knowledgeretrieval"
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

var _ appservice.DeviceRunBackend = (*Backend)(nil)

// deviceIdentity 是已认证的请求设备及其登录身份。
type deviceIdentity struct {
	identity *servermodels.Identity
	device   agentrunaction.RunDevice
}

// AuthenticateDevice 校验实时事件流请求携带的登录令牌与本人未撤销设备，并返回成员会话。
func (b *Backend) AuthenticateDevice(ctx context.Context, meta appservice.RequestMeta) (MemberSession, error) {
	device, err := b.ops.authenticateDevice(ctx, meta)
	if err != nil {
		return MemberSession{}, err
	}
	return NewMemberSession(device.identity), nil
}

// authenticateDevice 先解析登录身份，再校验 DeviceHeader 指向本人未撤销的设备。
func (o *directOperations) authenticateDevice(ctx context.Context, meta appservice.RequestMeta) (deviceIdentity, error) {
	identity, err := o.authenticate(ctx, meta)
	if err != nil {
		return deviceIdentity{}, err
	}
	record, err := o.deviceAuthenticator.Execute(ctx, identity, meta.DeviceID)
	if errors.Is(err, deviceaction.ErrNotFound) {
		return deviceIdentity{}, appservice.NotFoundError(meta, i18n.ErrorDeviceNotFound)
	}
	if err != nil {
		if ctx.Err() != nil {
			return deviceIdentity{}, ctx.Err()
		}
		slog.Warn("设备认证失败", "organization_id", identity.Organization.ID, "error", err)
		return deviceIdentity{}, appservice.FailedError(meta, i18n.ErrorDeviceRunRequestFailed)
	}
	return deviceIdentity{identity: identity, device: agentrunaction.RunDevice{
		OrganizationID: identity.Organization.ID, UserID: identity.User.ID, DeviceID: record.ID,
	}}, nil
}

// GetDeviceWork 返回本设备的工作水位与待领取运行。
func (o *directOperations) GetDeviceWork(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity) (appservice.DeviceWork, error) {
	work, err := o.agentCoordinator.DeviceWork(ctx, device.device)
	if err != nil {
		return appservice.DeviceWork{}, o.deviceRunError(ctx, meta, err, device, "")
	}
	output := appservice.DeviceWork{WorkSeq: work.WorkSeq, Runs: make([]appservice.DeviceWorkRun, 0, len(work.Runs))}
	for _, run := range work.Runs {
		output.Runs = append(output.Runs, appservice.DeviceWorkRun{RunID: run.RunID, ConversationID: run.ConversationID})
	}
	return output, nil
}

// ReportDeviceLocalAgents 保存本设备上报的已安装且可用的本机 Agent。
func (o *directOperations) ReportDeviceLocalAgents(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity, input appservice.DeviceLocalAgentsInput) error {
	kinds := make([]domain.LocalAgentKind, 0, len(input.LocalAgents))
	for _, kind := range input.LocalAgents {
		kinds = append(kinds, domain.LocalAgentKind(kind))
	}
	if err := o.reportLocalAgents.Execute(ctx, device.identity, device.device.DeviceID, kinds); err != nil {
		if errors.Is(err, deviceaction.ErrNotFound) {
			return appservice.NotFoundError(meta, i18n.ErrorDeviceNotFound)
		}
		return o.deviceRunError(ctx, meta, err, device, "")
	}
	return nil
}

// ClaimDeviceRun 领取派发给本设备的排队运行并取得租约。
func (o *directOperations) ClaimDeviceRun(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity, runID string) (appservice.DeviceRunClaim, error) {
	if !common.ValidUUID(runID) {
		return appservice.DeviceRunClaim{}, appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	claim, err := o.agentCoordinator.ClaimDeviceRun(ctx, device.device, runID)
	if err != nil {
		return appservice.DeviceRunClaim{}, o.deviceRunError(ctx, meta, err, device, runID)
	}
	return appservice.DeviceRunClaim{
		Assignment: claim.Assignment, LeaseExpiresAt: claim.LeaseExpiresAt,
		LeaseRenewIntervalSeconds: int(agentrunaction.DeviceRunLeaseRenewInterval.Seconds()),
		RunTimeoutSeconds:         int(agentrunaction.DeviceRunMaxDuration.Seconds()),
	}, nil
}

// RenewDeviceRunLease 为本设备持有的运行续租，运行已结束时返回 ended。
func (o *directOperations) RenewDeviceRunLease(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity, runID string) (appservice.DeviceRunLease, error) {
	if !common.ValidUUID(runID) {
		return appservice.DeviceRunLease{}, appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	lease, err := o.agentCoordinator.RenewDeviceRunLease(ctx, device.device, runID)
	if err != nil {
		return appservice.DeviceRunLease{}, o.deviceRunError(ctx, meta, err, device, runID)
	}
	if lease.Ended {
		return appservice.DeviceRunLease{Ended: true}, nil
	}
	return appservice.DeviceRunLease{LeaseExpiresAt: &lease.LeaseExpiresAt}, nil
}

// PeekDeviceRunInputs 返回本设备持有运行尚未认领的输入信号。
func (o *directOperations) PeekDeviceRunInputs(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity, runID string, input appservice.DeviceRunInputPeekInput) (appservice.DeviceRunInputSignals, error) {
	if !common.ValidUUID(runID) {
		return appservice.DeviceRunInputSignals{}, appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	triggers, err := o.agentCoordinator.PeekDeviceRunInputs(ctx, device.device, runID, int64(input.AfterSeq))
	if err != nil {
		return appservice.DeviceRunInputSignals{}, o.deviceRunError(ctx, meta, err, device, runID)
	}
	output := appservice.DeviceRunInputSignals{Seqs: make([]int64, 0, len(triggers))}
	for _, trigger := range triggers {
		output.Seqs = append(output.Seqs, trigger.Seq)
	}
	return output, nil
}

// ClaimDeviceRunInputs 为本设备持有的运行认领输入并返回截至该边界的上下文消息。
func (o *directOperations) ClaimDeviceRunInputs(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity, runID string, input appservice.DeviceRunInputClaimInput) (appservice.DeviceRunClaimedInput, error) {
	if !common.ValidUUID(runID) {
		return appservice.DeviceRunClaimedInput{}, appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	claimed, err := o.agentCoordinator.ClaimDeviceRunInputs(ctx, device.device, runID, input.ThroughSeq)
	if err != nil {
		return appservice.DeviceRunClaimedInput{}, o.deviceRunError(ctx, meta, err, device, runID)
	}
	if claimed.Suppressed {
		return appservice.DeviceRunClaimedInput{Suppressed: true, Messages: json.RawMessage("[]")}, nil
	}
	messages, err := json.Marshal(claimed.Input.Messages)
	if err != nil {
		return appservice.DeviceRunClaimedInput{}, o.deviceRunError(ctx, meta, fmt.Errorf("encode claimed messages: %w", err), device, runID)
	}
	return appservice.DeviceRunClaimedInput{EndSeq: claimed.Input.EndSeq, Messages: messages}, nil
}

// SearchDeviceRunKnowledge 在本设备持有运行绑定的知识库中检索。
func (o *directOperations) SearchDeviceRunKnowledge(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity, runID string, input appservice.DeviceRunKnowledgeSearchInput) (appservice.DeviceRunKnowledgeSearchResult, error) {
	if !common.ValidUUID(runID) {
		return appservice.DeviceRunKnowledgeSearchResult{}, appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	var request knowledgeretrieval.Request
	if err := json.Unmarshal(input.Request, &request); err != nil {
		return appservice.DeviceRunKnowledgeSearchResult{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	result, err := o.agentCoordinator.SearchDeviceRunKnowledge(ctx, device.device, runID, request)
	if err != nil {
		return appservice.DeviceRunKnowledgeSearchResult{}, o.deviceRunError(ctx, meta, err, device, runID)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return appservice.DeviceRunKnowledgeSearchResult{}, o.deviceRunError(ctx, meta, fmt.Errorf("encode knowledge search result: %w", err), device, runID)
	}
	return appservice.DeviceRunKnowledgeSearchResult{Result: encoded}, nil
}

// GetDeviceRunMemory 返回本设备持有运行所属个人 AI 员工的记忆。
func (o *directOperations) GetDeviceRunMemory(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity, runID string) (appservice.DeviceRunMemory, error) {
	if !common.ValidUUID(runID) {
		return appservice.DeviceRunMemory{}, appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	entries, err := o.agentCoordinator.LoadDeviceRunMemory(ctx, device.device, runID)
	if err != nil {
		return appservice.DeviceRunMemory{}, o.deviceRunError(ctx, meta, err, device, runID)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return appservice.DeviceRunMemory{}, o.deviceRunError(ctx, meta, fmt.Errorf("encode device run memory: %w", err), device, runID)
	}
	return appservice.DeviceRunMemory{Entries: encoded}, nil
}

// ListDeviceRunMCPTools 列出本设备持有运行绑定的企业 MCP 服务及其工具目录，不可用的服务不列出。
func (o *directOperations) ListDeviceRunMCPTools(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity, runID string) (appservice.DeviceRunMCPToolList, error) {
	if !common.ValidUUID(runID) {
		return appservice.DeviceRunMCPToolList{}, appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	servers, err := o.agentCoordinator.ListDeviceRunMCPTools(ctx, device.device, runID)
	if err != nil {
		return appservice.DeviceRunMCPToolList{}, o.deviceRunError(ctx, meta, err, device, runID)
	}
	output := appservice.DeviceRunMCPToolList{Servers: make([]appservice.DeviceRunMCPServer, 0, len(servers))}
	for _, server := range servers {
		tools := make([]appservice.DeviceRunMCPTool, 0, len(server.Tools))
		for _, tool := range server.Tools {
			tools = append(tools, appservice.DeviceRunMCPTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
		}
		output.Servers = append(output.Servers, appservice.DeviceRunMCPServer{ID: server.ID, Name: server.Name, Tools: tools})
	}
	return output, nil
}

// CallDeviceRunMCPTool 为本设备持有的运行调用企业 MCP 工具，服务连接失败与工具报告的失败作为结果返回。
func (o *directOperations) CallDeviceRunMCPTool(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity, runID string, input appservice.DeviceRunMCPToolCallInput) (appservice.DeviceRunMCPToolCallResult, error) {
	if !common.ValidUUID(runID) {
		return appservice.DeviceRunMCPToolCallResult{}, appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	if !common.ValidUUID(input.ServerID) || !json.Valid(input.Arguments) {
		return appservice.DeviceRunMCPToolCallResult{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	result, err := o.agentCoordinator.CallDeviceRunMCPTool(ctx, device.device, runID, input.ServerID, input.ToolName, input.Arguments)
	if errors.Is(err, agentrunaction.ErrDeviceRunMCPToolNotFound) {
		return appservice.DeviceRunMCPToolCallResult{}, appservice.NotFoundError(meta, i18n.ErrorMCPToolNotFound)
	}
	if err != nil {
		return appservice.DeviceRunMCPToolCallResult{}, o.deviceRunError(ctx, meta, err, device, runID)
	}
	return appservice.DeviceRunMCPToolCallResult{Result: result.Result, Error: result.Error}, nil
}

// CompleteDeviceRun 以成功结果收尾本设备持有的运行。
func (o *directOperations) CompleteDeviceRun(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity, runID string, input appservice.DeviceRunResultInput) error {
	if !common.ValidUUID(runID) {
		return appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	result := agentruntime.RunResult{Content: input.Content, EndSeq: input.EndSeq}
	if err := decodeDeviceRunProcess(meta, device, runID, &result, input.Decision, deviceRunProcess{input.Usage, input.Blocks, input.Plan}); err != nil {
		return err
	}
	if err := o.agentCoordinator.CompleteDeviceRun(ctx, device.device, runID, result); err != nil {
		return o.deviceRunError(ctx, meta, err, device, runID)
	}
	return nil
}

// FailDeviceRun 以失败原因收尾派发给本设备的运行。
func (o *directOperations) FailDeviceRun(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity, runID string, input appservice.DeviceRunFailureInput) error {
	if !common.ValidUUID(runID) {
		return appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	partial := agentruntime.RunResult{}
	if err := decodeDeviceRunProcess(meta, device, runID, &partial, nil, deviceRunProcess{input.Usage, input.Blocks, input.Plan}); err != nil {
		return err
	}
	if err := o.agentCoordinator.FailDeviceRun(ctx, device.device, runID, domain.AgentRunErrorCode(input.ErrorCode), input.Message, partial); err != nil {
		return o.deviceRunError(ctx, meta, err, device, runID)
	}
	return nil
}

// deviceRunProcess 是设备透传的用量、过程内容块与任务清单。
type deviceRunProcess struct {
	usage  json.RawMessage
	blocks json.RawMessage
	plan   json.RawMessage
}

// decodeDeviceRunProcess 解码设备透传的结束方式与过程内容，缺省表示直接回答且没有过程内容。
func decodeDeviceRunProcess(meta appservice.RequestMeta, device deviceIdentity, runID string, result *agentruntime.RunResult, decision json.RawMessage, process deviceRunProcess) error {
	for _, part := range []struct {
		data   json.RawMessage
		target any
	}{{decision, &result.Decision}, {process.usage, &result.Usage}, {process.blocks, &result.Blocks}, {process.plan, &result.Plan}} {
		if len(part.data) == 0 || string(part.data) == "null" {
			continue
		}
		if err := json.Unmarshal(part.data, part.target); err != nil {
			slog.Warn("设备运行结果格式无效", "organization_id", device.device.OrganizationID, "device_id", device.device.DeviceID, "agent_run_id", runID, "error", err)
			return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
		}
	}
	return nil
}

// DeviceRunModels 校验模型网关请求来自持有该运行有效租约的本人未撤销设备，并返回经统一调用入口记为该运行调用的对话模型组件工厂。
func (b *Backend) DeviceRunModels(ctx context.Context, meta appservice.RequestMeta, runID string) (agentruntime.ModelFactory, error) {
	device, err := b.ops.authenticateDevice(ctx, meta)
	if err != nil {
		return nil, err
	}
	if !common.ValidUUID(runID) {
		return nil, appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	models, err := b.ops.agentCoordinator.DeviceRunModels(ctx, device.device, runID)
	if err != nil {
		return nil, b.ops.deviceRunError(ctx, meta, err, device, runID)
	}
	return models, nil
}

// ReadDeviceRunAttachment 校验请求来自持有该运行有效租约的本人未撤销设备，并返回运行所属会话中指定附件消息的文件内容。
func (b *Backend) ReadDeviceRunAttachment(ctx context.Context, meta appservice.RequestMeta, runID, messageID string) ([]byte, error) {
	device, err := b.ops.authenticateDevice(ctx, meta)
	if err != nil {
		return nil, err
	}
	if !common.ValidUUID(runID) {
		return nil, appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	content, err := b.ops.agentCoordinator.ReadDeviceRunAttachment(ctx, device.device, runID, messageID)
	if errors.Is(err, agentrunaction.ErrAttachmentUnavailable) {
		return nil, appservice.NotFoundError(meta, i18n.ErrorFileNotFound)
	}
	if err != nil {
		return nil, b.ops.deviceRunError(ctx, meta, err, device, runID)
	}
	return content, nil
}

// deviceRunError 转换设备运行期错误：运行不存在、不可领取与租约失效给出稳定原因码，其余记录日志后按请求失败收敛。
func (o *directOperations) deviceRunError(ctx context.Context, meta appservice.RequestMeta, err error, device deviceIdentity, runID string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case errors.Is(err, agentrunaction.ErrDeviceRunNotFound):
		return appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	case errors.Is(err, agentrunaction.ErrDeviceRunUnavailable):
		return appservice.ConflictError(meta, i18n.ErrorDeviceRunUnavailable, "run_unavailable")
	case errors.Is(err, agentrunaction.ErrDeviceRunLeaseLost):
		return appservice.ConflictError(meta, i18n.ErrorDeviceRunLeaseLost, "lease_lost")
	case errors.Is(err, agentrunaction.ErrDeviceRunFailureCodeInvalid):
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	slog.Warn("设备运行请求失败", "organization_id", device.device.OrganizationID, "device_id", device.device.DeviceID,
		"agent_run_id", runID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorDeviceRunRequestFailed)
}

// SearchDeviceRunWeb 用企业配置的搜索服务为本设备持有的运行搜索互联网，搜索服务的失败按原因转换。
func (o *directOperations) SearchDeviceRunWeb(ctx context.Context, meta appservice.RequestMeta, device deviceIdentity, runID string, input appservice.DeviceRunWebSearchInput) (appservice.DeviceRunWebSearchResult, error) {
	if !common.ValidUUID(runID) {
		return appservice.DeviceRunWebSearchResult{}, appservice.NotFoundError(meta, i18n.ErrorDeviceRunNotFound)
	}
	var request websearch.Request
	if err := json.Unmarshal(input.Request, &request); err != nil {
		return appservice.DeviceRunWebSearchResult{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	result, err := o.agentCoordinator.SearchDeviceRunWeb(ctx, device.device, runID, request)
	if _, _, classified := connectiontest.Details(err); classified {
		return appservice.DeviceRunWebSearchResult{}, webSearchError(ctx, meta, err)
	}
	if err != nil {
		return appservice.DeviceRunWebSearchResult{}, o.deviceRunError(ctx, meta, err, device, runID)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return appservice.DeviceRunWebSearchResult{}, o.deviceRunError(ctx, meta, fmt.Errorf("encode web search result: %w", err), device, runID)
	}
	return appservice.DeviceRunWebSearchResult{Result: encoded}, nil
}
