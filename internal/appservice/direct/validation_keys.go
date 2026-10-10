//go:build server

package direct

import (
	"maps"

	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// validationFieldTables 按变量名登记各业务域校验错误码到文案键的映射，本包与 dispatch 包的每张映射都在此登记。
var validationFieldTables = map[string]map[common.FieldCode]i18n.Key{
	"accountFieldKeys":                      accountFieldKeys,
	"agentCreateFieldKeys":                  agentCreateFieldKeys,
	"agentEvaluationFieldKeys":              agentEvaluationFieldKeys,
	"agentExecutionFieldKeys":               agentExecutionFieldKeys,
	"agentStatusFieldKeys":                  agentStatusFieldKeys,
	"agentUpdateFieldKeys":                  agentUpdateFieldKeys,
	"AIProviderFieldCodes":                  dispatch.AIProviderFieldCodes,
	"businessHoursFieldKeys":                businessHoursFieldKeys,
	"businessSystemFieldKeys":               businessSystemFieldKeys,
	"channelFieldKeys":                      channelFieldKeys,
	"contactFieldKeys":                      contactFieldKeys,
	"contactFieldValidationKeys":            contactFieldValidationKeys,
	"contactTagValidationKeys":              contactTagValidationKeys,
	"conversationMessageValidationKeys":     conversationMessageValidationKeys,
	"fileFieldKeys":                         fileFieldKeys,
	"invitationFieldKeys":                   invitationFieldKeys,
	"knowledgeBaseFieldKeys":                knowledgeBaseFieldKeys,
	"personalAgentFieldKeys":                personalAgentFieldKeys,
	"platformFieldKeys":                     platformFieldKeys,
	"PlatformAIModelFieldCodes":             dispatch.PlatformAIModelFieldCodes,
	"preferencesFieldKeys":                  preferencesFieldKeys,
	"profileFieldKeys":                      profileFieldKeys,
	"roleFieldKeys":                         roleFieldKeys,
	"serviceCategoryFieldKeys":              serviceCategoryFieldKeys,
	"serviceReplySuggestionsValidationKeys": serviceReplySuggestionsValidationKeys,
	"serviceSessionSummaryFieldKeys":        serviceSessionSummaryFieldKeys,
	"serviceSummarySettingsFieldKeys":       serviceSummarySettingsFieldKeys,
	"serviceTimeoutsFieldKeys":              serviceTimeoutsFieldKeys,
	"teamFieldKeys":                         teamFieldKeys,
	"telegramFieldKeys":                     telegramFieldKeys,
	"translationFieldKeys":                  translationFieldKeys,
	"translationSettingsFieldKeys":          translationSettingsFieldKeys,
	"userFieldKeys":                         userFieldKeys,
	"webSearchFieldKeys":                    webSearchFieldKeys,
	"websiteVisitorValidationKeys":          websiteVisitorValidationKeys,
	"wechatFieldKeys":                       wechatFieldKeys,
	"workspaceFieldKeys":                    workspaceFieldKeys,
}

// ValidationFieldKeys 返回按变量名登记的校验错误码文案映射副本，供完整性检查读取。
func ValidationFieldKeys() map[string]map[common.FieldCode]i18n.Key {
	tables := make(map[string]map[common.FieldCode]i18n.Key, len(validationFieldTables))
	for name, table := range validationFieldTables {
		tables[name] = maps.Clone(table)
	}
	return tables
}
