//go:build server

package customerservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ValidationTranslationModelInvalid 表示翻译模型不可用或不满足翻译用途。
const ValidationTranslationModelInvalid ValidationCode = "TRANSLATION_MODEL_INVALID"

// LoadTranslationModelID 读取企业设置的翻译模型编号，未设置时返回 nil。
func LoadTranslationModelID(ctx context.Context, db bun.IDB, organizationID string) (*string, error) {
	setting := &servermodels.CustomerServiceSetting{}
	if err := db.NewSelect().Model(setting).
		Column("translation_model_id").
		Where("css.organization_id = ?", organizationID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load translation model: %w", err)
	}
	return setting.TranslationModelID, nil
}

// GetTranslationSettingsQuery 读取当前企业的翻译设置。
type GetTranslationSettingsQuery struct {
	db *bun.DB
}

// NewGetTranslationSettingsQuery 创建翻译设置读取查询。
func NewGetTranslationSettingsQuery(db *bun.DB) *GetTranslationSettingsQuery {
	return &GetTranslationSettingsQuery{db: db}
}

// Execute 返回当前企业的翻译模型编号，未设置时为 nil。
func (q *GetTranslationSettingsQuery) Execute(ctx context.Context, identity *servermodels.Identity) (*string, error) {
	return LoadTranslationModelID(ctx, q.db, identity.Organization.ID)
}

// UpdateTranslationSettingsAction 修改当前企业的翻译设置。
type UpdateTranslationSettingsAction struct {
	db *bun.DB
}

// NewUpdateTranslationSettingsAction 创建翻译设置修改操作。
func NewUpdateTranslationSettingsAction(db *bun.DB) *UpdateTranslationSettingsAction {
	return &UpdateTranslationSettingsAction{db: db}
}

// Execute 校验并保存翻译模型编号：模型须满足翻译用途，nil 表示关闭翻译。
func (a *UpdateTranslationSettingsAction) Execute(ctx context.Context, identity *servermodels.Identity, modelID *string) (*string, error) {
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		setting := &servermodels.CustomerServiceSetting{OrganizationID: identity.Organization.ID}
		if modelID != nil {
			_, err := aimodel.Lock(ctx, tx, identity.Organization.ID, *modelID, domain.AIModelUsageTranslation)
			if errors.Is(err, aimodel.ErrUnavailable) {
				return &ValidationError{Fields: map[string]ValidationCode{"modelId": ValidationTranslationModelInvalid}}
			}
			if err != nil {
				return fmt.Errorf("check translation model: %w", err)
			}
			setting.TranslationModelID = modelID
		}
		if err := saveSetting(ctx, tx, setting, "translation_model_id"); err != nil {
			return fmt.Errorf("save translation settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return modelID, nil
}
