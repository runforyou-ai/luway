package appservice

import "github.com/runforyou-ai/cervi/internal/domain"

// KnowledgeIndexStatus 定义知识来源的索引展示状态，文档与问答条目共用。
type KnowledgeIndexStatus string

const (
	KnowledgeIndexInitial   KnowledgeIndexStatus = "initial"
	KnowledgeIndexQueued    KnowledgeIndexStatus = "queued"
	KnowledgeIndexRunning   KnowledgeIndexStatus = "running"
	KnowledgeIndexSucceeded KnowledgeIndexStatus = "succeeded"
	KnowledgeIndexFailed    KnowledgeIndexStatus = "failed"
	KnowledgeIndexCancelled KnowledgeIndexStatus = "cancelled"
)

// KnowledgeIndexProcessingStatus 定义知识来源索引流程的执行状态。
type KnowledgeIndexProcessingStatus string

const (
	KnowledgeProcessingInitial    KnowledgeIndexProcessingStatus = KnowledgeIndexProcessingStatus(domain.KnowledgeIndexInitial)
	KnowledgeProcessingQueued     KnowledgeIndexProcessingStatus = KnowledgeIndexProcessingStatus(domain.KnowledgeIndexQueued)
	KnowledgeProcessingFetching   KnowledgeIndexProcessingStatus = KnowledgeIndexProcessingStatus(domain.KnowledgeIndexFetching)
	KnowledgeProcessingConverting KnowledgeIndexProcessingStatus = KnowledgeIndexProcessingStatus(domain.KnowledgeIndexConverting)
	KnowledgeProcessingSplitting  KnowledgeIndexProcessingStatus = KnowledgeIndexProcessingStatus(domain.KnowledgeIndexSplitting)
	KnowledgeProcessingEmbedding  KnowledgeIndexProcessingStatus = KnowledgeIndexProcessingStatus(domain.KnowledgeIndexEmbedding)
	KnowledgeProcessingPublishing KnowledgeIndexProcessingStatus = KnowledgeIndexProcessingStatus(domain.KnowledgeIndexPublishing)
	KnowledgeProcessingSucceeded  KnowledgeIndexProcessingStatus = KnowledgeIndexProcessingStatus(domain.KnowledgeIndexSucceeded)
	KnowledgeProcessingFailed     KnowledgeIndexProcessingStatus = KnowledgeIndexProcessingStatus(domain.KnowledgeIndexFailed)
	KnowledgeProcessingCancelled  KnowledgeIndexProcessingStatus = KnowledgeIndexProcessingStatus(domain.KnowledgeIndexCancelled)
)
