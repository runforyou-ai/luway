//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	servicecategoryaction "github.com/runforyou-ai/luway/internal/actions/servicecategory"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// customerServiceOps 持有企业客服设置的业务实现依赖。
type customerServiceOps struct {
	getBusinessHours      *customerserviceaction.GetBusinessHoursQuery
	updateBusinessHours   *customerserviceaction.UpdateBusinessHoursAction
	getServiceTimeouts    *customerserviceaction.GetServiceTimeoutsQuery
	updateServiceTimeouts *customerserviceaction.UpdateServiceTimeoutsAction
	listCategories        *servicecategoryaction.ListQuery
	createCategory        *servicecategoryaction.CreateAction
	updateCategory        *servicecategoryaction.UpdateAction
	archiveCategory       *servicecategoryaction.ArchiveAction
	getIdentitySecret     *customerserviceaction.GetCustomerIdentitySecretQuery
	regenerateSecret      *customerserviceaction.RegenerateCustomerIdentitySecretAction
	getRequesterProfile   *contactaction.GetCustomerProfileQuery
	listBusinessQueries   *conversationaction.ListBusinessQueriesQuery
	getSummarySettings    *customerserviceaction.GetServiceSummarySettingsQuery
	updateSummarySettings *customerserviceaction.UpdateServiceSummarySettingsAction
	listSummaries         *servicesessionaction.ListServiceSummariesQuery
	updateSummary         *servicesessionaction.UpdateServiceSessionSummaryAction
}

// newCustomerServiceOps 创建企业客服设置的业务实现依赖。
func newCustomerServiceOps(db *bun.DB) *customerServiceOps {
	return &customerServiceOps{
		getBusinessHours:      customerserviceaction.NewGetBusinessHoursQuery(db),
		updateBusinessHours:   customerserviceaction.NewUpdateBusinessHoursAction(db),
		getServiceTimeouts:    customerserviceaction.NewGetServiceTimeoutsQuery(db),
		updateServiceTimeouts: customerserviceaction.NewUpdateServiceTimeoutsAction(db),
		listCategories:        servicecategoryaction.NewListQuery(db),
		createCategory:        servicecategoryaction.NewCreateAction(db),
		updateCategory:        servicecategoryaction.NewUpdateAction(db),
		archiveCategory:       servicecategoryaction.NewArchiveAction(db),
		getIdentitySecret:     customerserviceaction.NewGetCustomerIdentitySecretQuery(db),
		regenerateSecret:      customerserviceaction.NewRegenerateCustomerIdentitySecretAction(db),
		getRequesterProfile:   contactaction.NewGetCustomerProfileQuery(db),
		listBusinessQueries:   conversationaction.NewListBusinessQueriesQuery(db),
		getSummarySettings:    customerserviceaction.NewGetServiceSummarySettingsQuery(db),
		updateSummarySettings: customerserviceaction.NewUpdateServiceSummarySettingsAction(db),
		listSummaries:         servicesessionaction.NewListServiceSummariesQuery(db),
		updateSummary:         servicesessionaction.NewUpdateServiceSessionSummaryAction(db),
	}
}

// GetCustomerIdentitySecret 读取当前企业的客户身份密钥，未生成时为空。
func (o *customerServiceOps) GetCustomerIdentitySecret(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.CustomerIdentitySecret, error) {
	secret, err := o.getIdentitySecret.Execute(ctx, identity)
	if err != nil {
		return appservice.CustomerIdentitySecret{}, appservice.FailedError(meta, i18n.ErrorCustomerIdentitySecretLoadFailed, err)
	}
	return appservice.CustomerIdentitySecret{Secret: secret}, nil
}

// RegenerateCustomerIdentitySecret 生成或重新生成当前企业的客户身份密钥，旧密钥立即失效。
func (o *customerServiceOps) RegenerateCustomerIdentitySecret(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.CustomerIdentitySecret, error) {
	secret, err := o.regenerateSecret.Execute(ctx, identity)
	if err != nil {
		if errors.Is(err, identityaction.ErrInvalid) {
			return appservice.CustomerIdentitySecret{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
		}
		return appservice.CustomerIdentitySecret{}, appservice.FailedError(meta, i18n.ErrorCustomerIdentitySecretRegenerateFailed, err)
	}
	slog.InfoContext(ctx, "客户身份密钥已重新生成", "user_id", identity.User.ID)
	return appservice.CustomerIdentitySecret{Secret: secret}, nil
}

// GetRequesterProfile 返回服务会话发起人的资料；发起人是客户时给出客户身份与当前周期访客上下文。
func (o *customerServiceOps) GetRequesterProfile(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.RequesterProfile, error) {
	profile, err := o.getRequesterProfile.Execute(ctx, identity, conversationID)
	if err != nil {
		if errors.Is(err, contactaction.ErrNotFound) {
			return appservice.RequesterProfile{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
		}
		return appservice.RequesterProfile{}, appservice.FailedError(meta, i18n.ErrorCustomerProfileLoadFailed, err)
	}
	result := appservice.CustomerProfile{ContactID: profile.ContactID, IdentityVerified: profile.IdentityVerified, ExternalUserID: profile.ExternalUserID, Email: profile.Email}
	if visit := profile.VisitorContext; visit != nil {
		result.Visit = &appservice.CustomerVisit{
			ReferrerURL: visit.ReferrerURL, PageURL: visit.PageURL, PageTitle: visit.PageTitle, Browser: visit.Browser, OS: visit.OS,
			DeviceType: visit.DeviceType, Language: visit.Language, TimeZone: visit.TimeZone, Country: visit.Country,
		}
	}
	return appservice.RequesterProfile{Customer: &result}, nil
}

// ListServiceBusinessQueries 返回服务会话当前服务周期内 AI 员工查询业务系统的记录。
func (o *customerServiceOps) ListServiceBusinessQueries(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ServiceBusinessQueryList, error) {
	queries, err := o.listBusinessQueries.Execute(ctx, identity, conversationID)
	if err != nil {
		if errors.Is(err, conversationaction.ErrConversationNotFound) {
			return appservice.ServiceBusinessQueryList{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
		}
		return appservice.ServiceBusinessQueryList{}, appservice.FailedError(meta, i18n.ErrorBusinessQueriesLoadFailed, err)
	}
	result := appservice.ServiceBusinessQueryList{Queries: make([]appservice.ServiceBusinessQuery, 0, len(queries))}
	for _, query := range queries {
		call := query.ToolCall
		result.Queries = append(result.Queries, appservice.ServiceBusinessQuery{
			ID: query.ID, BusinessSystem: call.BusinessSystem, Level: call.Level, ToolName: call.Name, Arguments: call.Arguments, BoundArguments: call.BoundArguments, Result: call.Result, Error: call.Error,
			Status: call.Status, Evidence: call.Evidence, CalledAt: query.CalledAt,
		})
	}
	return result, nil
}

// GetBusinessHours 读取当前企业的客服工作时间。
func (o *customerServiceOps) GetBusinessHours(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.BusinessHours, error) {
	hours, err := o.getBusinessHours.Execute(ctx, identity)
	if err != nil {
		return appservice.BusinessHours{}, appservice.FailedError(meta, i18n.ErrorBusinessHoursLoadFailed, err)
	}
	return businessHoursFromDomain(hours), nil
}

// UpdateBusinessHours 修改当前企业的客服工作时间。
func (o *customerServiceOps) UpdateBusinessHours(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.BusinessHours) (appservice.BusinessHours, error) {
	// 每周时段按周一到周日转换为领域值。
	hours := domain.BusinessHours{Enabled: input.Enabled, TimeZone: input.TimeZone, Overrides: arr.OrEmpty(arr.Map(input.Overrides, func(override appservice.BusinessHoursOverride) domain.BusinessHoursOverride {
		return domain.BusinessHoursOverride{Date: override.Date, Periods: businessHoursPeriodsToDomain(override.Periods)}
	}))}
	for day, periods := range input.Weekly {
		hours.Weekly[day] = businessHoursPeriodsToDomain(periods)
	}
	saved, err := o.updateBusinessHours.Execute(ctx, identity, hours)
	if err != nil {
		return appservice.BusinessHours{}, dispatch.Catalog{dispatch.FieldRule(businessHoursFieldKeys), dispatch.SessionRule}.Translate(meta, err, i18n.ErrorBusinessHoursUpdateFailed)
	}
	slog.InfoContext(ctx, "客服工作时间已更新", "enabled", saved.Enabled, "time_zone", saved.TimeZone)
	return businessHoursFromDomain(saved), nil
}

// GetServiceTimeouts 读取当前企业的客服超时时长。
func (o *customerServiceOps) GetServiceTimeouts(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ServiceTimeouts, error) {
	timeouts, err := o.getServiceTimeouts.Execute(ctx, identity)
	if err != nil {
		return appservice.ServiceTimeouts{}, appservice.FailedError(meta, i18n.ErrorServiceTimeoutsLoadFailed, err)
	}
	return appservice.ServiceTimeouts(timeouts), nil
}

// UpdateServiceTimeouts 修改当前企业的客服超时时长。
func (o *customerServiceOps) UpdateServiceTimeouts(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ServiceTimeouts) (appservice.ServiceTimeouts, error) {
	saved, err := o.updateServiceTimeouts.Execute(ctx, identity, domain.ServiceTimeouts(input))
	if err != nil {
		return appservice.ServiceTimeouts{}, dispatch.Catalog{dispatch.FieldRule(serviceTimeoutsFieldKeys), dispatch.SessionRule}.Translate(meta, err, i18n.ErrorServiceTimeoutsUpdateFailed)
	}
	slog.InfoContext(ctx, "客服超时时长已更新", "response_reminder_minutes", saved.ResponseReminderMinutes, "response_reclaim_minutes", saved.ResponseReclaimMinutes,
		"queue_reminder_minutes", saved.QueueReminderMinutes, "ai_follow_up_minutes", saved.AIFollowUpMinutes, "ai_close_minutes", saved.AICloseMinutes)
	return appservice.ServiceTimeouts(saved), nil
}

// ListServiceCategories 返回当前企业的咨询分类目录。
func (o *customerServiceOps) ListServiceCategories(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ServiceCategoryList, error) {
	records, err := o.listCategories.Execute(ctx, identity)
	if err != nil {
		return appservice.ServiceCategoryList{}, serviceCategoryError(meta, err, i18n.ErrorServiceCategoryListFailed)
	}
	categories := arr.Map(records, serviceCategoryFromAction)
	return appservice.ServiceCategoryList{Categories: categories}, nil
}

// CreateServiceCategory 新增咨询分类。
func (o *customerServiceOps) CreateServiceCategory(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ServiceCategoryInput) (appservice.ServiceCategory, error) {
	record, err := o.createCategory.Execute(ctx, identity, servicecategoryaction.Input{Name: input.Name, Description: input.Description, TeamID: input.TeamID})
	if err != nil {
		return appservice.ServiceCategory{}, serviceCategoryError(meta, err, i18n.ErrorServiceCategoryCreateFailed)
	}
	slog.InfoContext(ctx, "咨询分类已新增", "service_category_id", record.ID)
	return serviceCategoryFromAction(*record), nil
}

// UpdateServiceCategory 修改咨询分类。
func (o *customerServiceOps) UpdateServiceCategory(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, categoryID string, input appservice.ServiceCategoryInput) (appservice.ServiceCategory, error) {
	record, err := o.updateCategory.Execute(ctx, identity, categoryID, servicecategoryaction.Input{Name: input.Name, Description: input.Description, TeamID: input.TeamID})
	if err != nil {
		return appservice.ServiceCategory{}, serviceCategoryError(meta, err, i18n.ErrorServiceCategoryUpdateFailed)
	}
	slog.InfoContext(ctx, "咨询分类已更新", "service_category_id", categoryID)
	return serviceCategoryFromAction(*record), nil
}

// DeleteServiceCategory 归档咨询分类。
func (o *customerServiceOps) DeleteServiceCategory(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, categoryID string) error {
	if err := o.archiveCategory.Execute(ctx, identity, categoryID); err != nil {
		return serviceCategoryError(meta, err, i18n.ErrorServiceCategoryDeleteFailed)
	}
	slog.InfoContext(ctx, "咨询分类已删除", "service_category_id", categoryID)
	return nil
}

// serviceCategoryFieldKeys 把咨询分类校验错误码映射为本地化文案键。
var serviceCategoryFieldKeys = map[common.FieldCode]i18n.Key{
	servicecategoryaction.ValidationNameDuplicate: i18n.FieldServiceCategoryNameDuplicate,
	servicecategoryaction.ValidationTeamInvalid:   i18n.FieldTeamInvalid,
}

// serviceCategoryErrors 是咨询分类操作的错误转换规则。
var serviceCategoryErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.FieldRule(serviceCategoryFieldKeys),
	dispatch.Is(servicecategoryaction.ErrNotFound, dispatch.NotFound(i18n.ErrorServiceCategoryNotFound)),
	dispatch.Is(servicecategoryaction.ErrLimitReached, dispatch.Invalid(i18n.ErrorServiceCategoryLimitReached)),
})

// serviceCategoryError 把咨询分类操作错误转换为结构化、本地化错误。
func serviceCategoryError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return serviceCategoryErrors.Translate(meta, err, failureKey)
}

// serviceCategoryFromAction 转换咨询分类契约。
func serviceCategoryFromAction(record servicecategoryaction.Record) appservice.ServiceCategory {
	category := appservice.ServiceCategory{ID: record.ID, Name: record.Name, Description: record.Description, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
	if record.TeamID != nil && record.TeamName != nil {
		category.Team = &appservice.TeamSummary{ID: *record.TeamID, Name: *record.TeamName}
	}
	return category
}

// businessHoursFromDomain 把领域工作时间转换为传输结构。
func businessHoursFromDomain(hours domain.BusinessHours) appservice.BusinessHours {
	return appservice.BusinessHours{
		Enabled: hours.Enabled, TimeZone: hours.TimeZone, Weekly: arr.Map(hours.Weekly[:], businessHoursPeriodsFromDomain),
		Overrides: arr.Map(hours.Overrides, func(override domain.BusinessHoursOverride) appservice.BusinessHoursOverride {
			return appservice.BusinessHoursOverride{Date: override.Date, Periods: businessHoursPeriodsFromDomain(override.Periods)}
		}),
	}
}

// businessHoursPeriodsFromDomain 把领域工作时段转换为传输结构。
func businessHoursPeriodsFromDomain(periods []domain.BusinessHoursPeriod) []appservice.BusinessHoursPeriod {
	return arr.Map(periods, func(period domain.BusinessHoursPeriod) appservice.BusinessHoursPeriod {
		return appservice.BusinessHoursPeriod{Start: period.Start, End: period.End}
	})
}

// businessHoursPeriodsToDomain 把传输结构的工作时段转换为领域值。
func businessHoursPeriodsToDomain(periods []appservice.BusinessHoursPeriod) []domain.BusinessHoursPeriod {
	return arr.OrEmpty(arr.Map(periods, func(period appservice.BusinessHoursPeriod) domain.BusinessHoursPeriod {
		return domain.BusinessHoursPeriod{Start: period.Start, End: period.End}
	}))
}

// GetServiceSummarySettings 读取当前企业的周期小结设置。
func (o *customerServiceOps) GetServiceSummarySettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ServiceSummarySettings, error) {
	settings, err := o.getSummarySettings.Execute(ctx, identity)
	if err != nil {
		return appservice.ServiceSummarySettings{}, appservice.FailedError(meta, i18n.ErrorServiceSummarySettingsLoadFailed, err)
	}
	return serviceSummarySettingsFromDomain(settings), nil
}

// UpdateServiceSummarySettings 修改当前企业的周期小结设置。
func (o *customerServiceOps) UpdateServiceSummarySettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ServiceSummarySettings) (appservice.ServiceSummarySettings, error) {
	settings := domain.ServiceSummarySettings{DecisionModelID: input.DecisionModelID, SummaryModelID: input.SummaryModelID, Locale: input.Locale}
	saved, err := o.updateSummarySettings.Execute(ctx, identity, settings)
	if err != nil {
		return appservice.ServiceSummarySettings{}, dispatch.Catalog{dispatch.FieldRule(serviceSummarySettingsFieldKeys), dispatch.SessionRule}.Translate(meta, err, i18n.ErrorServiceSummarySettingsUpdateFailed)
	}
	slog.InfoContext(ctx, "周期小结设置已更新", "decision_configured", saved.DecisionModelID != nil, "summary_configured", saved.SummaryModelID != nil, "locale", saved.Locale)
	return serviceSummarySettingsFromDomain(saved), nil
}

// serviceSummarySettingsFromDomain 把周期小结设置转换为传输结构。
func serviceSummarySettingsFromDomain(settings domain.ServiceSummarySettings) appservice.ServiceSummarySettings {
	return appservice.ServiceSummarySettings{
		DecisionModelID: settings.DecisionModelID, SummaryModelID: settings.SummaryModelID, Locale: settings.Locale,
	}
}

// GetServiceSummaries 返回服务会话当前周期的交接摘要与同一发起人已关闭周期的小结。
func (o *customerServiceOps) GetServiceSummaries(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ServiceSummaries, error) {
	summaries, err := o.listSummaries.Execute(ctx, identity, conversationID)
	if err != nil {
		if errors.Is(err, conversationaction.ErrConversationNotFound) {
			return appservice.ServiceSummaries{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
		}
		return appservice.ServiceSummaries{}, appservice.FailedError(meta, i18n.ErrorServiceSummariesLoadFailed, err)
	}
	result := appservice.ServiceSummaries{Sessions: arr.Map(summaries.Sessions, serviceSessionSummaryFromAction)}
	if handoff := summaries.Handoff; handoff != nil {
		result.Handoff = &appservice.HandoffSummary{Request: handoff.Request, Progress: handoff.Progress, Blocker: handoff.Blocker, MessageID: summaries.HandoffMessageID}
	}
	return result, nil
}

// UpdateServiceSessionSummary 修改已关闭服务周期的小结、是否解决与咨询分类。
func (o *customerServiceOps) UpdateServiceSessionSummary(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, serviceSessionID string, input appservice.ServiceSessionSummaryInput) (appservice.ServiceSessionSummary, error) {
	summary, err := o.updateSummary.Execute(ctx, identity, servicesessionaction.UpdateServiceSessionSummaryInput{
		ServiceSessionID: serviceSessionID, Summary: input.Summary, Resolved: input.Resolved, CategoryID: input.CategoryID,
	})
	if err != nil {
		return appservice.ServiceSessionSummary{}, serviceSessionSummaryErrors.Translate(meta, err, i18n.ErrorServiceSessionSummaryUpdateFailed)
	}
	slog.InfoContext(ctx, "周期小结已由客服修改", "service_session_id", serviceSessionID, "identity_id", identity.WorkspaceIdentity.ID)
	return serviceSessionSummaryFromAction(summary), nil
}

// serviceSessionSummaryFromAction 把周期小结转换为传输结构。
func serviceSessionSummaryFromAction(summary servicesessionaction.ServiceSessionSummary) appservice.ServiceSessionSummary {
	return appservice.ServiceSessionSummary{
		ServiceSessionID: summary.ServiceSessionID, ConversationID: summary.ConversationID,
		Source: summary.Source, ChannelType: summary.ChannelType, ChannelName: summary.ChannelName,
		ClosedAt: summary.ClosedAt, CloseReason: summary.CloseReason,
		Summary: summary.Summary, Resolved: summary.Resolved, CategoryID: summary.CategoryID, CategoryName: summary.CategoryName,
		EditedAt: summary.EditedAt, EditedBy: summary.EditedByName,
		Status: support.MapPtr(summary.Status, func(status domain.ServiceSessionSummaryStatus) appservice.ServiceSummaryStatus {
			return appservice.ServiceSummaryStatus(status)
		}),
	}
}

// businessHoursFieldKeys 把客服工作时间校验错误码映射为本地化文案键。
var businessHoursFieldKeys = map[common.FieldCode]i18n.Key{
	customerserviceaction.ValidationTimeZoneInvalid: i18n.FieldTimeZoneInvalid,
	customerserviceaction.ValidationWeeklyInvalid:   i18n.FieldBusinessHoursWeeklyInvalid,
	customerserviceaction.ValidationOverrideInvalid: i18n.FieldBusinessHoursOverrideInvalid,
}

// serviceTimeoutsFieldKeys 把客服超时时长校验错误码映射为本地化文案键。
var serviceTimeoutsFieldKeys = map[common.FieldCode]i18n.Key{
	customerserviceaction.ValidationReclaimNotAfterRemind: i18n.FieldServiceReclaimNotAfterReminder,
}

// serviceSummarySettingsFieldKeys 把周期小结设置校验错误码映射为本地化文案键。
var serviceSummarySettingsFieldKeys = map[common.FieldCode]i18n.Key{
	customerserviceaction.ValidationSummaryModelInvalid: i18n.FieldServiceSummaryModelInvalid,
}

// serviceSessionSummaryFieldKeys 把小结校验错误码映射为本地化文案键。
var serviceSessionSummaryFieldKeys = map[common.FieldCode]i18n.Key{
	servicesessionaction.ValidationCategoryIDInvalid: i18n.FieldServiceCategoryInvalid,
}

// serviceSessionSummaryErrors 是客服修改周期小结的错误转换规则。
var serviceSessionSummaryErrors = dispatch.Catalog{
	dispatch.FieldRule(serviceSessionSummaryFieldKeys),
	dispatch.Is(servicesessionaction.ErrServiceSessionNotFound, dispatch.NotFound(i18n.ErrorConversationNotFound)),
	dispatch.Is(conversationaction.ErrConversationNotFound, dispatch.NotFound(i18n.ErrorConversationNotFound)),
	// 只有周期未关闭的冲突按冲突返回。
	func(meta appservice.RequestMeta, err error) error {
		if conflictError, ok := errors.AsType[*conversationaction.ConflictError](err); ok && conflictError.Reason == servicesessionaction.ConflictReasonServiceSessionNotClosed {
			return appservice.ConflictError(meta, i18n.ErrorServiceSessionNotClosed, conflictError.Reason)
		}
		return nil
	},
	dispatch.SessionRule,
}
