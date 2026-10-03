//go:build server

// Package platformmodel 实现平台管理员维护的平台模型目录、来源路由与平台模型调用记录。
package platformmodel

import (
	"errors"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
)

var (
	// ErrNotFound 表示平台模型或调用记录不存在。
	ErrNotFound = errors.New("platform AI model not found")
	// ErrInUse 表示平台模型仍被工作区业务配置引用。
	ErrInUse = errors.New("platform AI model is in use")
)

// ValidationCode 标识平台模型字段校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationNameInvalid            ValidationCode = "PLATFORM_AI_MODEL_NAME_INVALID"
	ValidationNameDuplicate          ValidationCode = "PLATFORM_AI_MODEL_NAME_DUPLICATE"
	ValidationTypeInvalid            ValidationCode = "PLATFORM_AI_MODEL_TYPE_INVALID"
	ValidationInputModalitiesInvalid ValidationCode = "PLATFORM_AI_MODEL_INPUT_MODALITIES_INVALID"
	ValidationContextWindowInvalid   ValidationCode = "PLATFORM_AI_MODEL_CONTEXT_WINDOW_INVALID"
	ValidationMaxOutputTokensInvalid ValidationCode = "PLATFORM_AI_MODEL_MAX_OUTPUT_TOKENS_INVALID"
	ValidationRoutesInvalid          ValidationCode = "PLATFORM_AI_MODEL_ROUTES_INVALID"
	ValidationRouteDuplicate         ValidationCode = "PLATFORM_AI_MODEL_ROUTE_DUPLICATE"
	ValidationUsageConflict          ValidationCode = "PLATFORM_AI_MODEL_USAGE_CONFLICT"
	ValidationPriceInvalid           ValidationCode = "PLATFORM_AI_MODEL_PRICE_INVALID"
	ValidationCallQueryInvalid       ValidationCode = "PLATFORM_AI_MODEL_CALL_QUERY_INVALID"
)

// RouteInput 定义平台模型的一个来源：平台供应商与该供应商的上游模型标识，编号为空表示新增来源。
type RouteInput struct {
	ID         string
	ProviderID string
	Identifier string
	Enabled    bool
}

// Input 定义平台模型的属性、积分价格与按尝试顺序排列的来源；价格为空表示未定价，工作区不可使用。
type Input struct {
	aimodel.Spec
	Price  *domain.CreditPrice
	Routes []RouteInput
}

// Route 定义平台模型的一个来源及其供应商。
type Route struct {
	ID            string                 `bun:"id"`
	ModelID       string                 `bun:"model_id"`
	ProviderID    string                 `bun:"provider_id"`
	ProviderName  string                 `bun:"provider_name"`
	ProviderBrand domain.AIProviderBrand `bun:"provider_brand"`
	Identifier    string                 `bun:"identifier"`
	Enabled       bool                   `bun:"enabled"`
}

// Record 定义平台模型的属性、积分价格与按尝试顺序排列的来源；价格为空表示未定价。
type Record struct {
	ID string
	aimodel.Spec
	Price  *domain.CreditPrice
	Routes []Route
}
