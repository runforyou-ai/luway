//go:build server

package customerservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	ValidationSummaryModelInvalid ValidationCode = "SERVICE_SUMMARY_MODEL_INVALID"
)

// LoadServiceSummarySettings 读取企业周期小结设置。
func LoadServiceSummarySettings(ctx context.Context, db bun.IDB, workspaceID string) (domain.ServiceSummarySettings, error) {
	setting := &servermodels.CustomerServiceSetting{}
	if err := db.NewSelect().Model(setting).
		Column("decision_model_id", "summary_model_id", "summary_locale").
		Where("css.workspace_id = ?", workspaceID).
		Scan(ctx); err != nil {
		return domain.ServiceSummarySettings{}, fmt.Errorf("load service summary settings: %w", err)
	}
	return domain.ServiceSummarySettings{
		DecisionModelID: setting.DecisionModelID, SummaryModelID: setting.SummaryModelID, Locale: domain.Locale(setting.SummaryLocale),
	}, nil
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
	return LoadServiceSummarySettings(ctx, q.db, identity.Workspace.ID)
}

// UpdateServiceSummarySettingsAction 修改当前企业的周期小结设置。
type UpdateServiceSummarySettingsAction struct {
	db *bun.DB
}

// NewUpdateServiceSummarySettingsAction 创建周期小结设置修改操作。
func NewUpdateServiceSummarySettingsAction(db *bun.DB) *UpdateServiceSummarySettingsAction {
	return &UpdateServiceSummarySettingsAction{db: db}
}

// Execute 校验并保存周期小结设置：判断模型须满足判断用途，小结模型须满足小结用途。
func (a *UpdateServiceSummarySettingsAction) Execute(ctx context.Context, identity *servermodels.Identity, input domain.ServiceSummarySettings) (domain.ServiceSummarySettings, error) {
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		fields := make(map[string]ValidationCode)
		for _, reference := range []struct {
			field   string
			modelID *string
			usage   domain.AIModelUsage
		}{
			{"decisionModelId", input.DecisionModelID, domain.AIModelUsageDecision},
			{"summaryModelId", input.SummaryModelID, domain.AIModelUsageSummary},
		} {
			if reference.modelID == nil {
				continue
			}
			_, err := aimodel.Lock(ctx, tx, identity.Workspace.ID, *reference.modelID, reference.usage)
			if errors.Is(err, aimodel.ErrUnavailable) {
				fields[reference.field] = ValidationSummaryModelInvalid
			} else if err != nil {
				return fmt.Errorf("check service summary model: %w", err)
			}
		}
		if len(fields) > 0 {
			return &ValidationError{Fields: fields}
		}
		setting := &servermodels.CustomerServiceSetting{
			WorkspaceID: identity.Workspace.ID, SummaryLocale: string(input.Locale),
			DecisionModelID: input.DecisionModelID, SummaryModelID: input.SummaryModelID,
		}
		if err := saveSetting(ctx, tx, setting, "decision_model_id", "summary_model_id", "summary_locale"); err != nil {
			return fmt.Errorf("save service summary settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.ServiceSummarySettings{}, err
	}
	return input, nil
}
