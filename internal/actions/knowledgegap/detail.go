//go:build server

package knowledgegap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// transcriptLimit 是沟通记录返回的周期内最近对客消息条数上限。
const transcriptLimit = 200

// Message 定义沟通记录中的一条对客消息；Sender 为 customer 发起人、ai AI 员工或 staff 真人处理人，发起人的 SenderName 为空。
type Message struct {
	ID         string    `bun:"id"`
	Type       string    `bun:"type"`
	Sender     string    `bun:"sender"`
	SenderName string    `bun:"sender_name"`
	Body       string    `bun:"body"`
	CreatedAt  time.Time `bun:"created_at"`
}

// Detail 定义待补知识详情：来源周期的对客沟通、AI 起草的问答、默认加入的知识库与处理结果。
type Detail struct {
	servermodels.KnowledgeGap `bun:",extend"`
	Question                  string    `bun:"question"`
	CategoryName              *string   `bun:"category_name"`
	AgentIdentityID           *string   `bun:"agent_identity_id"`
	Evaluable                 bool      `bun:"evaluable"`
	Handleable                bool      `bun:"handleable"`
	DefaultKnowledgeBaseID    *string   `bun:"-"`
	Messages                  []Message `bun:"-"`
}

// GetQuery 读取待补知识详情。
type GetQuery struct{ db *bun.DB }

// NewGetQuery 创建待补知识详情查询。
func NewGetQuery(db *bun.DB) *GetQuery { return &GetQuery{db: db} }

// Execute 返回待补知识详情并标出当前成员能否处理；默认知识库取来源周期的接待 AI 员工当前绑定的第一个问答知识库。
func (q *GetQuery) Execute(ctx context.Context, identity *servermodels.Identity, id string) (*Detail, error) {
	detail := &Detail{}
	err := q.db.NewSelect().Model(detail).
		ColumnExpr("kg.*").
		ColumnExpr("sc.name AS category_name, ss.agent_identity_id").
		ColumnExpr("? AS handleable", handleableSQL(identity)).
		ColumnExpr("coalesce(CASE WHEN qm.deleted_at IS NULL THEN qm.body END, '') AS question").
		// 提问为未删除、有正文的文字消息，接待的 AI 员工仍服务该周期的服务对象，且提问尚未加入评测时可以加入；规则与 agentevaluation 的 addCapturedCase 一致。
		ColumnExpr("coalesce(qm.type = ? AND qm.deleted_at IS NULL AND btrim(qm.body) <> '' AND EXISTS (?) AND NOT EXISTS (?), false) AS evaluable", domain.MessageTypeText,
			q.db.NewSelect().TableExpr("agents AS a").ColumnExpr("1").
				Join("JOIN service_conversations AS svc ON svc.id = ss.service_conversation_id AND svc.workspace_id = ss.workspace_id").
				Where("a.workspace_id = ss.workspace_id AND a.identity_id = ss.agent_identity_id").
				Where("svc.audience = ANY(a.service_audiences)"),
			q.db.NewSelect().TableExpr("agent_evaluation_cases AS aec").ColumnExpr("1").
				Where("aec.workspace_id = kg.workspace_id AND aec.question_message_id = kg.question_message_id")).
		Join("JOIN service_sessions AS ss ON ss.id = kg.service_session_id AND ss.workspace_id = kg.workspace_id").
		Join("LEFT JOIN service_categories AS sc ON sc.id = ss.category_id AND sc.workspace_id = ss.workspace_id").
		Join("LEFT JOIN messages AS qm ON qm.id = kg.question_message_id AND qm.workspace_id = kg.workspace_id").
		Where("kg.workspace_id = ? AND kg.id = ?", identity.Workspace.ID, id).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load knowledge gap: %w", err)
	}
	if detail.AgentIdentityID != nil {
		ids := make([]string, 0, 1)
		if err := q.db.NewSelect().
			TableExpr("agents AS a").
			Column("kb.id").
			Join("JOIN agent_revision_knowledge_bases AS arkb ON arkb.workspace_id = a.workspace_id AND arkb.agent_id = a.id AND arkb.revision_id = a.active_revision_id").
			Join("JOIN knowledge_bases AS kb ON kb.workspace_id = arkb.workspace_id AND kb.id = arkb.knowledge_base_id AND kb.category = ?", domain.KnowledgeBaseCategoryQA).
			Where("a.workspace_id = ? AND a.identity_id = ?", identity.Workspace.ID, *detail.AgentIdentityID).
			OrderExpr("kb.name ASC, kb.id ASC").
			Limit(1).
			Scan(ctx, &ids); err != nil {
			return nil, fmt.Errorf("load knowledge gap default knowledge base: %w", err)
		}
		if len(ids) > 0 {
			detail.DefaultKnowledgeBaseID = &ids[0]
		}
	}
	messages, err := Transcript(ctx, q.db, identity.Workspace.ID, detail.ServiceSessionID)
	if err != nil {
		return nil, err
	}
	detail.Messages = messages
	return detail, nil
}

// Transcript 读取客服周期内最近的对客文本与附件消息，按发送顺序返回。
func Transcript(ctx context.Context, db bun.IDB, workspaceID, serviceSessionID string) ([]Message, error) {
	messages := make([]Message, 0, transcriptLimit)
	if err := db.NewSelect().
		TableExpr("messages AS m").
		ColumnExpr("m.id, m.type, m.created_at").
		ColumnExpr("? AS body", messagequery.Summary("m")).
		ColumnExpr("CASE WHEN cp.subject_id = svc.requester_subject_id THEN 'customer' WHEN oi.type = ? THEN 'ai' ELSE 'staff' END AS sender",
			domain.WorkspaceIdentityTypeAgent).
		ColumnExpr("CASE WHEN cp.subject_id = svc.requester_subject_id THEN '' ELSE coalesce(oi.display_name, '') END AS sender_name").
		Join("JOIN conversation_participants AS cp ON cp.id = m.sender_participant_id AND cp.workspace_id = m.workspace_id").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = m.workspace_id AND svc.conversation_id = m.conversation_id").
		Join("LEFT JOIN workspace_identities AS oi ON oi.id = cs.source_id AND oi.workspace_id = cs.workspace_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Where("m.workspace_id = ? AND m.service_session_id = ?", workspaceID, serviceSessionID).
		Where("m.type IN (?, ?)", domain.MessageTypeText, domain.MessageTypeAttachment).
		Where("m.visibility = ? AND m.deleted_at IS NULL", domain.MessageVisibilityShared).
		Where("cs.kind IN (?, ?)", domain.ChatSubjectKindContact, domain.ChatSubjectKindWorkspaceIdentity).
		OrderExpr("m.message_seq DESC").
		Limit(transcriptLimit).
		Scan(ctx, &messages); err != nil {
		return nil, fmt.Errorf("load service session transcript: %w", err)
	}
	slices.Reverse(messages)
	return messages, nil
}
