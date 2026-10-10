package domain

// KnowledgeRetrievalQueryMaxLength 是知识库检索内容允许的最大字符数。
const KnowledgeRetrievalQueryMaxLength = 250

// KnowledgeBaseCategory 表示知识库内容类型。
type KnowledgeBaseCategory string

const (
	KnowledgeBaseCategoryStandard KnowledgeBaseCategory = "standard"
	KnowledgeBaseCategoryQA       KnowledgeBaseCategory = "qa"
)
