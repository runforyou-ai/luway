//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// CustomerServiceSetting 保存工作区的客户身份验证和翻译配置。
type CustomerServiceSetting struct {
	bun.BaseModel          `bun:"table:customer_service_settings,alias:css"`
	WorkspaceID            string    `bun:"workspace_id,pk"`
	TranslationModelID     *string   `bun:"translation_model_id"`
	CustomerIdentitySecret *string   `bun:"customer_identity_secret"`
	CreatedAt              time.Time `bun:"created_at"`
	UpdatedAt              time.Time `bun:"updated_at"`
}
