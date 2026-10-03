//go:build server

package direct

import (
	"context"
	"log/slog"

	knowledgebaseaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// ListKnowledgeQAEntries 返回当前企业知识库中的问答列表。
func (o *directOperations) ListKnowledgeQAEntries(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, knowledgeBaseID string, input appservice.KnowledgeQAListInput) (appservice.KnowledgeQAList, error) {
	output, err := o.listQAEntries.Execute(ctx, identity, knowledgeBaseID, knowledgebaseaction.QAListInput{Keyword: input.Keyword, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.KnowledgeQAList{}, o.knowledgeBaseError(meta, err, i18n.ErrorKnowledgeQAReadFailed, identity.Organization.ID, knowledgeBaseID)
	}
	entries := make([]appservice.KnowledgeQASummary, 0, len(output.Entries))
	for _, entry := range output.Entries {
		status, message := knowledgeIndexPresentation(meta, entry.Status, entry.FailureCode)
		entries = append(entries, appservice.KnowledgeQASummary{ID: entry.ID, Question: entry.Question,
			SimilarQuestions: entry.SimilarQuestions, Answer: entry.Answer, Status: status, FailureMessage: message, UpdatedAt: entry.UpdatedAt})
	}
	return appservice.KnowledgeQAList{Entries: entries, Page: appservice.PageInfo{Number: output.Page, Size: output.PageSize, Total: output.Total}}, nil
}

// GetKnowledgeQAEntry 返回当前企业的完整问答。
func (o *directOperations) GetKnowledgeQAEntry(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, knowledgeBaseID, entryID string) (appservice.KnowledgeQAEntry, error) {
	record, err := o.getQAEntry.Execute(ctx, identity, knowledgeBaseID, entryID)
	if err != nil {
		return appservice.KnowledgeQAEntry{}, o.knowledgeBaseError(meta, err, i18n.ErrorKnowledgeQAReadFailed, identity.Organization.ID, knowledgeBaseID)
	}
	return knowledgeQAFromAction(*record), nil
}

// CreateKnowledgeQAEntry 创建本地问答。
func (o *directOperations) CreateKnowledgeQAEntry(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, knowledgeBaseID string, input appservice.KnowledgeQAInput) (appservice.KnowledgeQAEntry, error) {
	return o.saveKnowledgeQAEntry(ctx, meta, identity, knowledgeBaseID, "", input)
}

// UpdateKnowledgeQAEntry 更新本地问答。
func (o *directOperations) UpdateKnowledgeQAEntry(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, knowledgeBaseID, entryID string, input appservice.KnowledgeQAInput) (appservice.KnowledgeQAEntry, error) {
	if !common.ValidUUID(entryID) {
		return appservice.KnowledgeQAEntry{}, appservice.NotFoundError(meta, i18n.ErrorKnowledgeQANotFound)
	}
	return o.saveKnowledgeQAEntry(ctx, meta, identity, knowledgeBaseID, entryID, input)
}

// saveKnowledgeQAEntry 保存问答内容。
func (o *directOperations) saveKnowledgeQAEntry(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, knowledgeBaseID, entryID string, input appservice.KnowledgeQAInput) (appservice.KnowledgeQAEntry, error) {
	questions := make([]knowledgebaseaction.QASimilarQuestion, 0, len(input.SimilarQuestions))
	for _, question := range input.SimilarQuestions {
		questions = append(questions, knowledgebaseaction.QASimilarQuestion{ID: question.ID, Content: question.Content})
	}
	record, err := o.saveQAEntry.Execute(ctx, identity, knowledgeBaseID, entryID, knowledgebaseaction.QAInput{
		Question: input.Question, Answer: input.Answer, SimilarQuestions: questions,
	})
	if err != nil {
		return appservice.KnowledgeQAEntry{}, o.knowledgeBaseError(meta, err, i18n.ErrorKnowledgeQASaveFailed, identity.Organization.ID, knowledgeBaseID)
	}
	slog.Info("知识问答保存成功", "organization_id", identity.Organization.ID, "knowledge_base_id", knowledgeBaseID, "entry_id", record.ID, "created", entryID == "", "similar_question_count", len(record.SimilarQuestions))
	return knowledgeQAFromAction(*record), nil
}

// DeleteKnowledgeQAEntry 删除当前企业的问答及其内容。
func (o *directOperations) DeleteKnowledgeQAEntry(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, knowledgeBaseID, entryID string) error {
	if err := o.deleteQAEntry.Execute(ctx, identity, knowledgeBaseID, entryID); err != nil {
		return o.knowledgeBaseError(meta, err, i18n.ErrorKnowledgeQADeleteFailed, identity.Organization.ID, knowledgeBaseID)
	}
	slog.Info("知识问答删除成功", "organization_id", identity.Organization.ID, "knowledge_base_id", knowledgeBaseID, "entry_id", entryID)
	return nil
}

// RetryKnowledgeQAEntry 按当前配置为问答安排新的索引任务。
func (o *directOperations) RetryKnowledgeQAEntry(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, knowledgeBaseID, entryID string) error {
	if err := o.qaProcessing.Retry(ctx, identity, knowledgeBaseID, entryID); err != nil {
		return o.knowledgeBaseError(meta, err, i18n.ErrorKnowledgeQARetryFailed, identity.Organization.ID, knowledgeBaseID)
	}
	slog.Info("知识问答已提交重试", "knowledge_base_id", knowledgeBaseID, "entry_id", entryID)
	return nil
}

// knowledgeQAFromAction 转换问答详情并保持相似问题顺序。
func knowledgeQAFromAction(record knowledgebaseaction.QARecord) appservice.KnowledgeQAEntry {
	questions := make([]appservice.KnowledgeQASimilarQuestion, 0, len(record.SimilarQuestions))
	for _, question := range record.SimilarQuestions {
		questions = append(questions, appservice.KnowledgeQASimilarQuestion{ID: question.ID, Content: question.Content})
	}
	return appservice.KnowledgeQAEntry{ID: record.ID, Question: record.Question, Answer: record.Answer,
		SimilarQuestions: questions, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}
