//go:build server

// Package modelcall 是业务调用 AI 模型的统一入口：按来源权重与熔断状态排出尝试顺序并依次请求上游，记录每次调用与上游尝试。
package modelcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/cloudwego/eino/components/model"
	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/einorun/provider"
	"github.com/runforyou-ai/einorun/provider/embedding"
	"github.com/runforyou-ai/einorun/provider/rerank"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// maxErrorRunes 是调用记录中失败原因保留的字符上限。
const maxErrorRunes = 1000

// Scope 定义一次模型调用的归属：工作区、发起主体与所服务的业务对象。
type Scope struct {
	WorkspaceID string
	Actor       domain.AIModelCallActor
	ActorID     string // 成员与 AI 员工为工作区身份编号，后台任务为空。
	Source      domain.AIModelCallSource
	SourceID    string // 为空表示业务对象没有编号。
}

// MemberScope 返回成员为业务对象发起的模型调用归属。
func MemberScope(identity *servermodels.Identity, source domain.AIModelCallSource, sourceID string) Scope {
	return Scope{
		WorkspaceID: identity.Workspace.ID, Actor: domain.AIModelCallActorMember, ActorID: identity.WorkspaceIdentity.ID,
		Source: source, SourceID: sourceID,
	}
}

// AgentScope 返回 AI 员工为业务对象发起的模型调用归属，agentIdentityID 是 AI 员工的工作区身份编号。
func AgentScope(workspaceID, agentIdentityID string, source domain.AIModelCallSource, sourceID string) Scope {
	return Scope{WorkspaceID: workspaceID, Actor: domain.AIModelCallActorAgent, ActorID: agentIdentityID, Source: source, SourceID: sourceID}
}

// SystemScope 返回后台任务为业务对象发起的模型调用归属。
func SystemScope(workspaceID string, source domain.AIModelCallSource, sourceID string) Scope {
	return Scope{WorkspaceID: workspaceID, Actor: domain.AIModelCallActorSystem, Source: source, SourceID: sourceID}
}

// Upstreams 定义统一入口请求上游时使用的协议客户端。
type Upstreams struct {
	Chat     func(context.Context, provider.ChatConfig) (model.AgenticModel, error)
	Embedder interface {
		Embed(context.Context, embedding.Endpoint, string, int, []string) (embedding.Result, error)
	}
	Reranker interface {
		Rerank(context.Context, rerank.Endpoint, string, string, []string, int) (rerank.Result, error)
	}
	Decider interface {
		Decide(context.Context, decision.Credential, string, any, map[string]decision.Question) (map[string]decision.Answer, error)
	}
}

// DefaultUpstreams 返回生产环境使用的上游协议客户端。
func DefaultUpstreams() Upstreams {
	return Upstreams{
		Chat:     provider.NewChatModel,
		Embedder: embedding.NewClient(),
		Reranker: rerank.NewClient(),
		Decider:  decision.NewClient(),
	}
}

// Invoker 执行模型调用并记录调用与上游尝试。
type Invoker struct {
	db        bun.IDB
	upstreams Upstreams
	lifecycle Lifecycle
}

// New 创建统一调用入口。
func New(db bun.IDB, upstreams Upstreams, lifecycle Lifecycle) *Invoker {
	return &Invoker{db: db, upstreams: upstreams, lifecycle: lifecycle}
}

// Usage 定义一次上游请求或一次调用的 Token 用量。
type Usage struct {
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
}

// add 累计另一份用量。
func (u *Usage) add(other Usage) {
	u.InputTokens += other.InputTokens
	u.CachedInputTokens += other.CachedInputTokens
	u.OutputTokens += other.OutputTokens
}

// call 是一次进行中的模型调用记录，routes 是本次调用的来源尝试顺序。
type call struct {
	invoker *Invoker
	id      string
	routes  []aimodel.Route
	usage   Usage
}

// begin 在同一事务内写入调用记录并执行开始回调，回调失败时整个事务回滚；成功后按来源权重与熔断状态排出本次调用的尝试顺序。
func (i *Invoker) begin(ctx context.Context, scope Scope, target *aimodel.Model, estimate Usage) (*call, error) {
	ctx = context.WithoutCancel(ctx)
	record := &servermodels.AIModelCall{
		ID: llm.ModelCallID(ctx), WorkspaceID: scope.WorkspaceID, ModelID: target.ID, ModelName: target.Name, ModelUsage: string(target.Usage), ModelScope: string(target.Scope),
		ActorType: string(scope.Actor), ActorID: support.NilIfZero(scope.ActorID),
		SourceType: string(scope.Source), SourceID: support.NilIfZero(scope.SourceID),
		Status: string(domain.AIModelCallStatusRunning),
	}
	columns := []string{"workspace_id", "model_id", "model_name", "model_usage", "model_scope", "actor_type", "actor_id", "source_type", "source_id", "status"}
	if record.ID != "" {
		columns = append(columns, "id")
	}
	err := serverstorage.RunInTx(ctx, i.db, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(record).Column(columns...).Returning("id").Exec(ctx); err != nil {
			return err
		}
		if i.lifecycle != nil {
			return i.lifecycle.Begin(ctx, tx, Facts{CallID: record.ID, WorkspaceID: scope.WorkspaceID, ModelID: target.ID, Scope: target.Scope, Usage: estimate})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("record AI model call: %w", err)
	}
	return &call{invoker: i, id: record.ID, routes: attemptOrder(target.Routes, trippedRoutes(ctx, i.db, target.Routes))}, nil
}

// beginAttempt 写入进行中的上游尝试。
func (c *call) beginAttempt(ctx context.Context, route aimodel.Route) (string, error) {
	record := &servermodels.AIModelCallAttempt{
		CallID: c.id, RouteID: route.ID, ProviderID: route.ProviderID, ProviderName: route.ProviderName,
		Identifier: route.Identifier, Status: string(domain.AIModelCallStatusRunning),
	}
	err := serverstorage.RunInTx(context.WithoutCancel(ctx), c.invoker.db, func(ctx context.Context, tx bun.Tx) error {
		parent, err := lockRunning(ctx, tx, c.id)
		if err != nil {
			return err
		}
		if parent == nil {
			return errors.New("AI model call already finished")
		}
		_, err = tx.NewInsert().Model(record).
			Column("call_id", "route_id", "provider_id", "provider_name", "identifier", "status").
			Returning("id").
			Exec(ctx)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("record AI model call attempt: %w", err)
	}
	return record.ID, nil
}

// finishAttempt 写入上游尝试的结果并累计到调用用量，写入失败只记录日志。
func (c *call) finishAttempt(ctx context.Context, attemptID string, usage Usage, err error) {
	c.usage.add(usage)
	status, message := attemptOutcome(ctx, err)
	updateErr := serverstorage.RunInTx(context.WithoutCancel(ctx), c.invoker.db, func(ctx context.Context, tx bun.Tx) error {
		parent, err := lockRunning(ctx, tx, c.id)
		if err != nil || parent == nil {
			return err
		}
		_, err = tx.NewUpdate().Model((*servermodels.AIModelCallAttempt)(nil)).
			Set("status = ?", status).
			Set("input_tokens = ?", usage.InputTokens).
			Set("cached_input_tokens = ?", usage.CachedInputTokens).
			Set("output_tokens = ?", usage.OutputTokens).
			Set("error_message = ?", message).
			Set("finished_at = now()").
			Where("id = ?", attemptID).
			Exec(ctx)
		return err
	})
	if updateErr != nil {
		slog.WarnContext(ctx, "记录模型上游尝试结果失败", "ai_model_call_id", c.id, "attempt_id", attemptID, "error", updateErr)
	}
}

// finish 在调用行锁与终态门禁内执行结束回调并记录结果，事务失败时由中断清理任务收尾。
func (c *call) finish(ctx context.Context, err error) {
	status, message := outcome(ctx, err)
	updateErr := serverstorage.RunInTx(context.WithoutCancel(ctx), c.invoker.db, func(ctx context.Context, tx bun.Tx) error {
		record, lockErr := lockRunning(ctx, tx, c.id)
		if lockErr != nil || record == nil {
			return lockErr
		}
		return c.invoker.finishLocked(ctx, tx, record, c.usage, status, message, err == nil)
	})
	if updateErr != nil {
		slog.WarnContext(ctx, "记录模型调用结果失败", "ai_model_call_id", c.id, "error", updateErr)
	}
}

// run 按预估用量开始一次调用，按本次调用的来源尝试顺序执行 attempt：成功即结束，失败且调用未被取消或超时时尝试下一来源，全部失败时返回最后一次错误。
func (i *Invoker) run(ctx context.Context, scope Scope, target *aimodel.Model, estimate Usage, attempt func(context.Context, aimodel.Route) (Usage, error)) error {
	record, err := i.begin(ctx, scope, target, estimate)
	if err != nil {
		return err
	}
	err = errors.New("AI model has no route")
	for _, route := range record.routes {
		attemptID, beginErr := record.beginAttempt(ctx, route)
		if beginErr != nil {
			err = beginErr
			break
		}
		var usage Usage
		usage, err = attempt(ctx, route)
		record.finishAttempt(ctx, attemptID, usage, err)
		if err == nil || ctx.Err() != nil {
			break
		}
		slog.WarnContext(ctx, "模型来源请求失败", "ai_model_call_id", record.id, "route_id", route.ID, "provider_id", route.ProviderID, "error", err)
	}
	record.finish(ctx, err)
	return err
}

// outcome 按错误与调用 context 给出状态与失败原因：调用超时为超时，调用取消为已取消，其余错误为失败。
func outcome(ctx context.Context, err error) (domain.AIModelCallStatus, string) {
	if err == nil {
		return domain.AIModelCallStatusSucceeded, ""
	}
	// 失败原因删除 NUL 字符、替换非法 UTF-8 字节并按字符上限截断。
	message := str.Substr(str.Remove(err.Error(), "\x00"), 0, maxErrorRunes)
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		return domain.AIModelCallStatusTimedOut, message
	case ctx.Err() != nil || errors.Is(err, context.Canceled):
		return domain.AIModelCallStatusCanceled, message
	default:
		return domain.AIModelCallStatusFailed, message
	}
}

// attemptOutcome 给出上游尝试的状态与失败原因：调用方 context 已结束时为已取消，上游返回超时为超时，其余同 outcome；熔断只把失败与上游超时计为来源故障。
func attemptOutcome(ctx context.Context, err error) (domain.AIModelCallStatus, string) {
	status, message := outcome(ctx, err)
	var netErr net.Error
	switch {
	case err == nil:
	case ctx.Err() != nil:
		status = domain.AIModelCallStatusCanceled
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		status = domain.AIModelCallStatusTimedOut
	}
	return status, message
}

// estimateTokens 按每 4 字节 1 个 Token 向上取整预估文本的 Token 数，limit 大于 0 时不超过 limit。
func estimateTokens(bytes int, limit int64) int64 {
	tokens := int64(bytes+3) / 4
	if limit > 0 {
		tokens = min(tokens, limit)
	}
	return tokens
}

// estimateValueTokens 按 JSON 编码长度预估输入的 Token 数，不超过模型上下文窗口；无法编码时取上下文窗口。
func estimateValueTokens(value any, contextWindow int64) int64 {
	encoded, err := json.Marshal(value)
	if err != nil {
		return contextWindow
	}
	return estimateTokens(len(encoded), contextWindow)
}
