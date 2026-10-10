//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	knowledgebaseaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	modelcallaction "github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// knowledgeOps 持有知识库的 Action 和 Query。
type knowledgeOps struct {
	documentQuery       *knowledgebaseaction.DocumentQuery
	createDocuments     *knowledgebaseaction.CreateDocumentsAction
	saveTextDocument    *knowledgebaseaction.SaveTextDocumentAction
	createWebDocument   *knowledgebaseaction.CreateWebDocumentAction
	renameDocument      *knowledgebaseaction.RenameDocumentAction
	documentProcessing  *knowledgebaseaction.DocumentProcessing
	deleteDocument      *knowledgebaseaction.DeleteDocumentAction
	listQAEntries       *knowledgebaseaction.ListQAEntriesQuery
	getQAEntry          *knowledgebaseaction.GetQAEntryQuery
	saveQAEntry         *knowledgebaseaction.SaveQAEntryAction
	qaProcessing        *knowledgebaseaction.QAProcessing
	deleteQAEntry       *knowledgebaseaction.DeleteQAEntryAction
	listKnowledgeBases  *knowledgebaseaction.ListKnowledgeBasesQuery
	getKnowledgeBase    *knowledgebaseaction.GetKnowledgeBaseQuery
	listBaseAgents      *knowledgebaseaction.ListKnowledgeBaseAgentsQuery
	createKnowledgeBase *knowledgebaseaction.CreateKnowledgeBaseAction
	updateKnowledgeBase *knowledgebaseaction.UpdateKnowledgeBaseAction
	deleteKnowledgeBase *knowledgebaseaction.DeleteKnowledgeBaseAction
	retrieval           *knowledgebaseaction.RetrievalService
	// files 解析文件地址。
	files *fileOps
}

// newKnowledgeOps 创建知识库的业务实现依赖。
func newKnowledgeOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer, documentQuery *knowledgebaseaction.DocumentQuery, retrieval *knowledgebaseaction.RetrievalService, files *fileOps) *knowledgeOps {
	return &knowledgeOps{
		documentQuery:       documentQuery,
		createDocuments:     knowledgebaseaction.NewCreateDocumentsAction(db, taskEnqueuer),
		saveTextDocument:    knowledgebaseaction.NewSaveTextDocumentAction(db, taskEnqueuer),
		createWebDocument:   knowledgebaseaction.NewCreateWebDocumentAction(db, taskEnqueuer),
		renameDocument:      knowledgebaseaction.NewRenameDocumentAction(db),
		documentProcessing:  knowledgebaseaction.NewDocumentProcessing(db, taskEnqueuer),
		deleteDocument:      knowledgebaseaction.NewDeleteDocumentAction(db),
		listQAEntries:       knowledgebaseaction.NewListQAEntriesQuery(db),
		getQAEntry:          knowledgebaseaction.NewGetQAEntryQuery(db),
		saveQAEntry:         knowledgebaseaction.NewSaveQAEntryAction(db, taskEnqueuer),
		qaProcessing:        knowledgebaseaction.NewQAProcessing(db, taskEnqueuer),
		deleteQAEntry:       knowledgebaseaction.NewDeleteQAEntryAction(db),
		listKnowledgeBases:  knowledgebaseaction.NewListKnowledgeBasesQuery(db),
		getKnowledgeBase:    knowledgebaseaction.NewGetKnowledgeBaseQuery(db),
		listBaseAgents:      knowledgebaseaction.NewListKnowledgeBaseAgentsQuery(db),
		createKnowledgeBase: knowledgebaseaction.NewCreateKnowledgeBaseAction(db),
		updateKnowledgeBase: knowledgebaseaction.NewUpdateKnowledgeBaseAction(db, taskEnqueuer),
		deleteKnowledgeBase: knowledgebaseaction.NewDeleteKnowledgeBaseAction(db),
		retrieval:           retrieval,
		files:               files,
	}
}

// RetrieveKnowledgeBase 在指定知识库中执行检索测试。
func (o *knowledgeOps) RetrieveKnowledgeBase(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, knowledgeBaseID string, input appservice.KnowledgeRetrievalInput) (appservice.KnowledgeRetrievalResult, error) {
	records, err := o.retrieval.Retrieve(ctx, identity, knowledgeBaseID, input.Query)
	if err != nil {
		return appservice.KnowledgeRetrievalResult{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeRetrievalFailed, identity.Workspace.ID, knowledgeBaseID)
	}
	return appservice.KnowledgeRetrievalResult{Records: arr.Map(records, func(record knowledgebaseaction.RetrievalRecord) appservice.KnowledgeRetrievalRecord {
		return appservice.KnowledgeRetrievalRecord{
			DocumentID: record.DocumentID, DocumentName: record.DocumentName,
			SegmentID: record.SegmentID, SegmentBatchID: record.SegmentBatchID, Position: record.Position,
			Context: record.Context, Content: record.Content, Answer: record.Answer, Score: record.Score,
		}
	})}, nil
}

// ListKnowledgeBases 返回当前企业的知识库列表。
func (o *knowledgeOps) ListKnowledgeBases(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.KnowledgeBaseList, error) {
	records, err := o.listKnowledgeBases.Execute(ctx, identity)
	if err != nil {
		return appservice.KnowledgeBaseList{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeBaseListFailed, identity.Workspace.ID, "")
	}
	knowledgeBases := arr.Map(records, knowledgeBaseFromAction)
	return appservice.KnowledgeBaseList{KnowledgeBases: knowledgeBases}, nil
}

// GetKnowledgeBase 返回当前企业中的知识库详情。
func (o *knowledgeOps) GetKnowledgeBase(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, knowledgeBaseID string) (appservice.KnowledgeBase, error) {
	record, err := o.getKnowledgeBase.Execute(ctx, identity, knowledgeBaseID)
	if err != nil {
		return appservice.KnowledgeBase{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeBaseReadFailed, identity.Workspace.ID, knowledgeBaseID)
	}
	return knowledgeBaseFromAction(*record), nil
}

// ListKnowledgeBaseAgents 返回当前配置版本绑定知识库的 AI 员工。
func (o *knowledgeOps) ListKnowledgeBaseAgents(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, knowledgeBaseID string) (appservice.KnowledgeBaseAgentList, error) {
	agents, err := o.listBaseAgents.Execute(ctx, identity, knowledgeBaseID)
	if err != nil {
		return appservice.KnowledgeBaseAgentList{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeBaseReadFailed, identity.Workspace.ID, knowledgeBaseID)
	}
	return appservice.KnowledgeBaseAgentList{Agents: arr.Map(agents, func(agent knowledgebaseaction.AgentUsage) appservice.KnowledgeBaseAgent {
		return appservice.KnowledgeBaseAgent{ID: agent.ID, DisplayName: agent.DisplayName, Status: appservice.UserStatus(agent.Status)}
	})}, nil
}

// CreateKnowledgeBase 创建企业知识库。
func (o *knowledgeOps) CreateKnowledgeBase(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.KnowledgeBaseInput) (appservice.KnowledgeBase, error) {
	record, err := o.createKnowledgeBase.Execute(ctx, identity, knowledgebaseaction.Input{
		Name: input.Name, Category: input.Category, Description: input.Description,
		EmbeddingModelID:        input.EmbeddingModelID,
		EmbeddingDimension:      input.EmbeddingDimension,
		ChunkLength:             input.ChunkLength,
		ChunkOverlap:            input.ChunkOverlap,
		RetrievalCount:          input.RetrievalCount,
		RetrievalScoreThreshold: input.RetrievalScoreThreshold,
		RerankModelID:           input.RerankModelID,
	})
	if err != nil {
		return appservice.KnowledgeBase{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeBaseCreateFailed, identity.Workspace.ID, "")
	}
	slog.InfoContext(ctx, "知识库创建成功", "knowledge_base_id", record.ID, "category", record.Category)
	return knowledgeBaseFromAction(*record), nil
}

// UpdateKnowledgeBase 修改企业知识库。
func (o *knowledgeOps) UpdateKnowledgeBase(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, knowledgeBaseID string, input appservice.KnowledgeBaseInput) (appservice.KnowledgeBase, error) {
	record, err := o.updateKnowledgeBase.Execute(ctx, identity, knowledgeBaseID, knowledgebaseaction.Input{
		Name: input.Name, Category: input.Category, Description: input.Description,
		EmbeddingModelID:        input.EmbeddingModelID,
		EmbeddingDimension:      input.EmbeddingDimension,
		ChunkLength:             input.ChunkLength,
		ChunkOverlap:            input.ChunkOverlap,
		RetrievalCount:          input.RetrievalCount,
		RetrievalScoreThreshold: input.RetrievalScoreThreshold,
		RerankModelID:           input.RerankModelID,
	})
	if err != nil {
		return appservice.KnowledgeBase{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeBaseUpdateFailed, identity.Workspace.ID, knowledgeBaseID)
	}
	slog.InfoContext(ctx, "知识库保存成功", "knowledge_base_id", record.ID, "category", record.Category)
	return knowledgeBaseFromAction(*record), nil
}

// DeleteKnowledgeBase 删除企业知识库。
func (o *knowledgeOps) DeleteKnowledgeBase(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, knowledgeBaseID string) error {
	if err := o.deleteKnowledgeBase.Execute(ctx, identity, knowledgeBaseID); err != nil {
		return knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeBaseDeleteFailed, identity.Workspace.ID, knowledgeBaseID)
	}
	slog.InfoContext(ctx, "知识库删除成功", "knowledge_base_id", knowledgeBaseID)
	return nil
}

// knowledgeBaseRetrievalErrors 是知识库错误中先于模型调用失败匹配的转换规则。
var knowledgeBaseRetrievalErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.FieldRule(knowledgeBaseFieldKeys),
	dispatch.Is(fileaction.ErrFileNotFound, dispatch.NotFound(i18n.ErrorFileNotFound)),
	dispatch.Is(knowledgebaseaction.ErrRetrievalNotReady, dispatch.Conflict(i18n.ErrorKnowledgeRetrievalNotReady, "retrieval_not_ready")),
})

// knowledgeBaseErrors 是知识库错误中晚于模型调用失败匹配的转换规则。
var knowledgeBaseErrors = dispatch.Catalog{
	dispatch.Is(knowledgebaseaction.ErrSegmentsNotReady, dispatch.Conflict(i18n.ErrorKnowledgeSegmentsNotReady, "segments_not_ready")),
	dispatch.Is(knowledgebaseaction.ErrSegmentStale, dispatch.Conflict(i18n.ErrorKnowledgeSegmentStale, "segment_stale")),
	dispatch.Is(knowledgebaseaction.ErrSegmentQueryInvalid, dispatch.Invalid(i18n.ErrorValidationFailed)),
	dispatch.Is(knowledgebaseaction.ErrPageSizeInvalid, dispatch.Invalid(i18n.ErrorValidationFailed)),
	dispatch.Is(knowledgebaseaction.ErrDocumentNotFound, dispatch.NotFound(i18n.ErrorKnowledgeDocumentNotFound)),
	dispatch.Is(knowledgebaseaction.ErrDocumentUnsupported, dispatch.Invalid(i18n.ErrorKnowledgeDocumentUnsupported)),
	dispatch.Is(knowledgebaseaction.ErrDocumentSourceUnsupported, dispatch.Invalid(i18n.ErrorKnowledgeDocumentSourceUnsupported)),
	dispatch.Is(knowledgebaseaction.ErrDocumentURLDuplicate, dispatch.Conflict(i18n.ErrorKnowledgeDocumentURLDuplicate, "document_url_duplicate")),
	dispatch.Is(knowledgebaseaction.ErrDocumentBatchInvalid, dispatch.Invalid(i18n.ErrorKnowledgeDocumentBatchInvalid)),
	dispatch.Is(knowledgebaseaction.ErrQANotFound, dispatch.NotFound(i18n.ErrorKnowledgeQANotFound)),
	dispatch.Is(knowledgebaseaction.ErrQAUnsupported, dispatch.Invalid(i18n.ErrorKnowledgeQAUnsupported)),
	dispatch.Is(knowledgebaseaction.ErrBaseHasContent, dispatch.Invalid(i18n.ErrorKnowledgeBaseHasContent)),
	dispatch.Is(knowledgebaseaction.ErrNotFound, dispatch.NotFound(i18n.ErrorKnowledgeBaseNotFound)),
}

// knowledgeBaseError 转换知识库领域错误，向量与重排模型调用失败时记录模型错误码。
func knowledgeBaseError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, workspaceID, knowledgeBaseID string) error {
	if mapped := knowledgeBaseRetrievalErrors.Find(meta, err); mapped != nil {
		return mapped
	}
	if embeddingError, ok := errors.AsType[*modelcallaction.EmbeddingError](err); ok {
		slog.WarnContext(logscope.WithWorkspace(ctx, workspaceID), "知识库检索向量模型调用失败", "knowledge_base_id", knowledgeBaseID, "code", embeddingError.Code)
		return appservice.UnavailableError(meta, i18n.ErrorKnowledgeEmbeddingUnavailable, nil)
	}
	if rerankError, ok := errors.AsType[*modelcallaction.RerankError](err); ok {
		slog.WarnContext(logscope.WithWorkspace(ctx, workspaceID), "知识库检索重排模型调用失败", "knowledge_base_id", knowledgeBaseID, "code", rerankError.Code)
		return appservice.UnavailableError(meta, i18n.ErrorKnowledgeRerankUnavailable, nil)
	}
	return knowledgeBaseErrors.Translate(meta, err, failureKey)
}

// knowledgeBaseFromAction 转换知识库契约。
func knowledgeBaseFromAction(record knowledgebaseaction.Record) appservice.KnowledgeBase {
	return appservice.KnowledgeBase{
		ID: record.ID, Name: record.Name, Category: record.Category, Description: record.Description,
		EmbeddingModelID:        record.EmbeddingModelID,
		EmbeddingDimension:      record.EmbeddingDimension,
		ChunkLength:             record.ChunkLength,
		ChunkOverlap:            record.ChunkOverlap,
		RetrievalCount:          record.RetrievalCount,
		RetrievalScoreThreshold: record.RetrievalScoreThreshold,
		RerankModelID:           record.RerankModelID,

		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

// knowledgeBaseFieldKeys 把知识库校验错误码映射为本地化文案键。
var knowledgeBaseFieldKeys = map[common.FieldCode]i18n.Key{
	knowledgebaseaction.ValidationEmbeddingModelInvalid: i18n.FieldKnowledgeBaseEmbeddingModelInvalid,
	knowledgebaseaction.ValidationChunkLengthInvalid:    i18n.FieldKnowledgeBaseChunkLengthInvalid,
	knowledgebaseaction.ValidationChunkOverlapInvalid:   i18n.FieldKnowledgeBaseChunkOverlapInvalid,
	knowledgebaseaction.ValidationRerankModelInvalid:    i18n.FieldKnowledgeBaseRerankModelInvalid,

	knowledgebaseaction.ValidationDocumentURLInvalid: i18n.FieldKnowledgeDocumentURLInvalid,

	knowledgebaseaction.ValidationQAContentInvalid: i18n.FieldKnowledgeQAContentInvalid,
	knowledgebaseaction.ValidationNameDuplicate:    i18n.FieldKnowledgeBaseNameDuplicate,
}
