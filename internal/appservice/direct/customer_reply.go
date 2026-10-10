//go:build server

package direct

import (
	"context"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
)

// GenerateServiceReplySuggestions 使用 AI 员工为服务会话生成回复候选。
func (o *agentOps) GenerateServiceReplySuggestions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ServiceReplySuggestionsInput) (appservice.ServiceReplySuggestions, error) {
	candidates, err := o.serviceReplySuggestions.Execute(ctx, identity, agentrunaction.ServiceReplySuggestionsInput{
		ConversationID: conversationID, AgentIdentityID: input.AgentIdentityID,
		Mode: input.Mode, Tone: input.Tone,
		Draft: input.Draft, ReplyToMessageID: input.ReplyToMessageID, Language: input.Language,
	})
	if err == nil {
		return appservice.ServiceReplySuggestions{Candidates: candidates}, nil
	}
	return appservice.ServiceReplySuggestions{}, serviceReplySuggestionsErrors.Translate(meta, err, i18n.ErrorCustomerReplySuggestFailed)
}

// ListServiceReplyAgents 返回可用于 AI 写回复的 AI 员工。
func (o *agentOps) ListServiceReplyAgents(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ServiceReplyAgentList, error) {
	agents, err := o.listServiceReplyAgents.Execute(ctx, identity)
	if err != nil {
		return appservice.ServiceReplyAgentList{}, appservice.FailedError(meta, i18n.ErrorAgentListFailed, err)
	}
	output := arr.Map(agents, func(agent agentrunaction.ServiceReplyAgent) appservice.ServiceReplyAgent {
		return appservice.ServiceReplyAgent{IdentityID: agent.IdentityID, DisplayName: agent.DisplayName}
	})
	return appservice.ServiceReplyAgentList{Agents: output}, nil
}

// serviceReplySuggestionsValidationKeys 映射 AI 写回复输入的校验错误码。
var serviceReplySuggestionsValidationKeys = map[common.FieldCode]i18n.Key{
	agentrunaction.ValidationCustomerReplyLanguageInvalid: i18n.FieldLocaleInvalid,
	conversationaction.ValidationBodyRequired:             i18n.FieldCustomerReplyDraftRequired,
	conversationaction.ValidationBodyTooLong:              i18n.FieldMessageBodyTooLong,
}

// serviceReplySuggestionsErrors 是 AI 写回复的错误转换规则。
var serviceReplySuggestionsErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(conversationaction.ErrConversationNotFound, dispatch.NotFound(i18n.ErrorConversationNotFound)),
	dispatch.Is(agentrunaction.ErrAgentUnavailable, dispatch.NotFound(i18n.ErrorAgentUnavailable)),
	dispatch.Is(agentrunaction.ErrCustomerReplyGenerationFailed, dispatch.Unavailable(i18n.ErrorCustomerReplySuggestFailed)),
	dispatch.FieldRule(serviceReplySuggestionsValidationKeys),
	conversationConflictRule(i18n.ErrorMessageConflict, customerReplyConflictKeys),
})
