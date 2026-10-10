package domain

// KnowledgeQAContentKind 表示问答内容的业务用途。
type KnowledgeQAContentKind string

const (
	KnowledgeQAContentPrimaryQuestion KnowledgeQAContentKind = "primary_question"
	KnowledgeQAContentSimilarQuestion KnowledgeQAContentKind = "similar_question"
	KnowledgeQAContentAnswer          KnowledgeQAContentKind = "answer"
)
