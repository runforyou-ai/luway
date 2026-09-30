//go:build server

// Package translation 翻译客户会话消息与客服回复，并维护客户语言与回复语言。
package translation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	customerserviceaction "github.com/runforyou-ai/cervi/internal/actions/customerservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/pkg/languagetag"
	"github.com/uptrace/bun"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// ValidationCode 标识翻译请求的校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationMessageIDsInvalid ValidationCode = "TRANSLATION_MESSAGE_IDS_INVALID"
	ValidationBodyRequired      ValidationCode = "TRANSLATION_BODY_REQUIRED"
	ValidationBodyTooLong       ValidationCode = "TRANSLATION_BODY_TOO_LONG"
	ValidationLanguageInvalid   ValidationCode = "TRANSLATION_LANGUAGE_INVALID"
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

const (
	// maxMessageIDs 是一次翻译请求的消息数量上限。
	maxMessageIDs = 50
	// maxReplyRunes 是翻译发送的回复字符数上限，与对客消息上限一致。
	maxReplyRunes = 4000
)

// Translator 以企业翻译模型执行单次模型调用完成翻译。
type Translator struct {
	db     *bun.DB
	caller agentruntime.SingleCaller
}

// NewTranslator 创建客户会话翻译器。
func NewTranslator(db *bun.DB, caller agentruntime.SingleCaller) *Translator {
	return &Translator{db: db, caller: caller}
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

// loadModel 读取企业翻译模型的调用配置，未设置或模型已不存在时返回 ErrDisabled。
func loadModel(ctx context.Context, db bun.IDB, organizationID string) (agentruntime.ModelConfig, error) {
	reference, err := customerserviceaction.LoadTranslationModel(ctx, db, organizationID)
	if err != nil {
		return agentruntime.ModelConfig{}, err
	}
	if reference == nil {
		return agentruntime.ModelConfig{}, ErrDisabled
	}
	credential, err := customerserviceaction.LoadModel(ctx, db, organizationID, reference, domain.AIModelTypeChat)
	if err != nil {
		return agentruntime.ModelConfig{}, fmt.Errorf("load translation model: %w", err)
	}
	if credential == nil {
		return agentruntime.ModelConfig{}, ErrDisabled
	}
	return credential.ModelConfig(), nil
}

// callJSON 以单次模型调用执行翻译指令，并把正文中的 JSON 对象解析到 target。
func callJSON(ctx context.Context, caller agentruntime.SingleCaller, model agentruntime.ModelConfig, instruction string, input any, target any) error {
	encoded, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode translation input: %w", err)
	}
	response, err := caller.CallOnce(ctx, agentruntime.SingleCallRequest{Instruction: instruction, Model: model, Input: string(encoded)})
	if err != nil {
		return fmt.Errorf("call translation model: %w", err)
	}
	if err := agentruntime.DecodeJSONObject(response.Text, target); err != nil {
		return fmt.Errorf("decode translation response: %w", err)
	}
	return nil
}

// normalizedLanguage 规范化模型返回的语言标签，无效时视为无语言内容。
func normalizedLanguage(value string) string {
	if normalized, ok := languagetag.Normalize(value); ok {
		return normalized
	}
	return languagetag.Undetermined
}

// authorizeConversation 确认客户会话属于当前企业。
func authorizeConversation(ctx context.Context, db bun.IDB, organizationID, conversationID string) error {
	exists, err := db.NewSelect().Model((*servermodels.ChannelConversation)(nil)).
		Where("organization_id = ? AND conversation_id = ?", organizationID, conversationID).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check customer conversation: %w", err)
	}
	if !exists {
		return ErrConversationNotFound
	}
	return nil
}
