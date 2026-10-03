//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	creditaction "github.com/runforyou-ai/luway/internal/actions/credit"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// creditOps 持有工作区积分与平台积分管理的 Action 和 Query。
type creditOps struct {
	creditDB               *bun.DB
	updateDailyCreditGrant *platformaction.UpdateDailyCreditGrantAction
	workspaceCredits       *platformaction.WorkspaceCreditsQuery
	adjustWorkspaceCredits *platformaction.AdjustWorkspaceCreditsAction
}

// newCreditOps 创建积分的业务实现依赖。
func newCreditOps(db *bun.DB) creditOps {
	return creditOps{
		creditDB:               db,
		updateDailyCreditGrant: platformaction.NewUpdateDailyCreditGrantAction(db),
		workspaceCredits:       platformaction.NewWorkspaceCreditsQuery(db),
		adjustWorkspaceCredits: platformaction.NewAdjustWorkspaceCreditsAction(db),
	}
}

// GetCreditBalance 返回当前工作区的可用积分与今天的每日赠送。
func (o *directOperations) GetCreditBalance(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.CreditBalance, error) {
	balance, err := creditaction.GetBalance(ctx, o.creditDB, identity.Organization.ID)
	if err != nil {
		return appservice.CreditBalance{}, creditError(ctx, meta, err, i18n.ErrorCreditBalanceReadFailed, "organization_id", identity.Organization.ID)
	}
	return creditBalanceFromAction(balance), nil
}

// ListCreditEntries 返回当前工作区的积分流水。
func (o *directOperations) ListCreditEntries(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.CreditEntryListInput) (appservice.CreditEntryList, error) {
	list, err := creditaction.ListEntries(ctx, o.creditDB, identity.Organization.ID, input.Page, input.PageSize)
	if err != nil {
		return appservice.CreditEntryList{}, creditError(ctx, meta, err, i18n.ErrorCreditEntryListFailed, "organization_id", identity.Organization.ID)
	}
	return creditEntryListFromAction(list), nil
}

// UpdatePlatformDailyCreditGrant 修改每个工作区每天赠送的积分。
func (o *directOperations) UpdatePlatformDailyCreditGrant(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformDailyCreditGrantInput) (appservice.PlatformSettings, error) {
	settings, err := o.updateDailyCreditGrant.Execute(ctx, account, input.DailyCreditGrant)
	if err != nil {
		return appservice.PlatformSettings{}, creditError(ctx, meta, err, i18n.ErrorPlatformSettingsUpdateFailed, "account_id", account.Account.ID)
	}
	slog.Info("每日赠送积分已修改", "account_id", account.Account.ID, "daily_credit_grant", settings.DailyCreditGrant)
	return platformSettingsFromAction(settings), nil
}

// GetPlatformWorkspaceCredits 返回工作区的可用积分与今天的每日赠送。
func (o *directOperations) GetPlatformWorkspaceCredits(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string) (appservice.CreditBalance, error) {
	balance, err := o.workspaceCredits.Balance(ctx, workspaceID)
	if err != nil {
		return appservice.CreditBalance{}, creditError(ctx, meta, err, i18n.ErrorCreditBalanceReadFailed, "account_id", account.Account.ID, "workspace_id", workspaceID)
	}
	return creditBalanceFromAction(balance), nil
}

// ListPlatformWorkspaceCreditEntries 返回工作区的积分流水。
func (o *directOperations) ListPlatformWorkspaceCreditEntries(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string, input appservice.CreditEntryListInput) (appservice.CreditEntryList, error) {
	list, err := o.workspaceCredits.Entries(ctx, workspaceID, input.Page, input.PageSize)
	if err != nil {
		return appservice.CreditEntryList{}, creditError(ctx, meta, err, i18n.ErrorCreditEntryListFailed, "account_id", account.Account.ID, "workspace_id", workspaceID)
	}
	return creditEntryListFromAction(list), nil
}

// AdjustPlatformWorkspaceCredits 手动增加或扣减工作区积分，扣减最多扣到余额为 0。
func (o *directOperations) AdjustPlatformWorkspaceCredits(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string, input appservice.PlatformCreditAdjustmentInput) (appservice.PlatformCreditAdjustment, error) {
	applied, err := o.adjustWorkspaceCredits.Execute(ctx, account, workspaceID, input.Amount, input.Note)
	if errors.Is(err, creditaction.ErrInsufficient) {
		return appservice.PlatformCreditAdjustment{}, appservice.InvalidError(meta, i18n.ErrorPlatformCreditNothingToDeduct, nil)
	}
	if err != nil {
		return appservice.PlatformCreditAdjustment{}, creditError(ctx, meta, err, i18n.ErrorPlatformCreditAdjustFailed, "account_id", account.Account.ID, "workspace_id", workspaceID)
	}
	slog.Info("工作区积分已调整", "account_id", account.Account.ID, "workspace_id", workspaceID, "amount", applied)
	balance, err := o.workspaceCredits.Balance(ctx, workspaceID)
	if err != nil {
		return appservice.PlatformCreditAdjustment{}, creditError(ctx, meta, err, i18n.ErrorCreditBalanceReadFailed, "account_id", account.Account.ID, "workspace_id", workspaceID)
	}
	return appservice.PlatformCreditAdjustment{Amount: applied, Balance: creditBalanceFromAction(balance)}, nil
}

// creditError 把积分操作的错误转换为本地化业务错误。
func creditError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, attributes ...any) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		// 把积分校验错误码映射为本地化文案键。
		keys := map[common.FieldCode]i18n.Key{
			platformaction.ValidationDailyCreditGrantInvalid: i18n.FieldDailyCreditGrantInvalid,
			platformaction.ValidationCreditAmountInvalid:     i18n.FieldCreditAmountInvalid,
			platformaction.ValidationCreditNoteInvalid:       i18n.FieldCreditNoteInvalid,
		}
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
	}
	switch {
	case errors.Is(err, creditaction.ErrPageInvalid):
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	case errors.Is(err, platformaction.ErrNotPlatformAdmin):
		return appservice.ForbiddenError(meta, i18n.ErrorPlatformAdminRequired)
	case errors.Is(err, platformaction.ErrWorkspaceNotFound):
		return appservice.NotFoundError(meta, i18n.ErrorPlatformWorkspaceNotFound)
	}
	slog.Warn("积分操作失败", append(attributes, "failure", failureKey, "error", err)...)
	return appservice.FailedError(meta, failureKey)
}

// creditBalanceFromAction 把积分余额转换为应用契约。
func creditBalanceFromAction(balance creditaction.Balance) appservice.CreditBalance {
	return appservice.CreditBalance{
		Available: balance.Available, DailyGrant: balance.DailyGrant,
		DailyGrantRemaining: balance.DailyGrantRemaining, DailyGrantExpiresAt: balance.DailyGrantExpiresAt,
	}
}

// creditEntryListFromAction 把积分流水分页结果转换为应用契约。
func creditEntryListFromAction(list creditaction.EntryList) appservice.CreditEntryList {
	entries := make([]appservice.CreditEntry, 0, len(list.Entries))
	for _, entry := range list.Entries {
		entries = append(entries, appservice.CreditEntry{
			ID: entry.ID, Kind: appservice.CreditEntryKind(entry.Kind), OccurredAt: entry.OccurredAt, Amount: entry.Amount, Note: entry.Note,
			ModelName: entry.ModelName, ModelUsage: appservice.AIModelUsage(entry.ModelUsage), CallStatus: appservice.AIModelCallStatus(entry.CallStatus),
			InputTokens: entry.InputTokens, OutputTokens: entry.OutputTokens,
		})
	}
	return appservice.CreditEntryList{Entries: entries, Page: appservice.PageInfo(list.Page)}
}

// creditPriceFromDomain 把积分价格转换为应用契约，未定价时为空。
func creditPriceFromDomain(price *domain.CreditPrice) *appservice.CreditPrice {
	if price == nil {
		return nil
	}
	return &appservice.CreditPrice{Input: price.Input, Output: price.Output, Request: price.Request}
}
