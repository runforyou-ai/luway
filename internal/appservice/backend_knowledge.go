package appservice

import "context"

// KnowledgeBackend 定义知识库、知识文档与问答条目的业务调用。
type KnowledgeBackend interface {
	// RetryKnowledgeDocument 按当前配置重新处理文档。
	//appservice:route POST /knowledge-bases/{knowledgeBaseID:uuid}/documents/{documentID:uuid}/retry perm=ai_employees.manage
	RetryKnowledgeDocument(context.Context, RequestMeta, string, string) error
	// ListKnowledgeDocumentSegments 返回固定批次的分段页或锚点所在页。
	//appservice:route GET /knowledge-bases/{knowledgeBaseID:uuid}/documents/{documentID:uuid}/segments perm=ai_employees.manage
	ListKnowledgeDocumentSegments(context.Context, RequestMeta, string, string, KnowledgeDocumentSegmentInput) (KnowledgeDocumentSegmentPage, error)
	// RetrieveKnowledgeBase 在指定知识库中执行检索测试，返回混合召回与重排后的分段。
	//appservice:route POST /knowledge-bases/{knowledgeBaseID:uuid}/retrieval perm=none
	RetrieveKnowledgeBase(context.Context, RequestMeta, string, KnowledgeRetrievalInput) (KnowledgeRetrievalResult, error)
	// ListKnowledgeDocuments 返回当前分组的文档列表。
	//appservice:route GET /knowledge-bases/{knowledgeBaseID:uuid}/documents perm=ai_employees.manage
	ListKnowledgeDocuments(context.Context, RequestMeta, string, KnowledgeDocumentListInput) (KnowledgeDocumentList, error)
	// GetKnowledgeDocument 返回文档详情。
	//appservice:route GET /knowledge-bases/{knowledgeBaseID:uuid}/documents/{documentID:uuid} perm=ai_employees.manage
	GetKnowledgeDocument(context.Context, RequestMeta, string, string) (KnowledgeDocument, error)
	// CreateKnowledgeDocuments 保存最多十个已上传的文档原件。
	//appservice:route POST /knowledge-bases/{knowledgeBaseID:uuid}/documents status=201 perm=ai_employees.manage
	CreateKnowledgeDocuments(context.Context, RequestMeta, string, KnowledgeDocumentBatchInput) (KnowledgeDocumentBatch, error)
	// DeleteKnowledgeDocument 删除文档并释放原件。
	//appservice:route DELETE /knowledge-bases/{knowledgeBaseID:uuid}/documents/{documentID:uuid} perm=ai_employees.manage
	DeleteKnowledgeDocument(context.Context, RequestMeta, string, string) error
	// GetKnowledgeDocumentPreview 签发当前文档的原件预览请求。
	//appservice:route GET /knowledge-bases/{knowledgeBaseID:uuid}/documents/{documentID:uuid}/preview perm=ai_employees.manage
	GetKnowledgeDocumentPreview(context.Context, RequestMeta, string, string) (KnowledgeDocumentPreviewRequest, error)
	// CreateKnowledgeTextDocument 创建在线编写的文档并安排索引。
	//appservice:route POST /knowledge-bases/{knowledgeBaseID:uuid}/text-documents status=201 perm=ai_employees.manage
	CreateKnowledgeTextDocument(context.Context, RequestMeta, string, KnowledgeTextDocumentInput) (KnowledgeDocument, error)
	// GetKnowledgeDocumentContent 返回在线文档正文或网页抓取快照。
	//appservice:route GET /knowledge-bases/{knowledgeBaseID:uuid}/documents/{documentID:uuid}/content perm=ai_employees.manage
	GetKnowledgeDocumentContent(context.Context, RequestMeta, string, string) (KnowledgeDocumentContent, error)
	// UpdateKnowledgeDocumentContent 修改在线文档的名称与正文并安排索引。
	//appservice:route PUT /knowledge-bases/{knowledgeBaseID:uuid}/documents/{documentID:uuid}/content perm=ai_employees.manage
	UpdateKnowledgeDocumentContent(context.Context, RequestMeta, string, string, KnowledgeDocumentContentInput) (KnowledgeDocument, error)
	// RenameKnowledgeDocument 修改在线文档或网页文档的名称。
	//appservice:route PUT /knowledge-bases/{knowledgeBaseID:uuid}/documents/{documentID:uuid} perm=ai_employees.manage
	RenameKnowledgeDocument(context.Context, RequestMeta, string, string, KnowledgeDocumentRenameInput) (KnowledgeDocument, error)
	// CreateKnowledgeWebDocument 导入网页并安排首次抓取。
	//appservice:route POST /knowledge-bases/{knowledgeBaseID:uuid}/web-documents status=201 perm=ai_employees.manage
	CreateKnowledgeWebDocument(context.Context, RequestMeta, string, KnowledgeWebDocumentInput) (KnowledgeDocument, error)
	// RefetchKnowledgeDocument 重新抓取网页文档并重新索引。
	//appservice:route POST /knowledge-bases/{knowledgeBaseID:uuid}/documents/{documentID:uuid}/refetch perm=ai_employees.manage
	RefetchKnowledgeDocument(context.Context, RequestMeta, string, string, KnowledgeDocumentRefetchInput) error
	// ListKnowledgeQAEntries 返回分组中的本地问答列表。
	//appservice:route GET /knowledge-bases/{knowledgeBaseID:uuid}/qa-entries perm=ai_employees.manage
	ListKnowledgeQAEntries(context.Context, RequestMeta, string, KnowledgeQAListInput) (KnowledgeQAList, error)
	// GetKnowledgeQAEntry 返回完整的本地问答。
	//appservice:route GET /knowledge-bases/{knowledgeBaseID:uuid}/qa-entries/{entryID:uuid} perm=none
	GetKnowledgeQAEntry(context.Context, RequestMeta, string, string) (KnowledgeQAEntry, error)
	// CreateKnowledgeQAEntry 创建本地问答。
	//appservice:route POST /knowledge-bases/{knowledgeBaseID:uuid}/qa-entries status=201 perm=ai_employees.manage
	CreateKnowledgeQAEntry(context.Context, RequestMeta, string, KnowledgeQAInput) (KnowledgeQAEntry, error)
	// UpdateKnowledgeQAEntry 修改本地问答。
	//appservice:route PUT /knowledge-bases/{knowledgeBaseID:uuid}/qa-entries/{entryID:uuid} perm=ai_employees.manage
	UpdateKnowledgeQAEntry(context.Context, RequestMeta, string, string, KnowledgeQAInput) (KnowledgeQAEntry, error)
	// DeleteKnowledgeQAEntry 删除本地问答。
	//appservice:route DELETE /knowledge-bases/{knowledgeBaseID:uuid}/qa-entries/{entryID:uuid} perm=ai_employees.manage
	DeleteKnowledgeQAEntry(context.Context, RequestMeta, string, string) error
	// RetryKnowledgeQAEntry 按当前配置重新索引问答。
	//appservice:route POST /knowledge-bases/{knowledgeBaseID:uuid}/qa-entries/{entryID:uuid}/retry perm=ai_employees.manage
	RetryKnowledgeQAEntry(context.Context, RequestMeta, string, string) error
	// ListKnowledgeBases 返回当前企业的知识库列表。
	//appservice:route GET /knowledge-bases perm=none
	ListKnowledgeBases(context.Context, RequestMeta) (KnowledgeBaseList, error)
	// GetKnowledgeBase 返回当前企业中的知识库详情。
	//appservice:route GET /knowledge-bases/{knowledgeBaseID:uuid} perm=ai_employees.manage
	GetKnowledgeBase(context.Context, RequestMeta, string) (KnowledgeBase, error)
	// ListKnowledgeBaseAgents 返回当前配置版本绑定知识库的 AI 员工。
	//appservice:route GET /knowledge-bases/{knowledgeBaseID:uuid}/agents perm=ai_employees.manage
	ListKnowledgeBaseAgents(context.Context, RequestMeta, string) (KnowledgeBaseAgentList, error)
	// CreateKnowledgeBase 创建企业知识库。
	//appservice:route POST /knowledge-bases status=201 perm=ai_employees.manage
	CreateKnowledgeBase(context.Context, RequestMeta, KnowledgeBaseInput) (KnowledgeBase, error)
	// UpdateKnowledgeBase 修改企业知识库。
	//appservice:route PUT /knowledge-bases/{knowledgeBaseID:uuid} perm=ai_employees.manage
	UpdateKnowledgeBase(context.Context, RequestMeta, string, KnowledgeBaseInput) (KnowledgeBase, error)
	// DeleteKnowledgeBase 删除企业知识库。
	//appservice:route DELETE /knowledge-bases/{knowledgeBaseID:uuid} perm=ai_employees.manage
	DeleteKnowledgeBase(context.Context, RequestMeta, string) error
}
