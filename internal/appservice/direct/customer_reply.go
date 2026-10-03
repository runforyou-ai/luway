//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	creditaction "github.com/runforyou-ai/luway/internal/actions/credit"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// GenerateServiceReplySuggestions 使用 AI 员工为服务会话生成回复候选。
func (o *directOperations) GenerateServiceReplySuggestions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ServiceReplySuggestionsInput) (appservice.ServiceReplySuggestions, error) {
	candidates, err := o.serviceReplySuggestions.Execute(ctx, identity, agentrunaction.ServiceReplySuggestionsInput{
		ConversationID: conversationID, AgentIdentityID: input.AgentIdentityID,
		Mode: domain.ServiceReplyMode(input.Mode), Tone: domain.ServiceReplyTone(input.Tone),
		Draft: input.Draft, ReplyToMessageID: input.ReplyToMessageID, Language: input.Language,
	})
	if err == nil {
		return appservice.ServiceReplySuggestions{Candidates: candidates}, nil
	}
	if ctx.Err() != nil {
		return appservice.ServiceReplySuggestions{}, ctx.Err()
	}
	switch {
	case errors.Is(err, creditaction.ErrInsufficient):
		return appservice.ServiceReplySuggestions{}, appservice.ConflictError(meta, i18n.ErrorCreditsInsufficient, "credits_insufficient")
	case errors.Is(err, conversationaction.ErrConversationNotFound):
		return appservice.ServiceReplySuggestions{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	case errors.Is(err, agentrunaction.ErrAgentUnavailable):
		return appservice.ServiceReplySuggestions{}, appservice.NotFoundError(meta, i18n.ErrorAgentUnavailable)
	case errors.Is(err, agentrunaction.ErrCustomerReplyGenerationFailed):
		return appservice.ServiceReplySuggestions{}, appservice.FailedError(meta, i18n.ErrorCustomerReplySuggestFailed)
	}
	if validationError, ok := errors.AsType[*conversationaction.ValidationError](err); ok {
		return appservice.ServiceReplySuggestions{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, serviceReplySuggestionsValidationKeys))
	}
	if conflictError, ok := errors.AsType[*conversationaction.ConflictError](err); ok {
		return appservice.ServiceReplySuggestions{}, appservice.ConflictError(meta, customerReplyConflictMessageKey(conflictError.Reason), conflictError.Reason)
	}
	slog.Warn("读取客户回复候选资料失败", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "error", err)
	return appservice.ServiceReplySuggestions{}, appservice.FailedError(meta, i18n.ErrorCustomerReplySuggestFailed)
}

// ListServiceReplyAgents 返回可用于 AI 写回复的 AI 员工。
func (o *directOperations) ListServiceReplyAgents(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ServiceReplyAgentList, error) {
	agents, err := o.listServiceReplyAgents.Execute(ctx, identity)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.ServiceReplyAgentList{}, ctx.Err()
		}
		slog.Warn("读取 AI 写回复可用员工失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.ServiceReplyAgentList{}, appservice.FailedError(meta, i18n.ErrorAgentListFailed)
	}
	output := make([]appservice.ServiceReplyAgent, 0, len(agents))
	for _, agent := range agents {
		output = append(output, appservice.ServiceReplyAgent{IdentityID: agent.IdentityID, DisplayName: agent.DisplayName})
	}
	return appservice.ServiceReplyAgentList{Agents: output}, nil
}

var serviceReplySuggestionsValidationKeys = map[common.FieldCode]i18n.Key{
	conversationaction.ValidationConversationIDInvalid:    i18n.FieldConversationIDInvalid,
	agentrunaction.ValidationAgentIdentityIDInvalid:       i18n.FieldAgentIdentityIDInvalid,
	agentrunaction.ValidationServiceReplyModeInvalid:      i18n.FieldServiceReplyModeInvalid,
	agentrunaction.ValidationServiceReplyToneInvalid:      i18n.FieldServiceReplyToneInvalid,
	agentrunaction.ValidationCustomerReplyLanguageInvalid: i18n.FieldLocaleInvalid,
	conversationaction.ValidationReplyToMessageIDInvalid:  i18n.FieldReplyToMessageIDInvalid,
	conversationaction.ValidationBodyRequired:             i18n.FieldCustomerReplyDraftRequired,
	conversationaction.ValidationBodyTooLong:              i18n.FieldMessageBodyTooLong,
}
