//go:build server

// Package translation 翻译客户会话消息与客服回复，并维护客户语言与回复语言。
package translation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/languagetag"
	"github.com/uptrace/bun"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// ValidationCode 标识翻译请求的校验结果。
type ValidationCode = common.FieldCode

// 翻译请求的字段校验码。
const (
	ValidationLanguageInvalid ValidationCode = "TRANSLATION_LANGUAGE_INVALID"
)

// ValidationError 表示翻译请求校验失败。
type ValidationError = common.FieldError

var (
	// ErrDisabled 表示企业未设置翻译模型。
	ErrDisabled = errors.New("translation model is not configured")
	// ErrConversationNotFound 表示客户会话不存在或不属于当前企业。
	ErrConversationNotFound = errors.New("customer conversation not found")
	// ErrCustomerLanguageUnknown 表示尚无法确定客户使用的语言。
	ErrCustomerLanguageUnknown = errors.New("customer language is unknown")
	// ErrReplyLanguageChanged 表示译文语言与当前回复语言不一致。
	ErrReplyLanguageChanged = errors.New("reply language changed")
)

// Translator 以企业翻译模型执行单次模型调用完成翻译。
type Translator struct {
	db      *bun.DB
	invoker *modelcall.Invoker
}

// NewTranslator 创建客户会话翻译器。
func NewTranslator(db *bun.DB, invoker *modelcall.Invoker) *Translator {
	return &Translator{db: db, invoker: invoker}
}

// ViewerLanguage 返回成员阅读译文和书写回复使用的语言：设置了翻译语言时取翻译语言，否则取账号界面语言。
func ViewerLanguage(identity *servermodels.Identity) string {
	if identity.User.TranslationLanguage != nil {
		return *identity.User.TranslationLanguage
	}
	return identity.Account.Locale
}

// LanguageName 返回写入模型指令的语言说明，包含标签与英文名称。
func LanguageName(tag string) string {
	parsed, err := language.Parse(tag)
	if err != nil {
		return tag
	}
	if name := display.English.Tags().Name(parsed); name != "" {
		return tag + "（" + name + "）"
	}
	return tag
}

// loadModel 读取企业翻译模型的调用配置，调用由成员在会话中发起；未设置或模型已不可用时返回 ErrDisabled。
func (t *Translator) loadModel(ctx context.Context, identity *servermodels.Identity, conversationID string) (agentruntime.ModelConfig, error) {
	workspaceID := identity.Workspace.ID
	modelID, err := customerserviceaction.LoadTranslationModelID(ctx, t.db, workspaceID)
	if err != nil {
		return agentruntime.ModelConfig{}, err
	}
	model, err := customerserviceaction.LoadModel(ctx, t.db, workspaceID, modelID, domain.AIModelUsageTranslation)
	if err != nil {
		return agentruntime.ModelConfig{}, fmt.Errorf("load translation model: %w", err)
	}
	if model == nil {
		return agentruntime.ModelConfig{}, ErrDisabled
	}
	return t.invoker.ModelConfig(modelcall.MemberScope(identity, domain.AIModelCallSourceConversation, conversationID), model), nil
}

// callJSON 以单次模型调用执行翻译指令，返回按 T 的结构解析的输出。
func callJSON[T any](ctx context.Context, model agentruntime.ModelConfig, instruction string, input any) (T, error) {
	var zero T
	encoded, err := json.Marshal(input)
	if err != nil {
		return zero, fmt.Errorf("encode translation input: %w", err)
	}
	output, _, err := agentruntime.GenerateObject[T](ctx, model, instruction, string(encoded))
	if err != nil {
		return zero, fmt.Errorf("call translation model: %w", err)
	}
	return output, nil
}

// normalizedLanguage 规范化模型返回的语言标签，无效时视为无语言内容。
func normalizedLanguage(value string) string {
	if normalized, ok := languagetag.Normalize(value); ok {
		return normalized
	}
	return languagetag.Undetermined
}

// authorizeConversation 确认客户会话属于当前企业。
func authorizeConversation(ctx context.Context, db bun.IDB, workspaceID, conversationID string) error {
	exists, err := db.NewSelect().Model((*servermodels.ChannelConversation)(nil)).
		Where("workspace_id = ? AND conversation_id = ?", workspaceID, conversationID).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check customer conversation: %w", err)
	}
	if !exists {
		return ErrConversationNotFound
	}
	return nil
}
