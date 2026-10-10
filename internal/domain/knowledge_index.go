package domain

// KnowledgeIndexStatus 表示知识来源索引流程的执行状态，文档与问答条目共用。
type KnowledgeIndexStatus string

const (
	KnowledgeIndexInitial    KnowledgeIndexStatus = "initial"
	KnowledgeIndexQueued     KnowledgeIndexStatus = "queued"
	KnowledgeIndexFetching   KnowledgeIndexStatus = "fetching"
	KnowledgeIndexConverting KnowledgeIndexStatus = "converting"
	KnowledgeIndexSplitting  KnowledgeIndexStatus = "splitting"
	KnowledgeIndexEmbedding  KnowledgeIndexStatus = "embedding"
	KnowledgeIndexPublishing KnowledgeIndexStatus = "publishing"
	KnowledgeIndexSucceeded  KnowledgeIndexStatus = "succeeded"
	KnowledgeIndexFailed     KnowledgeIndexStatus = "failed"
	KnowledgeIndexCancelled  KnowledgeIndexStatus = "cancelled"
)

// IsProcessing 判断来源是否处于待处理或执行中的状态。
func (status KnowledgeIndexStatus) IsProcessing() bool {
	switch status {
	case KnowledgeIndexQueued, KnowledgeIndexFetching, KnowledgeIndexConverting, KnowledgeIndexSplitting, KnowledgeIndexEmbedding, KnowledgeIndexPublishing:
		return true
	default:
		return false
	}
}

// KnowledgeSourceType 表示分段所属的知识来源类型。
type KnowledgeSourceType string

const (
	KnowledgeSourceDocument KnowledgeSourceType = "document"
	KnowledgeSourceQAEntry  KnowledgeSourceType = "qa_entry"
)

const (
	// KnowledgeQAChunkLength 是问答答案分段的字符长度。
	KnowledgeQAChunkLength = 512
	// KnowledgeQAChunkOverlap 是问答答案分段的重叠字符数。
	KnowledgeQAChunkOverlap = 50
)
