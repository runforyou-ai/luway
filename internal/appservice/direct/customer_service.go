//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	contactaction "github.com/runforyou-ai/cervi/internal/actions/contact"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	customerserviceaction "github.com/runforyou-ai/cervi/internal/actions/customerservice"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	servicecategoryaction "github.com/runforyou-ai/cervi/internal/actions/servicecategory"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
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
func newCustomerServiceOps(db *bun.DB) customerServiceOps {
	return customerServiceOps{
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
func (o *directOperations) GetCustomerIdentitySecret(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.CustomerIdentitySecret, error) {
	secret, err := o.getIdentitySecret.Execute(ctx, identity)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.CustomerIdentitySecret{}, ctx.Err()
		}
		slog.Warn("读取客户身份密钥失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.CustomerIdentitySecret{}, appservice.FailedError(meta, i18n.ErrorCustomerIdentitySecretLoadFailed)
	}
	return appservice.CustomerIdentitySecret{Secret: secret}, nil
}

// RegenerateCustomerIdentitySecret 生成或重新生成当前企业的客户身份密钥，旧密钥立即失效。
func (o *directOperations) RegenerateCustomerIdentitySecret(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.CustomerIdentitySecret, error) {
	secret, err := o.regenerateSecret.Execute(ctx, identity)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.CustomerIdentitySecret{}, ctx.Err()
		}
		if errors.Is(err, identityaction.ErrInvalid) {
			return appservice.CustomerIdentitySecret{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
		}
		slog.Warn("生成客户身份密钥失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.CustomerIdentitySecret{}, appservice.FailedError(meta, i18n.ErrorIdentitySecretRegenerateFailed)
	}
	slog.Info("客户身份密钥已重新生成", "organization_id", identity.Organization.ID, "user_id", identity.User.ID)
	return appservice.CustomerIdentitySecret{Secret: secret}, nil
}

// GetRequesterProfile 返回服务会话发起人的资料；发起人是客户时给出客户身份与当前周期访客上下文。
func (o *directOperations) GetRequesterProfile(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.RequesterProfile, error) {
	profile, err := o.getRequesterProfile.Execute(ctx, identity, conversationID)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.RequesterProfile{}, ctx.Err()
		}
		if errors.Is(err, contactaction.ErrNotFound) {
			return appservice.RequesterProfile{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
		}
		slog.Warn("读取客户资料失败", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "error", err)
		return appservice.RequesterProfile{}, appservice.FailedError(meta, i18n.ErrorCustomerProfileLoadFailed)
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
func (o *directOperations) ListServiceBusinessQueries(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ServiceBusinessQueryList, error) {
	queries, err := o.listBusinessQueries.Execute(ctx, identity, conversationID)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.ServiceBusinessQueryList{}, ctx.Err()
		}
		if errors.Is(err, conversationaction.ErrConversationNotFound) {
			return appservice.ServiceBusinessQueryList{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
		}
		slog.Warn("读取业务查询记录失败", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "error", err)
		return appservice.ServiceBusinessQueryList{}, appservice.FailedError(meta, i18n.ErrorBusinessQueriesLoadFailed)
	}
	result := appservice.ServiceBusinessQueryList{Queries: make([]appservice.ServiceBusinessQuery, 0, len(queries))}
	for _, query := range queries {
		call := query.ToolCall
		result.Queries = append(result.Queries, appservice.ServiceBusinessQuery{
			ID: query.ID, MCPServer: call.MCPServer, ToolName: call.Name, Arguments: call.Arguments, Result: call.Result, Error: call.Error,
			Status: appservice.AgentToolCallStatus(call.Status), Evidence: call.Evidence, CalledAt: query.CalledAt,
		})
	}
	return result, nil
}

// GetBusinessHours 读取当前企业的客服工作时间。
func (o *directOperations) GetBusinessHours(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.BusinessHours, error) {
	hours, err := o.getBusinessHours.Execute(ctx, identity)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.BusinessHours{}, ctx.Err()
		}
		slog.Warn("读取客服工作时间失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.BusinessHours{}, appservice.FailedError(meta, i18n.ErrorBusinessHoursLoadFailed)
	}
	return businessHoursFromDomain(hours), nil
}

// UpdateBusinessHours 修改当前企业的客服工作时间。
func (o *directOperations) UpdateBusinessHours(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.BusinessHours) (appservice.BusinessHours, error) {
	// 每周时段固定 7 项，按周一到周日转换为领域值。
	if len(input.Weekly) != 7 {
		return appservice.BusinessHours{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, map[string]i18n.Key{"weekly": i18n.FieldBusinessHoursWeeklyInvalid})
	}
	hours := domain.BusinessHours{Enabled: input.Enabled, TimeZone: input.TimeZone, Overrides: make([]domain.BusinessHoursOverride, 0, len(input.Overrides))}
	for day, periods := range input.Weekly {
		hours.Weekly[day] = businessHoursPeriodsToDomain(periods)
	}
	for _, override := range input.Overrides {
		hours.Overrides = append(hours.Overrides, domain.BusinessHoursOverride{Date: override.Date, Periods: businessHoursPeriodsToDomain(override.Periods)})
	}
	saved, err := o.updateBusinessHours.Execute(ctx, identity, hours)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.BusinessHours{}, ctx.Err()
		}
		if validationError, ok := errors.AsType[*common.FieldError](err); ok {
			// 把客服工作时间校验错误码映射为本地化文案键。
			keys := map[common.FieldCode]i18n.Key{
				customerserviceaction.ValidationTimeZoneInvalid: i18n.FieldTimeZoneInvalid,
				customerserviceaction.ValidationWeeklyInvalid:   i18n.FieldBusinessHoursWeeklyInvalid,
				customerserviceaction.ValidationOverrideInvalid: i18n.FieldBusinessHoursOverrideInvalid,
			}
			return appservice.BusinessHours{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
		}
		if errors.Is(err, identityaction.ErrInvalid) {
			return appservice.BusinessHours{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
		}
		slog.Warn("修改客服工作时间失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.BusinessHours{}, appservice.FailedError(meta, i18n.ErrorBusinessHoursUpdateFailed)
	}
	slog.Info("客服工作时间已更新", "organization_id", identity.Organization.ID, "enabled", saved.Enabled, "time_zone", saved.TimeZone)
	return businessHoursFromDomain(saved), nil
}

// GetServiceTimeouts 读取当前企业的客服超时时长。
func (o *directOperations) GetServiceTimeouts(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ServiceTimeouts, error) {
	timeouts, err := o.getServiceTimeouts.Execute(ctx, identity)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.ServiceTimeouts{}, ctx.Err()
		}
		slog.Warn("读取客服超时时长失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.ServiceTimeouts{}, appservice.FailedError(meta, i18n.ErrorServiceTimeoutsLoadFailed)
	}
	return appservice.ServiceTimeouts(timeouts), nil
}

// UpdateServiceTimeouts 修改当前企业的客服超时时长。
func (o *directOperations) UpdateServiceTimeouts(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ServiceTimeouts) (appservice.ServiceTimeouts, error) {
	saved, err := o.updateServiceTimeouts.Execute(ctx, identity, domain.ServiceTimeouts(input))
	if err != nil {
		if ctx.Err() != nil {
			return appservice.ServiceTimeouts{}, ctx.Err()
		}
		if validationError, ok := errors.AsType[*common.FieldError](err); ok {
			// 把客服超时时长校验错误码映射为本地化文案键。
			keys := map[common.FieldCode]i18n.Key{
				customerserviceaction.ValidationTimeoutMinutesInvalid: i18n.FieldServiceTimeoutMinutesInvalid,
				customerserviceaction.ValidationReclaimNotAfterRemind: i18n.FieldServiceReclaimNotAfterReminder,
			}
			return appservice.ServiceTimeouts{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
		}
		if errors.Is(err, identityaction.ErrInvalid) {
			return appservice.ServiceTimeouts{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
		}
		slog.Warn("修改客服超时时长失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.ServiceTimeouts{}, appservice.FailedError(meta, i18n.ErrorServiceTimeoutsUpdateFailed)
	}
	slog.Info("客服超时时长已更新", "organization_id", identity.Organization.ID,
		"response_reminder_minutes", saved.ResponseReminderMinutes, "response_reclaim_minutes", saved.ResponseReclaimMinutes,
		"queue_reminder_minutes", saved.QueueReminderMinutes, "ai_follow_up_minutes", saved.AIFollowUpMinutes, "ai_close_minutes", saved.AICloseMinutes)
	return appservice.ServiceTimeouts(saved), nil
}

// ListServiceCategories 返回当前企业的咨询分类目录。
func (o *directOperations) ListServiceCategories(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ServiceCategoryList, error) {
	records, err := o.listCategories.Execute(ctx, identity)
	if err != nil {
		return appservice.ServiceCategoryList{}, serviceCategoryError(ctx, meta, err, i18n.ErrorServiceCategoryListFailed, identity.Organization.ID, "")
	}
	categories := make([]appservice.ServiceCategory, 0, len(records))
	for _, record := range records {
		categories = append(categories, serviceCategoryFromAction(record))
	}
	return appservice.ServiceCategoryList{Categories: categories}, nil
}

// CreateServiceCategory 新增咨询分类。
func (o *directOperations) CreateServiceCategory(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ServiceCategoryInput) (appservice.ServiceCategory, error) {
	record, err := o.createCategory.Execute(ctx, identity, servicecategoryaction.Input{Name: input.Name, Description: input.Description, TeamID: input.TeamID})
	if err != nil {
		return appservice.ServiceCategory{}, serviceCategoryError(ctx, meta, err, i18n.ErrorServiceCategoryCreateFailed, identity.Organization.ID, "")
	}
	slog.Info("咨询分类已新增", "organization_id", identity.Organization.ID, "service_category_id", record.ID)
	return serviceCategoryFromAction(*record), nil
}

// UpdateServiceCategory 修改咨询分类。
func (o *directOperations) UpdateServiceCategory(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, categoryID string, input appservice.ServiceCategoryInput) (appservice.ServiceCategory, error) {
	record, err := o.updateCategory.Execute(ctx, identity, categoryID, servicecategoryaction.Input{Name: input.Name, Description: input.Description, TeamID: input.TeamID})
	if err != nil {
		return appservice.ServiceCategory{}, serviceCategoryError(ctx, meta, err, i18n.ErrorServiceCategoryUpdateFailed, identity.Organization.ID, categoryID)
	}
	slog.Info("咨询分类已更新", "organization_id", identity.Organization.ID, "service_category_id", categoryID)
	return serviceCategoryFromAction(*record), nil
}

// DeleteServiceCategory 归档咨询分类。
func (o *directOperations) DeleteServiceCategory(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, categoryID string) error {
	if err := o.archiveCategory.Execute(ctx, identity, categoryID); err != nil {
		return serviceCategoryError(ctx, meta, err, i18n.ErrorServiceCategoryDeleteFailed, identity.Organization.ID, categoryID)
	}
	slog.Info("咨询分类已删除", "organization_id", identity.Organization.ID, "service_category_id", categoryID)
	return nil
}

// serviceCategoryError 把咨询分类操作错误转换为结构化、本地化错误。
func serviceCategoryError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID, categoryID string) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		// 把咨询分类校验错误码映射为本地化文案键。
		keys := map[common.FieldCode]i18n.Key{
			servicecategoryaction.ValidationNameRequired:       i18n.FieldServiceCategoryNameRequired,
			servicecategoryaction.ValidationNameTooLong:        i18n.FieldServiceCategoryNameTooLong,
			servicecategoryaction.ValidationNameDuplicate:      i18n.FieldServiceCategoryNameDuplicate,
			servicecategoryaction.ValidationDescriptionTooLong: i18n.FieldServiceCategoryDescTooLong,
			servicecategoryaction.ValidationTeamInvalid:        i18n.FieldTeamInvalid,
		}
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
	}
	if errors.Is(err, servicecategoryaction.ErrNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorServiceCategoryNotFound)
	}
	if errors.Is(err, servicecategoryaction.ErrLimitReached) {
		return appservice.InvalidError(meta, i18n.ErrorServiceCategoryLimitReached, nil)
	}
	slog.Warn("咨询分类操作失败", "organization_id", organizationID, "service_category_id", categoryID, "failure", failureKey, "error", err)
	return appservice.FailedError(meta, failureKey)
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
	output := appservice.BusinessHours{Enabled: hours.Enabled, TimeZone: hours.TimeZone, Weekly: make([][]appservice.BusinessHoursPeriod, 0, len(hours.Weekly)), Overrides: make([]appservice.BusinessHoursOverride, 0, len(hours.Overrides))}
	for _, periods := range hours.Weekly {
		output.Weekly = append(output.Weekly, businessHoursPeriodsFromDomain(periods))
	}
	for _, override := range hours.Overrides {
		output.Overrides = append(output.Overrides, appservice.BusinessHoursOverride{Date: override.Date, Periods: businessHoursPeriodsFromDomain(override.Periods)})
	}
	return output
}

// businessHoursPeriodsFromDomain 把领域工作时段转换为传输结构。
func businessHoursPeriodsFromDomain(periods []domain.BusinessHoursPeriod) []appservice.BusinessHoursPeriod {
	output := make([]appservice.BusinessHoursPeriod, 0, len(periods))
	for _, period := range periods {
		output = append(output, appservice.BusinessHoursPeriod{Start: period.Start, End: period.End})
	}
	return output
}

// businessHoursPeriodsToDomain 把传输结构的工作时段转换为领域值。
func businessHoursPeriodsToDomain(periods []appservice.BusinessHoursPeriod) []domain.BusinessHoursPeriod {
	output := make([]domain.BusinessHoursPeriod, 0, len(periods))
	for _, period := range periods {
		output = append(output, domain.BusinessHoursPeriod{Start: period.Start, End: period.End})
	}
	return output
}

// GetServiceSummarySettings 读取当前企业的周期小结设置。
func (o *directOperations) GetServiceSummarySettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ServiceSummarySettings, error) {
	settings, err := o.getSummarySettings.Execute(ctx, identity)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.ServiceSummarySettings{}, ctx.Err()
		}
		slog.Warn("读取周期小结设置失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.ServiceSummarySettings{}, appservice.FailedError(meta, i18n.ErrorSummarySettingsLoadFailed)
	}
	return serviceSummarySettingsFromDomain(settings), nil
}

// UpdateServiceSummarySettings 修改当前企业的周期小结设置。
func (o *directOperations) UpdateServiceSummarySettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ServiceSummarySettings) (appservice.ServiceSummarySettings, error) {
	settings := domain.ServiceSummarySettings{Locale: domain.Locale(input.Locale)}
	if input.Decision != nil {
		settings.Decision = &domain.AIModelReference{ProviderID: input.Decision.ProviderID, ModelIdentifier: input.Decision.ModelIdentifier}
	}
	if input.Summary != nil {
		settings.Summary = &domain.AIModelReference{ProviderID: input.Summary.ProviderID, ModelIdentifier: input.Summary.ModelIdentifier}
	}
	saved, err := o.updateSummarySettings.Execute(ctx, identity, settings)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.ServiceSummarySettings{}, ctx.Err()
		}
		if validationError, ok := errors.AsType[*common.FieldError](err); ok {
			// 把周期小结设置校验错误码映射为本地化文案键。
			keys := map[common.FieldCode]i18n.Key{
				customerserviceaction.ValidationSummaryModelInvalid:  i18n.FieldServiceSummaryModelInvalid,
				customerserviceaction.ValidationSummaryLocaleInvalid: i18n.FieldLocaleInvalid,
			}
			return appservice.ServiceSummarySettings{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
		}
		if errors.Is(err, identityaction.ErrInvalid) {
			return appservice.ServiceSummarySettings{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
		}
		slog.Warn("修改周期小结设置失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.ServiceSummarySettings{}, appservice.FailedError(meta, i18n.ErrorSummarySettingsUpdateFailed)
	}
	slog.Info("周期小结设置已更新", "organization_id", identity.Organization.ID,
		"decision_configured", saved.Decision != nil, "summary_configured", saved.Summary != nil, "locale", saved.Locale)
	return serviceSummarySettingsFromDomain(saved), nil
}

// serviceSummarySettingsFromDomain 把周期小结设置转换为传输结构。
func serviceSummarySettingsFromDomain(settings domain.ServiceSummarySettings) appservice.ServiceSummarySettings {
	result := appservice.ServiceSummarySettings{Locale: appservice.Locale(settings.Locale)}
	if settings.Decision != nil {
		result.Decision = &appservice.AIModelReference{ProviderID: settings.Decision.ProviderID, ModelIdentifier: settings.Decision.ModelIdentifier}
	}
	if settings.Summary != nil {
		result.Summary = &appservice.AIModelReference{ProviderID: settings.Summary.ProviderID, ModelIdentifier: settings.Summary.ModelIdentifier}
	}
	return result
}

// GetServiceSummaries 返回服务会话当前周期的交接摘要与同一发起人已关闭周期的小结。
func (o *directOperations) GetServiceSummaries(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ServiceSummaries, error) {
	summaries, err := o.listSummaries.Execute(ctx, identity, conversationID)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.ServiceSummaries{}, ctx.Err()
		}
		if errors.Is(err, conversationaction.ErrConversationNotFound) {
			return appservice.ServiceSummaries{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
		}
		slog.Warn("读取周期小结失败", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "error", err)
		return appservice.ServiceSummaries{}, appservice.FailedError(meta, i18n.ErrorServiceSummariesLoadFailed)
	}
	result := appservice.ServiceSummaries{Sessions: make([]appservice.ServiceSessionSummary, 0, len(summaries.Sessions))}
	if handoff := summaries.Handoff; handoff != nil {
		result.Handoff = &appservice.HandoffSummary{Request: handoff.Request, Progress: handoff.Progress, Blocker: handoff.Blocker, MessageID: summaries.HandoffMessageID}
	}
	for _, summary := range summaries.Sessions {
		result.Sessions = append(result.Sessions, serviceSessionSummaryFromAction(summary))
	}
	return result, nil
}

// UpdateServiceSessionSummary 修改已关闭服务周期的小结、是否解决与咨询分类。
func (o *directOperations) UpdateServiceSessionSummary(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, serviceSessionID string, input appservice.ServiceSessionSummaryInput) (appservice.ServiceSessionSummary, error) {
	summary, err := o.updateSummary.Execute(ctx, identity, servicesessionaction.UpdateServiceSessionSummaryInput{
		ServiceSessionID: serviceSessionID, Summary: input.Summary, Resolved: input.Resolved, CategoryID: input.CategoryID,
	})
	if err != nil {
		if ctx.Err() != nil {
			return appservice.ServiceSessionSummary{}, ctx.Err()
		}
		if validationError, ok := errors.AsType[*common.FieldError](err); ok {
			// 把小结校验错误码映射为本地化文案键。
			keys := map[common.FieldCode]i18n.Key{
				servicesessionaction.ValidationSummaryTooLong:    i18n.FieldServiceSummaryTooLong,
				servicesessionaction.ValidationCategoryIDInvalid: i18n.FieldServiceCategoryInvalid,
			}
			return appservice.ServiceSessionSummary{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
		}
		if errors.Is(err, servicesessionaction.ErrServiceSessionNotFound) || errors.Is(err, conversationaction.ErrConversationNotFound) {
			return appservice.ServiceSessionSummary{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
		}
		if conflict, ok := errors.AsType[*conversationaction.ConflictError](err); ok && conflict.Reason == servicesessionaction.ConflictReasonServiceSessionNotClosed {
			return appservice.ServiceSessionSummary{}, appservice.ConflictError(meta, i18n.ErrorServiceSessionNotClosed, conflict.Reason)
		}
		if errors.Is(err, identityaction.ErrInvalid) {
			return appservice.ServiceSessionSummary{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
		}
		slog.Warn("修改周期小结失败", "organization_id", identity.Organization.ID, "service_session_id", serviceSessionID, "error", err)
		return appservice.ServiceSessionSummary{}, appservice.FailedError(meta, i18n.ErrorSessionSummaryUpdateFailed)
	}
	slog.Info("周期小结已由客服修改", "organization_id", identity.Organization.ID, "service_session_id", serviceSessionID, "identity_id", identity.OrganizationIdentity.ID)
	return serviceSessionSummaryFromAction(summary), nil
}

// serviceSessionSummaryFromAction 把周期小结转换为传输结构。
func serviceSessionSummaryFromAction(summary servicesessionaction.ServiceSessionSummary) appservice.ServiceSessionSummary {
	result := appservice.ServiceSessionSummary{
		ServiceSessionID: summary.ServiceSessionID, ConversationID: summary.ConversationID,
		Source: appservice.ServiceSource(summary.Source), ChannelType: (*appservice.ChannelType)(summary.ChannelType), ChannelName: summary.ChannelName,
		ClosedAt: summary.ClosedAt, CloseReason: appservice.ServiceSessionCloseReason(summary.CloseReason),
		Summary: summary.Summary, Resolved: summary.Resolved, CategoryID: summary.CategoryID, CategoryName: summary.CategoryName,
		EditedAt: summary.EditedAt, EditedBy: summary.EditedByName,
	}
	if summary.Status != nil {
		result.Status = new(appservice.ServiceSummaryStatus(*summary.Status))
	}
	return result
}
