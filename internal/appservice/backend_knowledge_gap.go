package appservice

import "context"

// KnowledgeGapBackend 定义待补知识的业务调用。
type KnowledgeGapBackend interface {
	// ListKnowledgeGaps 返回一页指定处理状态的待补知识，并标出当前成员能否处理每一条。
	//appservice:route GET /knowledge-gaps perm=none
	ListKnowledgeGaps(context.Context, RequestMeta, KnowledgeGapListInput) (KnowledgeGapList, error)
	// GetKnowledgeGap 返回待补知识详情，并标出当前成员能否处理。
	//appservice:route GET /knowledge-gaps/{gapID:uuid} perm=none
	GetKnowledgeGap(context.Context, RequestMeta, string) (KnowledgeGap, error)
	// AcceptKnowledgeGap 把待补知识整理的问答加入知识库，须负责接待 AI 员工或拥有 AI 员工管理权限。
	//appservice:route POST /knowledge-gaps/{gapID:uuid}/accept perm=none
	AcceptKnowledgeGap(context.Context, RequestMeta, string, KnowledgeGapAcceptInput) error
	// DismissKnowledgeGap 忽略待补知识，须负责接待 AI 员工或拥有 AI 员工管理权限。
	//appservice:route POST /knowledge-gaps/{gapID:uuid}/dismiss perm=none
	DismissKnowledgeGap(context.Context, RequestMeta, string) error
}
