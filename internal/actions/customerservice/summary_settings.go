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

const (
	ValidationSummaryModelInvalid  ValidationCode = "SERVICE_SUMMARY_MODEL_INVALID"
	ValidationSummaryLocaleInvalid ValidationCode = "SERVICE_SUMMARY_LOCALE_INVALID"
)

// LoadServiceSummarySettings 读取企业周期小结设置。
func LoadServiceSummarySettings(ctx context.Context, db bun.IDB, organizationID string) (domain.ServiceSummarySettings, error) {
	setting := &servermodels.CustomerServiceSetting{}
	if err := db.NewSelect().Model(setting).
		Column("decision_provider_id", "decision_model_identifier", "summary_provider_id", "summary_model_identifier", "summary_locale").
		Where("css.organization_id = ?", organizationID).
		Scan(ctx); err != nil {
		return domain.ServiceSummarySettings{}, fmt.Errorf("load service summary settings: %w", err)
	}
	settings := domain.ServiceSummarySettings{Locale: domain.Locale(setting.SummaryLocale)}
	if setting.DecisionProviderID != nil && setting.DecisionModelIdentifier != nil {
		settings.Decision = &domain.AIModelReference{ProviderID: *setting.DecisionProviderID, ModelIdentifier: *setting.DecisionModelIdentifier}
	}
	if setting.SummaryProviderID != nil && setting.SummaryModelIdentifier != nil {
		settings.Summary = &domain.AIModelReference{ProviderID: *setting.SummaryProviderID, ModelIdentifier: *setting.SummaryModelIdentifier}
	}
	return settings, nil
}

// GetServiceSummarySettingsQuery 读取当前企业的周期小结设置。
type GetServiceSummarySettingsQuery struct {
	db *bun.DB
}

// NewGetServiceSummarySettingsQuery 创建周期小结设置读取查询。
func NewGetServiceSummarySettingsQuery(db *bun.DB) *GetServiceSummarySettingsQuery {
	return &GetServiceSummarySettingsQuery{db: db}
}

// Execute 返回当前企业的周期小结设置。
func (q *GetServiceSummarySettingsQuery) Execute(ctx context.Context, identity *servermodels.Identity) (domain.ServiceSummarySettings, error) {
	return LoadServiceSummarySettings(ctx, q.db, identity.Organization.ID)
}

// UpdateServiceSummarySettingsAction 修改当前企业的周期小结设置。
type UpdateServiceSummarySettingsAction struct {
	db *bun.DB
}

// NewUpdateServiceSummarySettingsAction 创建周期小结设置修改操作。
func NewUpdateServiceSummarySettingsAction(db *bun.DB) *UpdateServiceSummarySettingsAction {
	return &UpdateServiceSummarySettingsAction{db: db}
}

// Execute 校验并保存周期小结设置：判断模型须为判断用途，小结模型须为支持文本输入的对话模型。
func (a *UpdateServiceSummarySettingsAction) Execute(ctx context.Context, identity *servermodels.Identity, input domain.ServiceSummarySettings) (domain.ServiceSummarySettings, error) {
	if !input.Locale.Valid() {
		return domain.ServiceSummarySettings{}, &ValidationError{Fields: map[string]ValidationCode{"locale": ValidationSummaryLocaleInvalid}}
	}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		fields := make(map[string]ValidationCode)
		for field, reference := range map[string]struct {
			model     *domain.AIModelReference
			modelType domain.AIModelType
		}{
			"decision": {input.Decision, domain.AIModelTypeDecision},
			"summary":  {input.Summary, domain.AIModelTypeChat},
		} {
			if reference.model == nil {
				continue
			}
			query := tx.NewSelect().Model((*servermodels.AIProviderModel)(nil)).
				Where("organization_id = ? AND provider_id = ? AND identifier = ? AND model_type = ?",
					identity.Organization.ID, reference.model.ProviderID, reference.model.ModelIdentifier, reference.modelType)
			// 小结模型须支持文本输入。
			if reference.modelType == domain.AIModelTypeChat {
				query = query.Where("input_modalities @> ?::jsonb", fmt.Sprintf(`[%q]`, domain.AIModelInputModalityText))
			}
			exists, err := query.Exists(ctx)
			if err != nil {
				return fmt.Errorf("check service summary model: %w", err)
			}
			if !exists {
				fields[field] = ValidationSummaryModelInvalid
			}
		}
		if len(fields) > 0 {
			return &ValidationError{Fields: fields}
		}
		setting := &servermodels.CustomerServiceSetting{OrganizationID: identity.Organization.ID, SummaryLocale: string(input.Locale)}
		if input.Decision != nil {
			setting.DecisionProviderID, setting.DecisionModelIdentifier = &input.Decision.ProviderID, &input.Decision.ModelIdentifier
		}
		if input.Summary != nil {
			setting.SummaryProviderID, setting.SummaryModelIdentifier = &input.Summary.ProviderID, &input.Summary.ModelIdentifier
		}
		if err := saveSetting(ctx, tx, setting, "decision_provider_id", "decision_model_identifier", "summary_provider_id", "summary_model_identifier", "summary_locale"); err != nil {
			return fmt.Errorf("save service summary settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.ServiceSummarySettings{}, err
	}
	return input, nil
}
