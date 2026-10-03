//go:build server

// Package modelcall 是业务调用 AI 模型的统一入口：按模型的来源路由依次请求上游，并记录每次调用与上游尝试。
package modelcall

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/model"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/embedding"
	"github.com/runforyou-ai/luway/pkg/rerank"
	"github.com/uptrace/bun"
)

// maxErrorRunes 是调用记录中失败原因保留的字符上限。
const maxErrorRunes = 1000

// Scope 定义一次模型调用的归属：工作区、发起主体与所服务的业务对象。
type Scope struct {
	OrganizationID string
	Actor          domain.AIModelCallActor
	ActorID        string // 成员与 AI 员工为工作区身份编号，后台任务为空。
	Source         domain.AIModelCallSource
	SourceID       string // 为空表示业务对象没有编号。
}

// MemberScope 返回成员为业务对象发起的模型调用归属。
func MemberScope(identity *servermodels.Identity, source domain.AIModelCallSource, sourceID string) Scope {
	return Scope{
		OrganizationID: identity.Organization.ID, Actor: domain.AIModelCallActorMember, ActorID: identity.OrganizationIdentity.ID,
		Source: source, SourceID: sourceID,
	}
}

// AgentScope 返回 AI 员工为业务对象发起的模型调用归属，agentIdentityID 是 AI 员工的工作区身份编号。
func AgentScope(organizationID, agentIdentityID string, source domain.AIModelCallSource, sourceID string) Scope {
	return Scope{OrganizationID: organizationID, Actor: domain.AIModelCallActorAgent, ActorID: agentIdentityID, Source: source, SourceID: sourceID}
}

// SystemScope 返回后台任务为业务对象发起的模型调用归属。
func SystemScope(organizationID string, source domain.AIModelCallSource, sourceID string) Scope {
	return Scope{OrganizationID: organizationID, Actor: domain.AIModelCallActorSystem, Source: source, SourceID: sourceID}
}

// Upstreams 定义统一入口请求上游时使用的协议客户端。
type Upstreams struct {
	Chat     func(context.Context, modelprovider.ChatConfig) (model.AgenticModel, error)
	Embedder interface {
		Embed(context.Context, embedding.Credential, string, int, []string) (embedding.Result, error)
	}
	Reranker interface {
		Rerank(context.Context, rerank.Credential, string, string, []string, int) (rerank.Result, error)
	}
	Decider interface {
		Decide(context.Context, decision.Credential, string, any, map[string]decision.Question) (map[string]decision.Answer, error)
	}
}

// DefaultUpstreams 返回生产环境使用的上游协议客户端。
func DefaultUpstreams() Upstreams {
	return Upstreams{
		Chat:     modelprovider.NewChatModel,
		Embedder: embedding.NewClient(),
		Reranker: rerank.NewClient(),
		Decider:  decision.NewClient(),
	}
}

// Invoker 执行模型调用并记录调用与上游尝试。
type Invoker struct {
	db        bun.IDB
	upstreams Upstreams
}

// New 创建统一调用入口。
func New(db bun.IDB, upstreams Upstreams) *Invoker {
	return &Invoker{db: db, upstreams: upstreams}
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

// call 是一次进行中的模型调用记录。
type call struct {
	invoker *Invoker
	id      string
	usage   Usage
}

// begin 写入进行中的调用记录；Agent 运行时已为本次模型调用分配编号时沿用该编号。
func (i *Invoker) begin(ctx context.Context, scope Scope, target *aimodel.Model) (*call, error) {
	record := &servermodels.AIModelCall{
		ID: agentruntime.ModelCallID(ctx), OrganizationID: scope.OrganizationID, ModelID: target.ID, ModelName: target.Name, ModelUsage: string(target.Usage), ModelScope: string(target.Scope),
		ActorType: string(scope.Actor), ActorID: optional(scope.ActorID),
		SourceType: string(scope.Source), SourceID: optional(scope.SourceID),
		Status: string(domain.AIModelCallStatusRunning),
	}
	columns := []string{"organization_id", "model_id", "model_name", "model_usage", "model_scope", "actor_type", "actor_id", "source_type", "source_id", "status"}
	if record.ID != "" {
		columns = append(columns, "id")
	}
	if _, err := i.db.NewInsert().Model(record).
		Column(columns...).
		Returning("id").
		Exec(context.WithoutCancel(ctx)); err != nil {
		return nil, fmt.Errorf("record AI model call: %w", err)
	}
	return &call{invoker: i, id: record.ID}, nil
}

// beginAttempt 写入进行中的上游尝试。
func (c *call) beginAttempt(ctx context.Context, route aimodel.Route) (string, error) {
	record := &servermodels.AIModelCallAttempt{
		CallID: c.id, RouteID: route.ID, ProviderID: route.ProviderID, ProviderName: route.ProviderName,
		Identifier: route.Identifier, Status: string(domain.AIModelCallStatusRunning),
	}
	if _, err := c.invoker.db.NewInsert().Model(record).
		Column("call_id", "route_id", "provider_id", "provider_name", "identifier", "status").
		Returning("id").
		Exec(context.WithoutCancel(ctx)); err != nil {
		return "", fmt.Errorf("record AI model call attempt: %w", err)
	}
	return record.ID, nil
}

// finishAttempt 写入上游尝试的结果并累计到调用用量，写入失败只记录日志。
func (c *call) finishAttempt(ctx context.Context, attemptID string, usage Usage, err error) {
	c.usage.add(usage)
	status, message := outcome(ctx, err)
	if _, updateErr := c.invoker.db.NewUpdate().Model((*servermodels.AIModelCallAttempt)(nil)).
		Set("status = ?", status).
		Set("input_tokens = ?", usage.InputTokens).
		Set("cached_input_tokens = ?", usage.CachedInputTokens).
		Set("output_tokens = ?", usage.OutputTokens).
		Set("error_message = ?", message).
		Set("finished_at = now()").
		Where("id = ?", attemptID).
		Exec(context.WithoutCancel(ctx)); updateErr != nil {
		slog.Warn("记录模型上游尝试结果失败", "ai_model_call_id", c.id, "attempt_id", attemptID, "error", updateErr)
	}
}

// finish 写入调用的结果与累计用量，写入失败只记录日志。
func (c *call) finish(ctx context.Context, err error) {
	status, message := outcome(ctx, err)
	if _, updateErr := c.invoker.db.NewUpdate().Model((*servermodels.AIModelCall)(nil)).
		Set("status = ?", status).
		Set("input_tokens = ?", c.usage.InputTokens).
		Set("cached_input_tokens = ?", c.usage.CachedInputTokens).
		Set("output_tokens = ?", c.usage.OutputTokens).
		Set("error_message = ?", message).
		Set("finished_at = now()").
		Where("id = ?", c.id).
		Exec(context.WithoutCancel(ctx)); updateErr != nil {
		slog.Warn("记录模型调用结果失败", "ai_model_call_id", c.id, "error", updateErr)
	}
}

// run 记录一次调用，按来源顺序执行 attempt：成功即结束，失败且调用未被取消或超时时尝试下一来源，全部失败时返回最后一次错误。
func (i *Invoker) run(ctx context.Context, scope Scope, target *aimodel.Model, attempt func(context.Context, aimodel.Route) (Usage, error)) error {
	record, err := i.begin(ctx, scope, target)
	if err != nil {
		return err
	}
	err = errors.New("AI model has no route")
	for _, route := range target.Routes {
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
		slog.Warn("模型来源请求失败", "ai_model_call_id", record.id, "route_id", route.ID, "provider_id", route.ProviderID, "error", err)
	}
	record.finish(ctx, err)
	return err
}

// outcome 按错误与调用 context 给出状态与失败原因：调用超时为超时，调用取消为已取消，其余错误为失败。
func outcome(ctx context.Context, err error) (domain.AIModelCallStatus, string) {
	switch {
	case err == nil:
		return domain.AIModelCallStatusSucceeded, ""
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		return domain.AIModelCallStatusTimedOut, truncate(err.Error())
	case ctx.Err() != nil || errors.Is(err, context.Canceled):
		return domain.AIModelCallStatusCanceled, truncate(err.Error())
	default:
		return domain.AIModelCallStatusFailed, truncate(err.Error())
	}
}

// truncate 按字符上限截断失败原因并替换非法 UTF-8 字节。
func truncate(message string) string {
	if utf8.RuneCountInString(message) > maxErrorRunes {
		message = string([]rune(message)[:maxErrorRunes])
	}
	return string([]rune(message))
}

// optional 把空字符串转换为空值。
func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
