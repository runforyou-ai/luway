//go:build server

package customerservice

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ValidationTranslationModelInvalid 表示翻译模型不存在或不是支持文本输入的对话模型。
const ValidationTranslationModelInvalid ValidationCode = "TRANSLATION_MODEL_INVALID"

// LoadTranslationModel 读取企业设置的翻译模型，未设置时返回 nil。
func LoadTranslationModel(ctx context.Context, db bun.IDB, organizationID string) (*domain.AIModelReference, error) {
	setting := &servermodels.CustomerServiceSetting{}
	if err := db.NewSelect().Model(setting).
		Column("translation_provider_id", "translation_model_identifier").
		Where("css.organization_id = ?", organizationID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load translation model: %w", err)
	}
	if setting.TranslationProviderID == nil || setting.TranslationModelIdentifier == nil {
		return nil, nil
	}
	return &domain.AIModelReference{ProviderID: *setting.TranslationProviderID, ModelIdentifier: *setting.TranslationModelIdentifier}, nil
}

// GetTranslationSettingsQuery 读取当前企业的翻译设置。
type GetTranslationSettingsQuery struct {
	db *bun.DB
}

// NewGetTranslationSettingsQuery 创建翻译设置读取查询。
func NewGetTranslationSettingsQuery(db *bun.DB) *GetTranslationSettingsQuery {
	return &GetTranslationSettingsQuery{db: db}
}

// Execute 返回当前企业的翻译模型，未设置时为 nil。
func (q *GetTranslationSettingsQuery) Execute(ctx context.Context, identity *servermodels.Identity) (*domain.AIModelReference, error) {
	return LoadTranslationModel(ctx, q.db, identity.Organization.ID)
}

// UpdateTranslationSettingsAction 修改当前企业的翻译设置。
type UpdateTranslationSettingsAction struct {
	db *bun.DB
}

// NewUpdateTranslationSettingsAction 创建翻译设置修改操作。
func NewUpdateTranslationSettingsAction(db *bun.DB) *UpdateTranslationSettingsAction {
	return &UpdateTranslationSettingsAction{db: db}
}

// Execute 校验并保存翻译模型：模型须为支持文本输入的对话模型，nil 表示关闭翻译。
func (a *UpdateTranslationSettingsAction) Execute(ctx context.Context, identity *servermodels.Identity, model *domain.AIModelReference) (*domain.AIModelReference, error) {
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		setting := &servermodels.CustomerServiceSetting{OrganizationID: identity.Organization.ID}
		if model != nil {
			exists, err := tx.NewSelect().Model((*servermodels.AIProviderModel)(nil)).
				Where("organization_id = ? AND provider_id = ? AND identifier = ? AND model_type = ?",
					identity.Organization.ID, model.ProviderID, model.ModelIdentifier, domain.AIModelTypeChat).
				Where("input_modalities @> ?::jsonb", fmt.Sprintf(`[%q]`, domain.AIModelInputModalityText)).
				Exists(ctx)
			if err != nil {
				return fmt.Errorf("check translation model: %w", err)
			}
			if !exists {
				return &ValidationError{Fields: map[string]ValidationCode{"model": ValidationTranslationModelInvalid}}
			}
			setting.TranslationProviderID, setting.TranslationModelIdentifier = &model.ProviderID, &model.ModelIdentifier
		}
		if err := saveSetting(ctx, tx, setting, "translation_provider_id", "translation_model_identifier"); err != nil {
			return fmt.Errorf("save translation settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return model, nil
}
