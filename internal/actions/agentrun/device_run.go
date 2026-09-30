//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/knowledgeretrieval"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	// DeviceRunSweepActionName 是设备运行收敛扫描任务名。
	DeviceRunSweepActionName = "agent.device_run_sweep"
	// DeviceRunLeaseTTL 是设备领取或续租后租约的有效时长。
	DeviceRunLeaseTTL = 45 * time.Second
	// DeviceRunLeaseRenewInterval 是设备续租的间隔。
	DeviceRunLeaseRenewInterval = 10 * time.Second
	// DeviceRunMaxDuration 是设备运行自领取起的总时限，超出后不再续租、不再代理模型请求。
	DeviceRunMaxDuration = 30 * time.Minute
	// deviceRunSweepBatch 是单次扫描收敛的运行数量上限。
	deviceRunSweepBatch = 100
	// deviceRunRouteJoins 关联运行所属助理。
	deviceRunRouteJoins = `JOIN agents AS a ON a.organization_id = agr.organization_id AND a.identity_id = agr.agent_identity_id`
	// deviceRunRouteCurrent 判断助理仍绑定运行的执行设备。
	deviceRunRouteCurrent = `a.device_id = agr.execution_device_id`
)

var (
	// ErrDeviceRunNotFound 表示运行不存在或不由当前设备执行。
	ErrDeviceRunNotFound = errors.New("device agent run not found")
	// ErrDeviceRunUnavailable 表示运行已不处于可领取状态。
	ErrDeviceRunUnavailable = errors.New("device agent run is not claimable")
	// ErrDeviceRunLeaseLost 表示设备已不持有运行的有效租约。
	ErrDeviceRunLeaseLost = errors.New("device agent run lease lost")
	// ErrDeviceRunFailureCodeInvalid 表示设备上报的失败原因不在允许范围内。
	ErrDeviceRunFailureCodeInvalid = errors.New("device agent run failure code is invalid")
)

// RunDevice 标识以本机设备身份调用运行期接口的已认证设备。
type RunDevice struct {
	OrganizationID string
	UserID         string
	DeviceID       string
}

// DeviceWorkRun 是设备待领取运行的摘要。
type DeviceWorkRun struct {
	RunID          string `bun:"id"`
	ConversationID string `bun:"conversation_id"`
}

// DeviceWork 是设备当前工作水位与待领取运行。
type DeviceWork struct {
	WorkSeq int64
	Runs    []DeviceWorkRun
}

// DeviceClaim 是设备领取运行后得到的有效配置与租约。
type DeviceClaim struct {
	Assignment     json.RawMessage
	LeaseExpiresAt time.Time
}

// DeviceLease 是设备续租结果，Ended 表示运行已结束，设备应停止执行。
type DeviceLease struct {
	Ended          bool
	LeaseExpiresAt time.Time
}

// DeviceModelUpstream 是设备模型代理转发的上游模型服务与供应商凭据。
type DeviceModelUpstream struct {
	Brand      string
	BaseURL    string
	APIKey     string
	Identifier string
}

// DeviceClaimedInput 是设备认领输入的结果，Suppressed 表示运行已失效，设备应停止执行。
type DeviceClaimedInput struct {
	Suppressed bool
	Input      agentruntime.ClaimedInput
}

// DeviceWork 返回设备的工作水位与按创建顺序排列、助理仍绑定该设备的待领取运行。
func (a *ExecuteAction) DeviceWork(ctx context.Context, device RunDevice) (DeviceWork, error) {
	work := DeviceWork{Runs: make([]DeviceWorkRun, 0)}
	if err := a.db.NewSelect().Model((*servermodels.Device)(nil)).Column("work_seq").
		Where("d.organization_id = ? AND d.id = ?", device.OrganizationID, device.DeviceID).
		Scan(ctx, &work.WorkSeq); err != nil {
		return DeviceWork{}, fmt.Errorf("load device work sequence: %w", err)
	}
	if err := a.db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		ColumnExpr("agr.id, agr.conversation_id").
		// 助理已换电脑的运行不再交给设备，由收敛扫描标记失败。
		Join(deviceRunRouteJoins).
		Where(deviceRunRouteCurrent).
		Where("agr.organization_id = ? AND agr.execution_device_id = ?", device.OrganizationID, device.DeviceID).
		Where("agr.status = ?", domain.AgentRunStatusQueued).
		OrderExpr("agr.created_at ASC, agr.id ASC").
		Scan(ctx, &work.Runs); err != nil {
		return DeviceWork{}, fmt.Errorf("list device queued agent runs: %w", err)
	}
	return work, nil
}

// ClaimDeviceRun 由执行设备领取排队中的运行：标记运行中并取得租约，再按领取设备的能力解析并固定有效配置。
func (a *ExecuteAction) ClaimDeviceRun(ctx context.Context, device RunDevice, runID string) (DeviceClaim, error) {
	initial, err := a.loadDeviceRun(ctx, device, runID)
	if err != nil {
		return DeviceClaim{}, err
	}
	if initial.Status != string(domain.AgentRunStatusQueued) {
		return DeviceClaim{}, ErrDeviceRunUnavailable
	}
	policy, err := a.policyForRun(ctx, initial)
	if err != nil {
		return DeviceClaim{}, err
	}
	claim := DeviceClaim{}
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockAgentRun(ctx, tx, policy, initial)
		if err != nil {
			return err
		}
		run := locked.Run
		if run.Status != string(domain.AgentRunStatusQueued) {
			return ErrDeviceRunUnavailable
		}
		// 助理已换电脑时拒绝领取，由收敛扫描标记失败。
		current, err := tx.NewSelect().Model((*servermodels.AgentRun)(nil)).
			Join(deviceRunRouteJoins).
			Where("agr.id = ?", run.ID).
			Where(deviceRunRouteCurrent).
			Exists(ctx)
		if err != nil {
			return fmt.Errorf("check device run route: %w", err)
		}
		if !current {
			return ErrDeviceRunUnavailable
		}
		if err := tx.NewRaw(`
			UPDATE agent_runs
			SET status = ?, started_at = COALESCE(started_at, now()), claimed_at = now(),
				lease_expires_at = now() + make_interval(secs => ?), updated_at = now()
			WHERE id = ?
			RETURNING lease_expires_at
		`, domain.AgentRunStatusRunning, DeviceRunLeaseTTL.Seconds(), run.ID).Scan(ctx, &claim.LeaseExpiresAt); err != nil {
			return fmt.Errorf("claim device agent run: %w", err)
		}
		// 运行期间以助理的聊天主体发布输入状态。
		if _, err := chatstate.EnsureOrganizationIdentityChatSubject(ctx, tx, run.OrganizationID, run.AgentIdentityID, uuid.NewV7().String()); err != nil {
			return err
		}
		return chatstate.TouchConversation(ctx, tx, locked.PolicyContext.Conversation, domain.ConversationChangeTimeline)
	})
	if err != nil {
		return DeviceClaim{}, err
	}
	if claim.Assignment, err = a.deviceAssignment(ctx, runID, policy); err != nil {
		// 运行已转为运行中但设备拿不到有效配置，立即以失败结束，不留下无人执行的运行。
		if _, failErr := a.fail(context.WithoutCancel(ctx), runID, policy, err, domain.AgentRunErrorCodeDeviceRunFailed); failErr != nil {
			return DeviceClaim{}, fmt.Errorf("resolve device agent run assignment: %v; fail run: %w", err, failErr)
		}
		return DeviceClaim{}, err
	}
	a.holdDeviceRunTyping(ctx, initial, claim.LeaseExpiresAt)
	slog.Info("设备已领取 Agent 运行", "organization_id", device.OrganizationID, "device_id", device.DeviceID,
		"agent_run_id", runID)
	return claim, nil
}

// deviceAssignment 按领取设备的能力解析并固定已领取运行的有效配置，返回运行时的不透明 JSON。
func (a *ExecuteAction) deviceAssignment(ctx context.Context, runID string, policy agentRunPolicy) (json.RawMessage, error) {
	execution, terminal, err := a.loadExecution(ctx, runID)
	if err != nil {
		return nil, err
	}
	if terminal {
		return nil, ErrDeviceRunUnavailable
	}
	shared, err := a.loadRunCapabilities(ctx, &execution.Run)
	if err != nil {
		return nil, err
	}
	// 配置版本绑定知识库、企业 MCP 服务与企业启用联网搜索时经服务端检索、调用和搜索，网页在本机读取，本机工具全部提供，助理记忆经服务端读取。
	capabilities := shared.capabilities
	capabilities.Knowledge = len(execution.KnowledgeBaseIDs) > 0
	capabilities.LocalTools = agentruntime.LocalTools()
	capabilities.Memory = true
	assignment, err := a.resolveAssignment(ctx, execution, policy, capabilities)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(assignment)
	if err != nil {
		return nil, fmt.Errorf("encode device agent run assignment: %w", err)
	}
	return encoded, nil
}

// RenewDeviceRunLease 为设备持有的运行续租；运行已结束时返回 Ended，租约已过期或运行超出总时限时返回 ErrDeviceRunLeaseLost。
func (a *ExecuteAction) RenewDeviceRunLease(ctx context.Context, device RunDevice, runID string) (DeviceLease, error) {
	run, err := a.loadDeviceRun(ctx, device, runID)
	if err != nil {
		return DeviceLease{}, err
	}
	if agentRunStatusTerminal(run.Status) {
		a.releaseDeviceRun(runID)
		return DeviceLease{Ended: true}, nil
	}
	lease := DeviceLease{}
	err = a.db.NewRaw(`
		UPDATE agent_runs
		SET lease_expires_at = now() + make_interval(secs => ?), updated_at = now()
		WHERE id = ? AND status = ? AND lease_expires_at > clock_timestamp()
			AND claimed_at + make_interval(secs => ?) > clock_timestamp()
		RETURNING lease_expires_at
	`, DeviceRunLeaseTTL.Seconds(), run.ID, domain.AgentRunStatusRunning, DeviceRunMaxDuration.Seconds()).Scan(ctx, &lease.LeaseExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		// 续租与停止或收敛并发时按最新状态判断。
		current, reloadErr := a.loadDeviceRun(ctx, device, runID)
		if reloadErr != nil {
			return DeviceLease{}, reloadErr
		}
		a.releaseDeviceRun(runID)
		if agentRunStatusTerminal(current.Status) {
			return DeviceLease{Ended: true}, nil
		}
		return DeviceLease{}, ErrDeviceRunLeaseLost
	}
	if err != nil {
		return DeviceLease{}, fmt.Errorf("renew device agent run lease: %w", err)
	}
	a.holdDeviceRunTyping(ctx, run, lease.LeaseExpiresAt)
	return lease, nil
}

// ResolveDeviceModelUpstream 返回设备持有有效租约的运行按配置版本锁定的模型服务与当前供应商凭据。
func (a *ExecuteAction) ResolveDeviceModelUpstream(ctx context.Context, device RunDevice, runID string) (DeviceModelUpstream, error) {
	if _, err := a.requireDeviceLease(ctx, device, runID); err != nil {
		return DeviceModelUpstream{}, err
	}
	execution, terminal, err := a.loadExecution(ctx, runID)
	if err != nil {
		return DeviceModelUpstream{}, err
	}
	if terminal {
		return DeviceModelUpstream{}, ErrDeviceRunLeaseLost
	}
	// 本机 Agent 执行的运行使用本机 Agent 自身的模型，不经模型代理。
	if execution.ExecutionMode != domain.AgentExecutionModeManaged {
		return DeviceModelUpstream{}, ErrDeviceRunUnavailable
	}
	return DeviceModelUpstream{
		Brand: execution.Brand, BaseURL: execution.APIURL, APIKey: execution.APIKey, Identifier: execution.ModelIdentifier,
	}, nil
}

// PeekDeviceRunInputs 返回设备持有运行尚未进入 TurnLoop 缓冲区的连续输入信号。
func (a *ExecuteAction) PeekDeviceRunInputs(ctx context.Context, device RunDevice, runID string, afterSeq int64) ([]agentruntime.Trigger, error) {
	run, err := a.requireDeviceLease(ctx, device, runID)
	if err != nil {
		return nil, err
	}
	feed := &databaseInputFeed{db: a.db, execution: executionContext{Run: *run}}
	return feed.Peek(ctx, afterSeq)
}

// ClaimDeviceRunInputs 为设备持有的运行认领截至指定序号的输入，并按运行策略重建截至该边界的会话上下文。
func (a *ExecuteAction) ClaimDeviceRunInputs(ctx context.Context, device RunDevice, runID string, throughSeq int64) (DeviceClaimedInput, error) {
	run, err := a.requireDeviceLease(ctx, device, runID)
	if err != nil {
		return DeviceClaimedInput{}, err
	}
	policy, err := a.policyForRun(ctx, run)
	if err != nil {
		return DeviceClaimedInput{}, err
	}
	feed := &databaseInputFeed{db: a.db, enqueuer: a.enqueuer, execution: executionContext{Run: *run}, policy: policy, attachments: a.attachments}
	claimed, err := feed.Claim(withDeviceLease(ctx, device.DeviceID), throughSeq)
	if errors.Is(err, errAgentRunSuppressed) {
		return DeviceClaimedInput{Suppressed: true}, nil
	}
	if err != nil {
		return DeviceClaimedInput{}, err
	}
	return DeviceClaimedInput{Input: claimed}, nil
}

// SearchDeviceRunKnowledge 在设备持有运行的配置版本绑定的知识库中检索。
func (a *ExecuteAction) SearchDeviceRunKnowledge(ctx context.Context, device RunDevice, runID string, request knowledgeretrieval.Request) (knowledgeretrieval.Result, error) {
	if _, err := a.requireDeviceLease(ctx, device, runID); err != nil {
		return knowledgeretrieval.Result{}, err
	}
	execution, terminal, err := a.loadExecution(ctx, runID)
	if err != nil {
		return knowledgeretrieval.Result{}, err
	}
	if terminal {
		return knowledgeretrieval.Result{}, ErrDeviceRunLeaseLost
	}
	search, err := loadKnowledgeSearch(ctx, a.db, a.knowledge, execution.Run.OrganizationID, execution.KnowledgeBaseIDs)
	if err != nil {
		return knowledgeretrieval.Result{}, fmt.Errorf("load device agent run knowledge bases: %w", err)
	}
	if search == nil {
		return knowledgeretrieval.Result{}, errors.New("device agent run has no bound knowledge bases")
	}
	return search(ctx, request)
}

// ReadDeviceRunAttachment 读取设备持有运行所属会话中指定附件消息的文件内容。
func (a *ExecuteAction) ReadDeviceRunAttachment(ctx context.Context, device RunDevice, runID, messageID string) ([]byte, error) {
	run, err := a.requireDeviceLease(ctx, device, runID)
	if err != nil {
		return nil, err
	}
	return a.attachments.Content(ctx, run, messageID)
}

// CompleteDeviceRun 按设备上报的运行结果收尾；运行已进入终态时只保留过程内容，重复上报保持幂等；租约已过期或运行超出总时限时收敛为失败、保留过程内容并返回 ErrDeviceRunLeaseLost。
func (a *ExecuteAction) CompleteDeviceRun(ctx context.Context, device RunDevice, runID string, result agentruntime.RunResult) error {
	run, err := a.loadDeviceRun(ctx, device, runID)
	if err != nil {
		return err
	}
	if agentRunStatusTerminal(run.Status) {
		a.releaseDeviceRun(runID)
		return a.persistPartialProcess(ctx, run, result)
	}
	if code := deviceRunExpiry(run); code != "" {
		if err := a.expireDeviceRun(ctx, run, code, result); err != nil {
			return err
		}
		return ErrDeviceRunLeaseLost
	}
	execution, terminal, err := a.loadExecution(ctx, runID)
	if err != nil {
		return err
	}
	if terminal {
		a.releaseDeviceRun(runID)
		return nil
	}
	policy, err := a.policyForRun(ctx, run)
	if err != nil {
		return err
	}
	completed, err := a.complete(withDeviceLease(ctx, device.DeviceID), execution, policy, result)
	if err != nil {
		return fmt.Errorf("persist completed device agent run: %w", err)
	}
	a.releaseDeviceRun(runID)
	// 迟到结果被门禁抑制或运行已结束时保留已产生的过程内容。
	if completed {
		return nil
	}
	return a.persistPartialProcess(ctx, &execution.Run, result)
}

// FailDeviceRun 按设备上报的失败原因收尾运行并保留已产生的过程内容；运行中的运行在租约已过期或超出总时限时按对应原因收敛，已进入终态的运行只保留过程内容。
func (a *ExecuteAction) FailDeviceRun(ctx context.Context, device RunDevice, runID string, code domain.AgentRunErrorCode, message string, partial agentruntime.RunResult) error {
	if code != domain.AgentRunErrorCodeDeviceRunFailed && code != domain.AgentRunErrorCodeLocalAgentAuthRequired {
		return ErrDeviceRunFailureCodeInvalid
	}
	run, err := a.loadDeviceRun(ctx, device, runID)
	if err != nil {
		return err
	}
	if agentRunStatusTerminal(run.Status) {
		a.releaseDeviceRun(runID)
		return a.persistPartialProcess(ctx, run, partial)
	}
	if code := deviceRunExpiry(run); run.Status == string(domain.AgentRunStatusRunning) && code != "" {
		return a.expireDeviceRun(ctx, run, code, partial)
	}
	if message == "" {
		message = string(code)
	}
	if _, err := a.fail(withDeviceLease(ctx, device.DeviceID), runID, nil, errors.New(message), code); err != nil {
		return fmt.Errorf("fail device agent run: %w", err)
	}
	a.releaseDeviceRun(runID)
	slog.Info("设备上报 Agent 运行失败", "organization_id", device.OrganizationID, "device_id", device.DeviceID,
		"agent_run_id", runID, "error_code", code)
	return a.persistPartialProcess(ctx, run, partial)
}

// expireDeviceRun 以租约过期或超出总时限的原因结束运行中的设备运行，并保留设备回传的过程内容。
func (a *ExecuteAction) expireDeviceRun(ctx context.Context, run *servermodels.AgentRun, code domain.AgentRunErrorCode, partial agentruntime.RunResult) error {
	if _, err := a.fail(ctx, run.ID, nil, errors.New(string(code)), code); err != nil {
		return fmt.Errorf("expire device agent run: %w", err)
	}
	a.releaseDeviceRun(run.ID)
	slog.Info("设备 Agent 运行已收敛为失败", "agent_run_id", run.ID, "error_code", code)
	return a.persistPartialProcess(ctx, run, partial)
}

// SweepDeviceRuns 收敛无法继续的设备运行：运行中但租约已过期或超出总时限，或排队中但设备已撤销、设备主人已停用或助理已换电脑。
func (a *ExecuteAction) SweepDeviceRuns(ctx context.Context, _ struct{}) error {
	var stale []struct {
		ID   string                   `bun:"id"`
		Code domain.AgentRunErrorCode `bun:"code"`
	}
	if err := a.db.NewRaw(`
		SELECT agr.id,
			CASE
				WHEN agr.status = ? AND agr.claimed_at + make_interval(secs => ?) <= clock_timestamp() THEN ?
				WHEN agr.status = ? THEN ?
				WHEN d.id IS NULL OR d.revoked_at IS NOT NULL OR u.id IS NULL THEN ?
				ELSE ?
			END AS code
		FROM agent_runs AS agr
		LEFT JOIN devices AS d ON d.id = agr.execution_device_id AND d.organization_id = agr.organization_id
		LEFT JOIN users AS u ON u.id = d.user_id AND u.organization_id = d.organization_id AND u.status = ?
		LEFT JOIN agents AS a ON a.organization_id = agr.organization_id AND a.identity_id = agr.agent_identity_id
		WHERE agr.execution_device_id IS NOT NULL
			AND (
				(agr.status = ? AND (agr.lease_expires_at <= clock_timestamp() OR agr.claimed_at + make_interval(secs => ?) <= clock_timestamp()))
				OR (agr.status = ? AND (
					d.id IS NULL OR d.revoked_at IS NOT NULL OR u.id IS NULL
					OR a.device_id IS DISTINCT FROM agr.execution_device_id
				))
			)
		ORDER BY agr.created_at ASC
		LIMIT ?
	`, domain.AgentRunStatusRunning, DeviceRunMaxDuration.Seconds(), domain.AgentRunErrorCodeDeviceRunTimedOut,
		domain.AgentRunStatusRunning, domain.AgentRunErrorCodeDeviceLeaseExpired, domain.AgentRunErrorCodeDeviceUnavailable, domain.AgentRunErrorCodeExecutionChanged,
		domain.IdentityStatusActive, domain.AgentRunStatusRunning, DeviceRunMaxDuration.Seconds(), domain.AgentRunStatusQueued, deviceRunSweepBatch).Scan(ctx, &stale); err != nil {
		return fmt.Errorf("find stale device agent runs: %w", err)
	}
	// 单条收敛失败时记录并继续处理其余运行。
	for _, run := range stale {
		if _, err := a.fail(ctx, run.ID, nil, errors.New(string(run.Code)), run.Code); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Warn("收敛设备 Agent 运行失败", "agent_run_id", run.ID, "error_code", run.Code, "error", err)
			continue
		}
		a.releaseDeviceRun(run.ID)
		slog.Info("设备 Agent 运行已收敛为失败", "agent_run_id", run.ID, "error_code", run.Code)
	}
	return nil
}

// loadDeviceRun 读取由指定设备执行的运行。
func (a *ExecuteAction) loadDeviceRun(ctx context.Context, device RunDevice, runID string) (*servermodels.AgentRun, error) {
	run := &servermodels.AgentRun{}
	err := a.db.NewSelect().Model(run).
		Where("agr.organization_id = ? AND agr.id = ?", device.OrganizationID, runID).
		Where("agr.execution_device_id = ?", device.DeviceID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDeviceRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load device agent run: %w", err)
	}
	return run, nil
}

// requireDeviceLease 读取设备持有有效租约且未超出总时限的运行中运行。
func (a *ExecuteAction) requireDeviceLease(ctx context.Context, device RunDevice, runID string) (*servermodels.AgentRun, error) {
	run, err := a.loadDeviceRun(ctx, device, runID)
	if err != nil {
		return nil, err
	}
	// 运行须处于运行中，设备租约尚未过期且未超出总时限。
	if run.Status != string(domain.AgentRunStatusRunning) || deviceRunExpiry(run) != "" {
		return nil, ErrDeviceRunLeaseLost
	}
	return run, nil
}

type deviceLeaseKey struct{}

// withDeviceLease 标记本次调用来自执行设备，事务锁定运行后据此再次校验设备租约。
func withDeviceLease(ctx context.Context, deviceID string) context.Context {
	return context.WithValue(ctx, deviceLeaseKey{}, deviceID)
}

// checkDeviceLease 在事务锁定运行后校验设备调用：运行必须由该设备执行，运行中的运行要求租约尚未过期且未超出总时限。
func checkDeviceLease(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) error {
	deviceID, ok := ctx.Value(deviceLeaseKey{}).(string)
	if !ok {
		return nil
	}
	if run.ExecutionDeviceID == nil || *run.ExecutionDeviceID != deviceID {
		return ErrDeviceRunNotFound
	}
	if run.Status != string(domain.AgentRunStatusRunning) {
		return nil
	}
	valid, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Where("agr.id = ? AND agr.lease_expires_at > clock_timestamp()", run.ID).
		Where("agr.claimed_at + make_interval(secs => ?) > clock_timestamp()", DeviceRunMaxDuration.Seconds()).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check device agent run lease: %w", err)
	}
	if !valid {
		return ErrDeviceRunLeaseLost
	}
	return nil
}

// deviceRunExpiry 返回设备运行失去执行资格的原因：超出总时限或租约已过期，两者都未发生时返回空串。
func deviceRunExpiry(run *servermodels.AgentRun) domain.AgentRunErrorCode {
	now := time.Now()
	if run.ClaimedAt != nil && !run.ClaimedAt.Add(DeviceRunMaxDuration).After(now) {
		return domain.AgentRunErrorCodeDeviceRunTimedOut
	}
	if run.LeaseExpiresAt == nil || !run.LeaseExpiresAt.After(now) {
		return domain.AgentRunErrorCodeDeviceLeaseExpired
	}
	return ""
}
