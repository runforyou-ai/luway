//go:build server

package translation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	customerserviceaction "github.com/runforyou-ai/cervi/internal/actions/customerservice"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/pkg/languagetag"
	"github.com/uptrace/bun"
)

// ConversationState 是当前成员在客户会话中的翻译状态。
type ConversationState struct {
	// Enabled 表示企业已设置翻译模型。
	Enabled bool
	// ViewerLanguage 是当前成员的阅读与书写语言。
	ViewerLanguage string
	// CustomerLanguage 是对客回复使用的客户语言，尚无法确定时为空。
	CustomerLanguage string
	// ReplyLanguageLocked 表示客户语言由客服手动锁定。
	ReplyLanguageLocked bool
}

// customerLanguage 是客户语言的各项来源。
type customerLanguage struct {
	Locked   *string `bun:"locked"`
	Detected *string `bun:"detected"`
	Browser  *string `bun:"browser"`
}

// resolve 按锁定的回复语言、客户最近消息的语言、访客浏览器语言的顺序确定客户语言。
func (l customerLanguage) resolve() (string, bool) {
	if l.Locked != nil {
		return *l.Locked, true
	}
	if l.Detected != nil {
		return *l.Detected, false
	}
	if l.Browser != nil {
		if normalized, ok := languagetag.Normalize(*l.Browser); ok {
			return normalized, false
		}
	}
	return "", false
}

// loadCustomerLanguage 读取客户会话的客户语言来源。
func loadCustomerLanguage(ctx context.Context, db bun.IDB, organizationID, conversationID string) (customerLanguage, error) {
	var result customerLanguage
	err := db.NewSelect().
		TableExpr("channel_conversations AS cc").
		ColumnExpr("cc.reply_language AS locked").
		ColumnExpr(`(SELECT msg.language FROM messages AS msg
			JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.organization_id = msg.organization_id AND cp.conversation_id = msg.conversation_id
			JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.organization_id = cp.organization_id
			WHERE msg.organization_id = cc.organization_id AND msg.conversation_id = cc.conversation_id AND cs.kind = ?
				AND msg.language IS NOT NULL AND msg.language <> ? AND msg.deleted_at IS NULL
			ORDER BY msg.message_seq DESC LIMIT 1) AS detected`, domain.ChatSubjectKindContact, languagetag.Undetermined).
		ColumnExpr(`(SELECT ss.visitor_context->>'language' FROM service_sessions AS ss
			WHERE ss.organization_id = cc.organization_id AND ss.conversation_id = cc.conversation_id AND ss.visitor_context->>'language' <> ''
			ORDER BY ss.sequence DESC LIMIT 1) AS browser`).
		Where("cc.organization_id = ? AND cc.conversation_id = ?", organizationID, conversationID).
		Scan(ctx, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return customerLanguage{}, ErrConversationNotFound
	}
	if err != nil {
		return customerLanguage{}, fmt.Errorf("load customer language: %w", err)
	}
	return result, nil
}

// ConversationState 返回当前成员在客户会话中的翻译状态。
func (t *Translator) ConversationState(ctx context.Context, identity *servermodels.Identity, conversationID string) (ConversationState, error) {
	conversationID, valid := common.NormalizeUUID(conversationID)
	if !valid {
		return ConversationState{}, ErrConversationNotFound
	}
	return conversationState(ctx, t.db, identity, conversationID)
}

// conversationState 读取翻译模型设置与客户语言，组合为成员的翻译状态。
func conversationState(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversationID string) (ConversationState, error) {
	sources, err := loadCustomerLanguage(ctx, db, identity.Organization.ID, conversationID)
	if err != nil {
		return ConversationState{}, err
	}
	model, err := customerserviceaction.LoadTranslationModel(ctx, db, identity.Organization.ID)
	if err != nil {
		return ConversationState{}, err
	}
	language, locked := sources.resolve()
	return ConversationState{
		Enabled: model != nil, ViewerLanguage: ViewerLanguage(identity),
		CustomerLanguage: language, ReplyLanguageLocked: locked,
	}, nil
}

// SetReplyLanguage 锁定客户会话的对客回复语言，language 为空时恢复按客户最近消息的语言回复。
func (t *Translator) SetReplyLanguage(ctx context.Context, identity *servermodels.Identity, conversationID, language string) (ConversationState, error) {
	conversationID, valid := common.NormalizeUUID(conversationID)
	if !valid {
		return ConversationState{}, ErrConversationNotFound
	}
	var locked *string
	if language != "" {
		normalized, ok := languagetag.Normalize(language)
		if !ok || normalized == languagetag.Undetermined {
			return ConversationState{}, &ValidationError{Fields: map[string]ValidationCode{"language": ValidationLanguageInvalid}}
		}
		locked = &normalized
	}
	var state ConversationState
	err := t.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewUpdate().Model((*servermodels.ChannelConversation)(nil)).
			Set("reply_language = ?", locked).
			Set("updated_at = now()").
			Where("organization_id = ? AND conversation_id = ?", identity.Organization.ID, conversationID).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("save reply language: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected == 0 {
			return ErrConversationNotFound
		}
		state, err = conversationState(ctx, tx, identity, conversationID)
		return err
	})
	if err != nil {
		return ConversationState{}, err
	}
	return state, nil
}

// CustomerLocale 返回与客户语言主语言一致的对客语言，用于发给客户的系统话术，简体与繁体中文都取中文；客户语言未知或不在对客语言中时返回 fallback。
func CustomerLocale(ctx context.Context, db bun.IDB, organizationID, conversationID string, fallback domain.CustomerLocale) (domain.CustomerLocale, error) {
	sources, err := loadCustomerLanguage(ctx, db, organizationID, conversationID)
	if err != nil {
		return "", err
	}
	language, _ := sources.resolve()
	if locale, ok := i18n.MatchCustomerLocale(language); ok {
		return locale, nil
	}
	return fallback, nil
}
