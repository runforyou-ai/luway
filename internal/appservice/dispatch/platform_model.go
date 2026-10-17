//go:build server

package dispatch

import (
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// PlatformAIModelFieldCodes 是 Auto 池成员与平台模型调用记录校验错误码对应的文案键。
var PlatformAIModelFieldCodes = map[common.FieldCode]i18n.Key{
	aimodel.ValidationPoolIdentifierInvalid:        i18n.FieldPlatformAIModelNameInvalid,
	aimodel.ValidationPoolIdentifierDuplicate:      i18n.FieldPlatformAIModelRouteDuplicate,
	aimodel.ValidationPoolTypeInvalid:              i18n.FieldPlatformAIModelTypeInvalid,
	aimodel.ValidationPoolInputModalitiesInvalid:   i18n.FieldPlatformAIModelInputModalitiesInvalid,
	aimodel.ValidationPoolContextWindowInvalid:     i18n.FieldPlatformAIModelContextWindowInvalid,
	aimodel.ValidationPoolMaxOutputTokensInvalid:   i18n.FieldPlatformAIModelMaxOutputTokensInvalid,
	aimodel.ValidationPoolProviderInvalid:          i18n.FieldPlatformAIModelRoutesInvalid,
	aimodel.ValidationPoolWeightInvalid:            i18n.FieldPlatformAIModelRouteWeightInvalid,
	aimodel.ValidationPlatformUsageConflict:        i18n.FieldPlatformAIModelUsageConflict,
	platformaction.ValidationModelCallQueryInvalid: i18n.FieldPlatformAIModelCallQueryInvalid,
}
