//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	agentevaluationaction "github.com/runforyou-ai/cervi/internal/actions/agentevaluation"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	knowledgebaseaction "github.com/runforyou-ai/cervi/internal/actions/knowledgebase"
	knowledgegapaction "github.com/runforyou-ai/cervi/internal/actions/knowledgegap"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
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
func newKnowledgeGapOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer) knowledgeGapOps {
	return knowledgeGapOps{
		listKnowledgeGaps:   knowledgegapaction.NewListQuery(db),
		getKnowledgeGap:     knowledgegapaction.NewGetQuery(db),
		acceptKnowledgeGap:  knowledgegapaction.NewAcceptAction(db, knowledgebaseaction.NewSaveQAEntryAction(db, taskEnqueuer), agentevaluationaction.KnowledgeGapCases{}),
		dismissKnowledgeGap: knowledgegapaction.NewDismissAction(db),
	}
}

// ListKnowledgeGaps 返回一页指定处理状态的待补知识。
func (o *directOperations) ListKnowledgeGaps(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.KnowledgeGapListInput) (appservice.KnowledgeGapList, error) {
	agents, err := reportAgentScope(meta, identity, input.ChannelID, input.AgentID, input.Mine)
	if err != nil {
		return appservice.KnowledgeGapList{}, err
	}
	list, err := o.listKnowledgeGaps.Execute(ctx, identity, knowledgegapaction.ListInput{
		Scope:  knowledgegapaction.Scope{ChannelID: input.ChannelID, Agents: agents},
		Status: domain.KnowledgeGapStatus(input.Status), Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.KnowledgeGapList{}, knowledgeGapError(meta, err, i18n.ErrorKnowledgeGapLoadFailed, identity.Organization.ID)
	}
	gaps := make([]appservice.KnowledgeGapSummary, 0, len(list.Gaps))
	for _, gap := range list.Gaps {
		gaps = append(gaps, appservice.KnowledgeGapSummary{
			ID: gap.ID, ConversationID: gap.ConversationID, QuestionMessageID: common.StringValue(gap.QuestionMessageID), Question: gap.Question,
			Source: appservice.KnowledgeGapSource(gap.Source), Status: appservice.KnowledgeGapStatus(gap.Status), CategoryName: common.StringValue(gap.CategoryName),
			HasDraft: gap.HasDraft, OccurredAt: gap.OccurredAt,
		})
	}
	return appservice.KnowledgeGapList{Gaps: gaps, Page: appservice.PageInfo{Number: list.Page, Size: list.PageSize, Total: list.Total}}, nil
}

// GetKnowledgeGap 返回待补知识详情。
func (o *directOperations) GetKnowledgeGap(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, gapID string) (appservice.KnowledgeGap, error) {
	detail, err := o.getKnowledgeGap.Execute(ctx, identity, gapID)
	if err != nil {
		return appservice.KnowledgeGap{}, knowledgeGapError(meta, err, i18n.ErrorKnowledgeGapLoadFailed, identity.Organization.ID)
	}
	output := appservice.KnowledgeGap{
		ID: detail.ID, ConversationID: detail.ConversationID, QuestionMessageID: common.StringValue(detail.QuestionMessageID),
		Question: detail.Question, Source: appservice.KnowledgeGapSource(detail.Source), Status: appservice.KnowledgeGapStatus(detail.Status),
		CategoryName: common.StringValue(detail.CategoryName), OccurredAt: detail.OccurredAt, DraftStatus: appservice.KnowledgeGapDraftStatus(detail.DraftStatus),
		DefaultKnowledgeBaseID: common.StringValue(detail.DefaultKnowledgeBaseID),
		KnowledgeBaseID:        common.StringValue(detail.KnowledgeBaseID), QAEntryID: common.StringValue(detail.QAEntryID), Evaluable: detail.Evaluable,
		Messages: make([]appservice.ServiceTranscriptMessage, 0, len(detail.Messages)),
	}
	if domain.KnowledgeGapDraftStatus(detail.DraftStatus) == domain.KnowledgeGapDraftStatusReady && detail.DraftQuestion != nil {
		output.Draft = &appservice.KnowledgeGapDraft{Question: *detail.DraftQuestion, SimilarQuestions: detail.DraftSimilarQuestions, Answer: common.StringValue(detail.DraftAnswer)}
	}
	for _, message := range detail.Messages {
		output.Messages = append(output.Messages, appservice.ServiceTranscriptMessage{
			ID: message.ID, Type: appservice.MessageType(message.Type), Sender: appservice.ServiceTranscriptSender(message.Sender), SenderName: message.SenderName, Body: message.Body, CreatedAt: message.CreatedAt,
		})
	}
	return output, nil
}

// AcceptKnowledgeGap 在同一事务中保存问答并把待补知识记为已加入知识库。
func (o *directOperations) AcceptKnowledgeGap(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, gapID string, input appservice.KnowledgeGapAcceptInput) error {
	if input.EntryID != "" && !common.ValidUUID(input.EntryID) {
		return appservice.NotFoundError(meta, i18n.ErrorKnowledgeQANotFound)
	}
	questions := make([]knowledgebaseaction.QASimilarQuestion, 0, len(input.Entry.SimilarQuestions))
	for _, question := range input.Entry.SimilarQuestions {
		questions = append(questions, knowledgebaseaction.QASimilarQuestion{ID: question.ID, Content: question.Content})
	}
	err := o.acceptKnowledgeGap.Execute(ctx, identity, gapID, knowledgegapaction.AcceptInput{
		KnowledgeBaseID: input.KnowledgeBaseID, EntryID: input.EntryID,
		QA:              knowledgebaseaction.QAInput{Question: input.Entry.Question, SimilarQuestions: questions, Answer: input.Entry.Answer},
		AddToEvaluation: input.AddToEvaluation,
	})
	if errors.Is(err, knowledgegapaction.ErrNotFound) || errors.Is(err, knowledgegapaction.ErrHandled) {
		return knowledgeGapError(meta, err, i18n.ErrorKnowledgeGapAcceptFailed, identity.Organization.ID)
	}
	if errors.Is(err, agentevaluationaction.ErrAgentNotFound) || errors.Is(err, agentevaluationaction.ErrQuestionNotFound) || errors.Is(err, agentevaluationaction.ErrServiceSessionNotFound) ||
		errors.Is(err, agentevaluationaction.ErrQuestionAlreadyEvaluated) {
		return agentEvaluationError(ctx, meta, err, i18n.ErrorKnowledgeGapAcceptFailed, identity.Organization.ID, "")
	}
	if err != nil {
		return o.knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeGapAcceptFailed, identity.Organization.ID, input.KnowledgeBaseID)
	}
	slog.Info("待补知识已加入知识库", "organization_id", identity.Organization.ID, "knowledge_gap_id", gapID,
		"knowledge_base_id", input.KnowledgeBaseID, "updated_entry", input.EntryID != "", "added_to_evaluation", input.AddToEvaluation)
	return nil
}

// DismissKnowledgeGap 忽略待补知识。
func (o *directOperations) DismissKnowledgeGap(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, gapID string) error {
	if err := o.dismissKnowledgeGap.Execute(ctx, identity, gapID); err != nil {
		return knowledgeGapError(meta, err, i18n.ErrorKnowledgeGapDismissFailed, identity.Organization.ID)
	}
	return nil
}

// knowledgeGapError 把待补知识错误转换为结构化、本地化错误。
func knowledgeGapError(meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID string) error {
	switch {
	case errors.Is(err, knowledgegapaction.ErrNotFound):
		return appservice.NotFoundError(meta, i18n.ErrorKnowledgeGapNotFound)
	case errors.Is(err, knowledgegapaction.ErrHandled):
		return appservice.ConflictError(meta, i18n.ErrorKnowledgeGapHandled, "knowledge_gap_handled")
	case errors.Is(err, knowledgegapaction.ErrPageSizeInvalid), errors.Is(err, knowledgegapaction.ErrStatusInvalid):
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	case errors.Is(err, identityaction.ErrInvalid):
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	slog.Warn("处理待补知识失败", "organization_id", organizationID, "error", err)
	return appservice.FailedError(meta, failureKey)
}
