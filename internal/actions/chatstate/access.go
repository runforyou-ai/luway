//go:build server

// Package chatstate 提供聊天 Action 与 Query 共用的访问资格、事务锁定与消息追加能力。
package chatstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrConversationNotFound 表示会话不存在或当前身份无权访问。
	ErrConversationNotFound = errors.New("conversation not found")
	// ErrGroupOwnerRequired 表示群主身份校验失败。
	ErrGroupOwnerRequired = errors.New("group conversation owner required")
)

// GroupAccess 定义群聊操作所需的资格。
type GroupAccess int

const (
	GroupReadable GroupAccess = iota
	GroupSendable
	GroupManageable
)

// Member 保存锁定后的会话及当前成员关系。
type Member struct {
	Conversation  *servermodels.Conversation `bun:"-"`
	ParticipantID string                     `bun:"participant_id"`
	SubjectID     string                     `bun:"subject_id"`
	Role          string                     `bun:"role"`
}

// MemberQuery 限定当前身份仍参与的内部会话，不获取写锁。
func MemberQuery(db bun.IDB, identity *servermodels.Identity, conversationID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("conversations AS cv").
		Join("JOIN conversation_participants AS mine ON mine.organization_id = cv.organization_id AND mine.conversation_id = cv.id AND mine.left_at IS NULL").
		Join("JOIN chat_subjects AS subject ON subject.organization_id = mine.organization_id AND subject.id = mine.subject_id AND subject.kind = ? AND subject.source_id = ?", domain.ChatSubjectKindOrganizationIdentity, identity.OrganizationIdentity.ID).
		Where("cv.organization_id = ? AND cv.id = ?", identity.Organization.ID, conversationID).
		Where("cv.type IN (?, ?, ?)", domain.ConversationTypeDirect, domain.ConversationTypeAgent, domain.ConversationTypeGroup).
		Where("cv.status IN (?, ?)", domain.ConversationStatusActive, domain.ConversationStatusArchived)
}

// GroupQuery 限定当前成员可阅读的活跃或已解散群聊。
func GroupQuery(db bun.IDB, identity *servermodels.Identity, conversationID string) *bun.SelectQuery {
	return MemberQuery(db, identity, conversationID).Where("cv.type = ?", domain.ConversationTypeGroup)
}

// LockConversation 在调用方事务中锁定指定企业的会话。
func LockConversation(ctx context.Context, db bun.IDB, organizationID, conversationID string) (*servermodels.Conversation, error) {
	conversation := &servermodels.Conversation{}
	err := db.NewSelect().Model(conversation).
		Where("cv.organization_id = ? AND cv.id = ?", organizationID, conversationID).
		For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock conversation: %w", err)
	}
	return conversation, nil
}

// LockMember 在调用方事务中先锁会话，再读取并锁定当前成员关系。
func LockMember(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, conversationID string) (Member, error) {
	conversation, err := LockConversation(ctx, tx, identity.Organization.ID, conversationID)
	if err != nil {
		return Member{}, err
	}
	// 取得会话锁后重新查询当前成员资格。
	member := Member{Conversation: conversation}
	err = MemberQuery(tx, identity, conversationID).
		ColumnExpr("mine.id AS participant_id, mine.subject_id, mine.role").
		For("UPDATE OF mine").Scan(ctx, &member)
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, ErrConversationNotFound
	}
	if err != nil {
		return Member{}, fmt.Errorf("lock conversation participant: %w", err)
	}
	return member, nil
}

// LockGroup 锁定群聊与当前成员后校验可读、可发或可管理资格。
func LockGroup(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, conversationID string, access GroupAccess) (Member, error) {
	member, err := LockMember(ctx, tx, identity, conversationID)
	if err != nil {
		return Member{}, err
	}
	if member.Conversation.Type != string(domain.ConversationTypeGroup) ||
		(access != GroupReadable && member.Conversation.Status != string(domain.ConversationStatusActive)) {
		return Member{}, ErrConversationNotFound
	}
	if access == GroupManageable && member.Role != string(domain.ConversationParticipantRoleOwner) {
		return Member{}, ErrGroupOwnerRequired
	}
	return member, nil
}
