package domain

// KnowledgeGapSource 定义待补知识的来源。
type KnowledgeGapSource string

const (
	// KnowledgeGapSourceKnowledgeGap 表示 AI 因知识不足转人工。
	KnowledgeGapSourceKnowledgeGap KnowledgeGapSource = "knowledge_gap"
	// KnowledgeGapSourceInsufficientEvidence 表示 AI 因缺少依据转人工。
	KnowledgeGapSourceInsufficientEvidence KnowledgeGapSource = "insufficient_evidence"
	// KnowledgeGapSourceRatedUnresolved 表示 AI 关闭的周期被访客评价为未解决。
	KnowledgeGapSourceRatedUnresolved KnowledgeGapSource = "rated_unresolved"
	// KnowledgeGapSourcePossiblyWrong 表示判断模型认为 AI 客服在周期中的答复可能有误。
	KnowledgeGapSourcePossiblyWrong KnowledgeGapSource = "possibly_wrong"
)

// KnowledgeGapStatus 定义待补知识的处理状态。
type KnowledgeGapStatus string

const (
	KnowledgeGapStatusPending   KnowledgeGapStatus = "pending"
	KnowledgeGapStatusAccepted  KnowledgeGapStatus = "accepted"
	KnowledgeGapStatusDismissed KnowledgeGapStatus = "dismissed"
)

// KnowledgeGapDraftStatus 定义待补知识问答草稿的状态。
type KnowledgeGapDraftStatus string

const (
	KnowledgeGapDraftStatusPending     KnowledgeGapDraftStatus = "pending"
	KnowledgeGapDraftStatusReady       KnowledgeGapDraftStatus = "ready"
	KnowledgeGapDraftStatusFailed      KnowledgeGapDraftStatus = "failed"
	KnowledgeGapDraftStatusUnavailable KnowledgeGapDraftStatus = "unavailable"
)
