//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	agentevaluationaction "github.com/runforyou-ai/luway/internal/actions/agentevaluation"
	knowledgebaseaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	knowledgegapaction "github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// knowledgeGapOps 持有待补知识的查询与操作。
type knowledgeGapOps struct {
	listKnowledgeGaps   *knowledgegapaction.ListQuery
	getKnowledgeGap     *knowledgegapaction.GetQuery
	acceptKnowledgeGap  *knowledgegapaction.AcceptAction
	dismissKnowledgeGap *knowledgegapaction.DismissAction
}

// newKnowledgeGapOps 创建待补知识的业务实现依赖。
func newKnowledgeGapOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer) *knowledgeGapOps {
	return &knowledgeGapOps{
		listKnowledgeGaps:   knowledgegapaction.NewListQuery(db),
		getKnowledgeGap:     knowledgegapaction.NewGetQuery(db),
		acceptKnowledgeGap:  knowledgegapaction.NewAcceptAction(db, knowledgebaseaction.NewSaveQAEntryAction(db, taskEnqueuer), agentevaluationaction.KnowledgeGapCases{}),
		dismissKnowledgeGap: knowledgegapaction.NewDismissAction(db),
	}
}

// ListKnowledgeGaps 返回一页指定处理状态的待补知识。
func (o *knowledgeGapOps) ListKnowledgeGaps(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.KnowledgeGapListInput) (appservice.KnowledgeGapList, error) {
	agents := reportAgentScope(identity, input.AgentID, input.Mine)
	list, err := o.listKnowledgeGaps.Execute(ctx, identity, knowledgegapaction.ListInput{
		Scope:  knowledgegapaction.Scope{ChannelID: input.ChannelID, Agents: agents},
		Status: input.Status, Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.KnowledgeGapList{}, knowledgeGapError(meta, err, i18n.ErrorKnowledgeGapLoadFailed)
	}
	gaps := arr.Map(list.Gaps, func(gap knowledgegapaction.Summary) appservice.KnowledgeGapSummary {
		return appservice.KnowledgeGapSummary{
			ID: gap.ID, ConversationID: gap.ConversationID, QuestionMessageID: support.Deref(gap.QuestionMessageID), Question: gap.Question,
			Source: appservice.KnowledgeGapSource(gap.Source), Status: appservice.KnowledgeGapStatus(gap.Status), CategoryName: support.Deref(gap.CategoryName),
			HasDraft: gap.HasDraft, OccurredAt: gap.OccurredAt, Handleable: gap.Handleable,
		}
	})
	return appservice.KnowledgeGapList{Gaps: gaps, Page: appservice.PageInfo{Number: list.Page, Size: list.PageSize, Total: list.Total}}, nil
}

// GetKnowledgeGap 返回待补知识详情。
func (o *knowledgeGapOps) GetKnowledgeGap(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, gapID string) (appservice.KnowledgeGap, error) {
	detail, err := o.getKnowledgeGap.Execute(ctx, identity, gapID)
	if err != nil {
		return appservice.KnowledgeGap{}, knowledgeGapError(meta, err, i18n.ErrorKnowledgeGapLoadFailed)
	}
	output := appservice.KnowledgeGap{
		ID: detail.ID, ConversationID: detail.ConversationID, QuestionMessageID: support.Deref(detail.QuestionMessageID),
		Question: detail.Question, Source: appservice.KnowledgeGapSource(detail.Source), Status: appservice.KnowledgeGapStatus(detail.Status),
		CategoryName: support.Deref(detail.CategoryName), OccurredAt: detail.OccurredAt, DraftStatus: appservice.KnowledgeGapDraftStatus(detail.DraftStatus),
		DefaultKnowledgeBaseID: support.Deref(detail.DefaultKnowledgeBaseID),
		KnowledgeBaseID:        support.Deref(detail.KnowledgeBaseID), QAEntryID: support.Deref(detail.QAEntryID), Evaluable: detail.Evaluable,
		Handleable: detail.Handleable,
		Messages:   arr.Map(detail.Messages, serviceTranscriptMessageFromAction),
	}
	if domain.KnowledgeGapDraftStatus(detail.DraftStatus) == domain.KnowledgeGapDraftStatusReady && detail.DraftQuestion != nil {
		output.Draft = &appservice.KnowledgeGapDraft{Question: *detail.DraftQuestion, SimilarQuestions: detail.DraftSimilarQuestions, Answer: support.Deref(detail.DraftAnswer)}
	}
	return output, nil
}

// serviceTranscriptMessageFromAction 转换客服周期对客沟通中的一条消息。
func serviceTranscriptMessageFromAction(message knowledgegapaction.Message) appservice.ServiceTranscriptMessage {
	return appservice.ServiceTranscriptMessage{
		ID: message.ID, Type: appservice.MessageType(message.Type), Sender: appservice.ServiceTranscriptSender(message.Sender), SenderName: message.SenderName, Body: message.Body, CreatedAt: message.CreatedAt,
	}
}

// AcceptKnowledgeGap 在同一事务中保存问答并把待补知识记为已加入知识库。
func (o *knowledgeGapOps) AcceptKnowledgeGap(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, gapID string, input appservice.KnowledgeGapAcceptInput) error {
	questions := arr.Map(input.Entry.SimilarQuestions, func(question appservice.KnowledgeQASimilarQuestion) knowledgebaseaction.QASimilarQuestion {
		return knowledgebaseaction.QASimilarQuestion{ID: question.ID, Content: question.Content}
	})
	err := o.acceptKnowledgeGap.Execute(ctx, identity, gapID, knowledgegapaction.AcceptInput{
		KnowledgeBaseID: input.KnowledgeBaseID, EntryID: input.EntryID,
		QA:              knowledgebaseaction.QAInput{Question: input.Entry.Question, SimilarQuestions: questions, Answer: input.Entry.Answer},
		AddToEvaluation: input.AddToEvaluation,
	})
	if errors.Is(err, knowledgegapaction.ErrNotFound) || errors.Is(err, knowledgegapaction.ErrHandled) || errors.Is(err, knowledgegapaction.ErrForbidden) {
		return knowledgeGapError(meta, err, i18n.ErrorKnowledgeGapAcceptFailed)
	}
	if errors.Is(err, agentevaluationaction.ErrAgentNotFound) || errors.Is(err, agentevaluationaction.ErrQuestionNotFound) || errors.Is(err, agentevaluationaction.ErrServiceSessionNotFound) ||
		errors.Is(err, agentevaluationaction.ErrQuestionAlreadyEvaluated) {
		return agentEvaluationError(meta, err, i18n.ErrorKnowledgeGapAcceptFailed)
	}
	if err != nil {
		return knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeGapAcceptFailed, identity.Workspace.ID, input.KnowledgeBaseID)
	}
	slog.InfoContext(ctx, "待补知识已加入知识库", "knowledge_gap_id", gapID,
		"knowledge_base_id", input.KnowledgeBaseID, "updated_entry", input.EntryID != "", "added_to_evaluation", input.AddToEvaluation)
	return nil
}

// DismissKnowledgeGap 忽略待补知识。
func (o *knowledgeGapOps) DismissKnowledgeGap(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, gapID string) error {
	if err := o.dismissKnowledgeGap.Execute(ctx, identity, gapID); err != nil {
		return knowledgeGapError(meta, err, i18n.ErrorKnowledgeGapDismissFailed)
	}
	return nil
}

// knowledgeGapErrors 是待补知识的错误转换规则。
var knowledgeGapErrors = dispatch.Catalog{
	dispatch.Is(knowledgegapaction.ErrNotFound, dispatch.NotFound(i18n.ErrorKnowledgeGapNotFound)),
	dispatch.Is(knowledgegapaction.ErrForbidden, dispatch.Forbidden(i18n.ErrorPermissionDenied)),
	dispatch.Is(knowledgegapaction.ErrHandled, dispatch.Conflict(i18n.ErrorKnowledgeGapHandled, "knowledge_gap_handled")),
	dispatch.Is(knowledgegapaction.ErrPageSizeInvalid, dispatch.Invalid(i18n.ErrorValidationFailed)),
	dispatch.SessionRule,
}

// knowledgeGapError 把待补知识错误转换为结构化、本地化错误。
func knowledgeGapError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return knowledgeGapErrors.Translate(meta, err, failureKey)
}
