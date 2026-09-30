//go:build server

package direct

import (
	"context"
	"strconv"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/appservice"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// GetConversationMessageContext 返回已授权消息周围的连续窗口。
func (o *directOperations) GetConversationMessageContext(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID, messageID string) (appservice.ConversationMessageList, error) {
	history, err := o.listConversationMessages.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, AroundMessageID: messageID})
	if err != nil {
		return appservice.ConversationMessageList{}, conversationMessageError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	return o.conversationMessageListFromAction(ctx, meta, identity, conversationID, history)
}

// GetConversationNavigationState 返回当前群聊的待查看数量和可见尾端。
func (o *directOperations) GetConversationNavigationState(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ConversationNavigationState, error) {
	state, err := o.conversationNavigation.Execute(ctx, identity, conversationID)
	if err != nil {
		return appservice.ConversationNavigationState{}, conversationMessageError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	return appservice.ConversationNavigationState{PendingMentionCount: state.PendingMentionCount, ReviewedThroughMessageID: state.ReviewedThroughMessageID, ReviewedThroughSequence: strconv.FormatInt(state.ReviewedThroughSequence, 10), LatestMessageID: state.LatestMessageID, LatestSequence: strconv.FormatInt(state.LatestSequence, 10)}, nil
}

// ListPendingConversationMentions 返回本轮固定的目标序列。
func (o *directOperations) ListPendingConversationMentions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.PendingConversationMentions, error) {
	pending, err := o.pendingConversationMentions.Execute(ctx, identity, conversationID)
	if err != nil {
		return appservice.PendingConversationMentions{}, conversationMessageError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	return appservice.PendingConversationMentions{MessageIDs: pending.MessageIDs, LastTargetSequence: messageSeqString(pending.LastTargetSequence)}, nil
}

// MarkConversationMentionReviewed 确认已查看的提及并返回连续水位。
func (o *directOperations) MarkConversationMentionReviewed(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.MarkConversationMentionReviewedInput) (appservice.ConversationMentionReview, error) {
	result, err := o.reviewConversationMention.Execute(ctx, identity, conversationID, input.MessageID)
	if err != nil {
		return appservice.ConversationMentionReview{}, conversationMessageError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	return appservice.ConversationMentionReview{ReviewedThroughMessageID: result.ReviewedThroughMessageID, ReviewedThroughSequence: strconv.FormatInt(result.ReviewedThroughSequence, 10), Outcome: appservice.ConversationMentionReviewOutcome(result.Outcome)}, nil
}

// messageSeqString 无损转换可空消息序号。
func messageSeqString(sequence *int64) *string {
	if sequence == nil {
		return nil
	}
	value := strconv.FormatInt(*sequence, 10)
	return &value
}
