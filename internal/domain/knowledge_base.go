package domain

const (
	// KnowledgeBaseNameMaxLength 是知识库名称允许的最大字符数。
	KnowledgeBaseNameMaxLength = 120
	// KnowledgeBaseDescriptionMaxLength 是知识库描述允许的最大字符数。
	KnowledgeBaseDescriptionMaxLength = 1000
	// KnowledgeRetrievalQueryMaxLength 是知识库检索内容允许的最大字符数。
	KnowledgeRetrievalQueryMaxLength = 250
)

// KnowledgeEmbeddingDimensions 是知识库可选的向量维度。
var KnowledgeEmbeddingDimensions = []int{384, 512, 768, 1024, 1536, 2048, 3072}

// KnowledgeBaseCategory 表示知识库内容类型。
type KnowledgeBaseCategory string

const (
	KnowledgeBaseCategoryStandard KnowledgeBaseCategory = "standard"
	KnowledgeBaseCategoryQA       KnowledgeBaseCategory = "qa"
)
