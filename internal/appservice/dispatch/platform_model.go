//go:build server

package dispatch

import (
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// PlatformAIModelFieldCodes 是平台模型与平台模型调用记录校验错误码对应的文案键。
var PlatformAIModelFieldCodes = map[common.FieldCode]i18n.Key{
	aimodel.ValidationPlatformNameInvalid:            i18n.FieldPlatformAIModelNameInvalid,
	aimodel.ValidationPlatformNameDuplicate:          i18n.FieldPlatformAIModelNameDuplicate,
	aimodel.ValidationPlatformTypeInvalid:            i18n.FieldPlatformAIModelTypeInvalid,
	aimodel.ValidationPlatformInputModalitiesInvalid: i18n.FieldPlatformAIModelInputModalitiesInvalid,
	aimodel.ValidationPlatformContextWindowInvalid:   i18n.FieldPlatformAIModelContextWindowInvalid,
	aimodel.ValidationPlatformMaxOutputTokensInvalid: i18n.FieldPlatformAIModelMaxOutputTokensInvalid,
	aimodel.ValidationPlatformRoutesInvalid:          i18n.FieldPlatformAIModelRoutesInvalid,
	aimodel.ValidationPlatformRouteDuplicate:         i18n.FieldPlatformAIModelRouteDuplicate,
	aimodel.ValidationPlatformRouteWeightInvalid:     i18n.FieldPlatformAIModelRouteWeightInvalid,
	aimodel.ValidationPlatformUsageConflict:          i18n.FieldPlatformAIModelUsageConflict,
	platformaction.ValidationModelCallQueryInvalid:   i18n.FieldPlatformAIModelCallQueryInvalid,
}
