//go:build server

package inbox

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/searchtext"
	"github.com/uptrace/bun"
)

// searchResultLimit 是每组检索结果返回的最大数量。
const searchResultLimit = 6

// SearchRange 表示检索覆盖的会话范围。
type SearchRange string

const (
	// SearchRangeList 按当前列表筛选检索会话。
	SearchRangeList SearchRange = "list"
	// SearchRangeReadable 检索当前身份可阅读的全部会话。
	SearchRangeReadable SearchRange = "readable"
	// SearchRangeConversation 只检索指定会话的消息。
	SearchRangeConversation SearchRange = "conversation"
)

// SearchPersonKind 表示人员结果的来源。
type SearchPersonKind string

const (
	// SearchPersonMember 表示企业成员或 AI 员工。
	SearchPersonMember SearchPersonKind = "member"
	// SearchPersonContact 表示外部联系人。
	SearchPersonContact SearchPersonKind = "contact"
)

// SearchInput 定义收件箱检索文本与范围，List 只在列表范围生效，ConversationID 只在会话范围生效。
type SearchInput struct {
	Text           string
	Range          SearchRange
	List           LoadInput
	ConversationID string
}

// SearchMessage 表示命中的消息、所在会话摘要和高亮摘要。
type SearchMessage struct {
	ID                  string
	Type                domain.MessageType
	SenderName          *string
	SenderContactNumber *int64
	OriginatedAt        time.Time
	Excerpt             []searchtext.Segment
	Conversation        ConversationSummary
}

// SearchPerson 表示命中的企业成员或外部联系人；真人成员携带用户编号，AI 员工携带 Agent 编号，外部联系人携带最近一次客户会话。
type SearchPerson struct {
	Kind           SearchPersonKind                `bun:"kind"`
	ID             string                          `bun:"id"`
	UserID         *string                         `bun:"user_id"`
	AgentID        *string                         `bun:"agent_id"`
	IdentityType   domain.OrganizationIdentityType `bun:"identity_type"`
	DisplayName    string                          `bun:"display_name"`
	ContactNumber  *int64                          `bun:"contact_number"`
	AvatarFileID   *string                         `bun:"avatar_file_id"`
	ConversationID *string                         `bun:"conversation_id"`
}

// SearchResult 保存各组检索结果；跨会话命中的消息按写入时间倒序，会话内命中的消息按消息序号倒序。
type SearchResult struct {
	Conversations []ConversationSummary
	Messages      []SearchMessage
	People        []SearchPerson
}

type searchMessageRow struct {
	ID                  string             `bun:"id"`
	ConversationID      string             `bun:"conversation_id"`
	Type                domain.MessageType `bun:"type"`
	Body                string             `bun:"body"`
	AttachmentName      *string            `bun:"attachment_name"`
	SenderName          *string            `bun:"sender_name"`
	SenderContactNumber *int64             `bun:"sender_contact_number"`
	OriginatedAt        time.Time          `bun:"originated_at"`
}

// Search 在同一只读快照中检索会话名称、消息正文与附件文件名、成员和外部联系人；会话范围只检索消息。
func (q *LoadInboxQuery) Search(ctx context.Context, identity *servermodels.Identity, input SearchInput) (SearchResult, error) {
	result := SearchResult{Conversations: []ConversationSummary{}, Messages: []SearchMessage{}, People: []SearchPerson{}}
	switch input.Range {
	case SearchRangeList:
		normalized, err := normalizeLoadInput(input.List)
		if err != nil {
			return result, err
		}
		input.List = normalized
	case SearchRangeReadable:
	case SearchRangeConversation:
		if !common.ValidUUID(input.ConversationID) {
			return result, ErrQueryInvalid
		}
	default:
		return result, ErrQueryInvalid
	}
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return result, nil
	}
	// 会话分组与会话名称搜索分页共用同一候选投影，分组即分页首页的前几条。
	names := LoadInput{Search: text, SearchRange: SearchRangeReadable}
	if input.Range == SearchRangeList {
		names = input.List
		names.Search, names.SearchRange = text, SearchRangeList
	}
	names, err := normalizeLoadInput(names)
	if err != nil {
		return result, err
	}
	err = q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		snapshot := NewLoadInboxQuery(tx)
		candidates := snapshot.readableCandidates(identity)
		if input.Range == SearchRangeList {
			candidates = snapshot.listCandidates(identity, input.List)
		}
		if input.Range == SearchRangeConversation {
			readable, err := tx.NewSelect().TableExpr("(?) AS candidates", candidates).Where("candidates.id = ?", input.ConversationID).Exists(ctx)
			if err != nil {
				return err
			}
			if !readable {
				return ErrConversationUnavailable
			}
		}
		var rows []searchMessageRow
		query, searchable := searchtext.ParseQuery(text)
		if searchable {
			var err error
			if rows, err = snapshot.searchMessages(ctx, identity, query, candidates, input); err != nil {
				return err
			}
		}
		var conversationIDs []string
		if input.Range != SearchRangeConversation {
			points, err := snapshot.readNeighborPoints(ctx, identity, names, nil, false, searchResultLimit)
			if err != nil {
				return err
			}
			for _, point := range points {
				conversationIDs = append(conversationIDs, point.ID)
			}
			if result.People, err = snapshot.searchPeople(ctx, identity, text); err != nil {
				return err
			}
		}
		ids := slices.Clone(conversationIDs)
		for _, row := range rows {
			if !slices.Contains(ids, row.ConversationID) {
				ids = append(ids, row.ConversationID)
			}
		}
		if len(ids) == 0 {
			return nil
		}
		summaries, err := snapshot.readSummaries(ctx, identity, ids, input.List.serviceView())
		if err != nil {
			return err
		}
		for _, id := range conversationIDs {
			if summary := summaries[id]; summary != nil {
				result.Conversations = append(result.Conversations, *summary)
			}
		}
		for _, row := range rows {
			summary := summaries[row.ConversationID]
			if summary == nil {
				continue
			}
			// 摘要优先取正文命中，正文未命中时取附件文件名命中。
			excerpt, matched := query.Excerpt(row.Body)
			if !matched && row.AttachmentName != nil {
				excerpt, _ = query.Excerpt(*row.AttachmentName)
			}
			result.Messages = append(result.Messages, SearchMessage{
				ID: row.ID, Type: row.Type, SenderName: row.SenderName, SenderContactNumber: row.SenderContactNumber, OriginatedAt: row.OriginatedAt, Excerpt: excerpt, Conversation: *summary,
			})
		}
		return nil
	})
	return result, err
}

// searchMessages 按检索词元读取候选会话内的文本与附件消息；会话内按消息序号倒序，跨会话按消息编号（写入时间）倒序。
func (q *LoadInboxQuery) searchMessages(ctx context.Context, identity *servermodels.Identity, query searchtext.Query, candidates *bun.SelectQuery, input SearchInput) ([]searchMessageRow, error) {
	messages := q.db.NewSelect().TableExpr("messages AS msg").
		ColumnExpr("msg.id::text AS id, msg.conversation_id::text AS conversation_id, msg.type, msg.body, msg.originated_at").
		ColumnExpr("ma.name AS attachment_name").
		ColumnExpr("CASE WHEN cs.kind = ? THEN "+contactname.Expr("c", "cci.display_name")+" WHEN cs.kind = ? THEN oi.display_name END AS sender_name", domain.ChatSubjectKindContact, domain.ChatSubjectKindOrganizationIdentity).
		ColumnExpr("c.number AS sender_contact_number").
		Join("LEFT JOIN message_attachments AS ma ON ma.organization_id = msg.organization_id AND ma.message_id = msg.id").
		Join("LEFT JOIN conversation_participants AS cp ON cp.organization_id = msg.organization_id AND cp.conversation_id = msg.conversation_id AND cp.id = msg.sender_participant_id").
		Join("LEFT JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id").
		Join("LEFT JOIN organization_identities AS oi ON oi.organization_id = cs.organization_id AND oi.id = cs.source_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("LEFT JOIN contacts AS c ON c.organization_id = cs.organization_id AND c.id = cs.source_id AND cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN channel_conversations AS cc ON cc.organization_id = msg.organization_id AND cc.conversation_id = msg.conversation_id").
		Join("LEFT JOIN contact_channel_identities AS cci ON cci.organization_id = cc.organization_id AND cci.id = cc.contact_channel_identity_id AND cci.contact_id = cs.source_id AND cs.kind = ?", domain.ChatSubjectKindContact).
		Where("msg.organization_id = ?", identity.Organization.ID)
	if input.Range == SearchRangeConversation {
		messages = messages.Where("msg.conversation_id = ?", input.ConversationID).
			Where("msg.deleted_at IS NULL").
			Where("msg.type IN (?)", bun.In([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment})).
			Where("msg.search_vector @@ ?::tsquery", query.TSQuery()).
			Where("?", messagequery.VisibleTo("msg", identity.OrganizationIdentity.ID)).
			OrderExpr("msg.message_seq DESC").Limit(searchResultLimit)
	} else {
		ids, err := q.searchMessageIDs(ctx, identity, query, candidates)
		if err != nil || len(ids) == 0 {
			return nil, err
		}
		messages = messages.Where("msg.id IN (?)", bun.In(ids)).OrderExpr("msg.id DESC")
	}
	var rows []searchMessageRow
	if err := messages.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// searchMessageIDs 从当前月份起向更早的月份分段查找候选会话内的命中消息，按编号倒序返回，凑满结果即停止；各段依次覆盖 1、2、4…个月，以字面量编号边界裁剪到对应分区，查找止于工作区创建月份。
func (q *LoadInboxQuery) searchMessageIDs(ctx context.Context, identity *servermodels.Identity, query searchtext.Query, candidates *bun.SelectQuery) ([]string, error) {
	var createdAt time.Time
	if err := q.db.NewSelect().Table("organizations").Column("created_at").Where("id = ?", identity.Organization.ID).Scan(ctx, &createdAt); err != nil {
		return nil, err
	}
	first := serverstorage.MessageMonthStart(createdAt)
	ids := make([]string, 0, searchResultLimit)
	end := serverstorage.MessageMonthStart(time.Now()).AddDate(0, 1, 0)
	for span := 1; len(ids) < searchResultLimit && end.After(first); span *= 2 {
		start := end.AddDate(0, -span, 0)
		if start.Before(first) {
			start = first
		}
		var batch []string
		if err := q.db.NewSelect().TableExpr("messages AS msg").ColumnExpr("msg.id::text").
			Where("msg.organization_id = ?", identity.Organization.ID).
			Where("msg.id >= ? AND msg.id < ?", serverstorage.MessageIDLowerBound(start), serverstorage.MessageIDLowerBound(end)).
			Where("msg.deleted_at IS NULL").
			Where("msg.type IN (?)", bun.In([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment})).
			Where("msg.search_vector @@ ?::tsquery", query.TSQuery()).
			Where("?", messagequery.VisibleTo("msg", identity.OrganizationIdentity.ID)).
			Where("msg.conversation_id IN (SELECT candidates.id FROM (?) AS candidates)", candidates).
			OrderExpr("msg.id DESC").Limit(searchResultLimit-len(ids)).
			Scan(ctx, &batch); err != nil {
			return nil, err
		}
		ids = append(ids, batch...)
		end = start
	}
	return ids, nil
}

// searchPeople 在全企业通讯录中匹配活跃成员、AI 员工、本人名下的助理和外部联系人，不随会话范围收窄；成员优先，外部联系人补足剩余名额。
func (q *LoadInboxQuery) searchPeople(ctx context.Context, identity *servermodels.Identity, text string) ([]SearchPerson, error) {
	pattern := common.ContainsPattern(text)
	people := []SearchPerson{}
	if err := q.db.NewSelect().TableExpr("organization_identities AS oi").
		ColumnExpr("? AS kind, oi.id::text AS id, u.id::text AS user_id, a.id::text AS agent_id, oi.type AS identity_type, oi.display_name, oi.avatar_file_id::text AS avatar_file_id", SearchPersonMember).
		Join("LEFT JOIN users AS u ON u.organization_id = oi.organization_id AND u.identity_id = oi.id").
		Join("LEFT JOIN accounts AS acc ON acc.id = u.account_id").
		Join("LEFT JOIN agents AS a ON a.organization_id = oi.organization_id AND a.identity_id = oi.id").
		Where("oi.organization_id = ? AND oi.id <> ?", identity.Organization.ID, identity.OrganizationIdentity.ID).
		Where("((oi.type = ? AND u.status = ?) OR (oi.type = ? AND a.status = ?) OR (oi.type = ? AND a.status = ? AND a.owner_user_id = ?))",
			domain.OrganizationIdentityTypeUser, domain.IdentityStatusActive, domain.OrganizationIdentityTypeAgent, domain.IdentityStatusActive,
			domain.OrganizationIdentityTypeAssistant, domain.IdentityStatusActive, identity.User.ID).
		Where("(oi.display_name ILIKE ? OR acc.email ILIKE ?)", pattern, pattern).
		OrderExpr("lower(oi.display_name), oi.id").
		Limit(searchResultLimit).
		Scan(ctx, &people); err != nil {
		return nil, err
	}
	remaining := searchResultLimit - len(people)
	if remaining == 0 {
		return people, nil
	}
	// 外部联系人关联最近一次客户会话，关联条件与客户会话阅读范围一致；有会话的联系人排在前面。
	var contacts []SearchPerson
	if err := q.db.NewSelect().TableExpr("contacts AS c").
		ColumnExpr("? AS kind, c.id::text AS id, c.number AS contact_number, latest.avatar_file_id, latest.conversation_id", SearchPersonContact).
		ColumnExpr("COALESCE("+contactname.Expr("c", contactname.LatestIdentityName("c"))+", '') AS display_name").
		Join(`LEFT JOIN LATERAL (
			SELECT cv.id::text AS conversation_id, cci.display_name AS channel_display_name, cci.avatar_file_id::text AS avatar_file_id
			FROM channel_conversations AS cc
			JOIN contact_channel_identities AS cci ON cci.organization_id = cc.organization_id AND cci.id = cc.contact_channel_identity_id
			JOIN conversations AS cv ON cv.organization_id = cc.organization_id AND cv.id = cc.conversation_id
			JOIN service_conversations AS svc ON svc.organization_id = cc.organization_id AND svc.conversation_id = cc.conversation_id
			JOIN service_sessions AS current ON current.organization_id = svc.organization_id AND current.service_conversation_id = svc.id AND current.id = svc.current_service_session_id
			WHERE cc.organization_id = c.organization_id AND cci.contact_id = c.id
			ORDER BY cv.last_activity_at DESC NULLS LAST, cv.id DESC
			LIMIT 1
		) AS latest ON TRUE`).
		Where("c.organization_id = ? AND c.deleted_at IS NULL", identity.Organization.ID).
		Where(`(COALESCE(c.display_name, '') ILIKE ?
			OR EXISTS (SELECT 1 FROM contact_methods AS cm WHERE cm.organization_id = c.organization_id AND cm.contact_id = c.id AND cm.normalized_value ILIKE ?)
			OR EXISTS (SELECT 1 FROM contact_channel_identities AS name_cci WHERE name_cci.organization_id = c.organization_id AND name_cci.contact_id = c.id AND COALESCE(name_cci.display_name, '') ILIKE ?)
			OR c.number = ?)`, pattern, pattern, pattern, contactname.Number(text)).
		OrderExpr("latest.conversation_id IS NULL, lower(COALESCE("+contactname.Expr("c", contactname.LatestIdentityName("c"))+", '')), c.id").
		Limit(remaining).
		Scan(ctx, &contacts); err != nil {
		return nil, err
	}
	return append(people, contacts...), nil
}

// readableCandidates 返回当前身份可阅读的全部会话，不附加列表筛选；同时是服务会话与本人 AI 聊天的会话只出现一次，取较晚的活动时间。
func (q *LoadInboxQuery) readableCandidates(identity *servermodels.Identity) *bun.SelectQuery {
	organizationID, identityID := identity.Organization.ID, identity.OrganizationIdentity.ID
	readable := q.serviceConversationAccessQuery(organizationID, identityID).
		UnionAll(q.directConversationAccessQuery(organizationID, identityID)).
		UnionAll(q.agentConversationAccessQuery(organizationID, identityID)).
		UnionAll(q.groupConversationAccessQuery(organizationID, identityID))
	return q.db.NewSelect().TableExpr("(?) AS readable", readable).
		ColumnExpr("readable.id, max(readable.last_activity_at) AS last_activity_at").
		GroupExpr("readable.id")
}
