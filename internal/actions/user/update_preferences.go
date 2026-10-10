//go:build server

package user

import (
	"context"
	"fmt"
	_ "time/tzdata"

	accountaction "github.com/runforyou-ai/luway/internal/actions/account"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/languagetag"
	"github.com/runforyou-ai/support/validate"
	"github.com/uptrace/bun"
)

// UpdatePreferencesAction 修改当前成员及所属账号的偏好设置。
type UpdatePreferencesAction struct {
	db *bun.DB
}

// NewUpdatePreferencesAction 创建用户偏好修改操作。
func NewUpdatePreferencesAction(db *bun.DB) *UpdatePreferencesAction {
	return &UpdatePreferencesAction{db: db}
}

// Execute 在事务内校验并保存当前用户的偏好设置。
func (a *UpdatePreferencesAction) Execute(ctx context.Context, identity *servermodels.Identity, input PreferencesInput) (*servermodels.Identity, error) {
	fields := make(map[string]ValidationCode)
	if !input.Locale.Valid() {
		fields["locale"] = ValidationLocaleInvalid
	}
	if !validate.Timezone(input.TimeZone) {
		fields["timeZone"] = ValidationTimeZoneInvalid
	}
	// 翻译语言为空时使用界面语言，其余取规范的语言标签。
	var translationLanguage *string
	if input.TranslationLanguage != "" {
		normalized, ok := languagetag.Normalize(input.TranslationLanguage)
		if !ok || normalized == languagetag.Undetermined {
			fields["translationLanguage"] = ValidationTranslationLanguageInvalid
		}
		translationLanguage = &normalized
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	var updatedIdentity *servermodels.Identity
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := identityaction.UpdateUserAccount(ctx, identity.Workspace.ID, tx.NewUpdate().
			Model((*servermodels.User)(nil)).
			Set("profile_version = profile_version + CASE WHEN (translation_language, message_notifications_enabled) IS DISTINCT FROM (?, ?) THEN 1 ELSE 0 END",
				translationLanguage, input.MessageNotificationsEnabled).
			Set("translation_language = ?", translationLanguage).
			Set("message_notifications_enabled = ?", input.MessageNotificationsEnabled).
			Where("u.id = ?", identity.User.ID).
			Where("u.workspace_id = ?", identity.Workspace.ID)); err != nil {
			return fmt.Errorf("update user preferences: %w", err)
		}
		// 界面语言和时区属于账号，实际变化时推进该账号全部成员身份的资料版本。
		var changed []string
		if err := tx.NewUpdate().Model((*servermodels.Account)(nil)).
			Set("locale = ?", input.Locale).
			Set("time_zone = ?", input.TimeZone).
			Where("id = ?", identity.Account.ID).
			Where("(locale, time_zone) IS DISTINCT FROM (?, ?)", input.Locale, input.TimeZone).
			Returning("id").
			Scan(ctx, &changed); err != nil {
			return fmt.Errorf("update account preferences: %w", err)
		}
		if len(changed) > 0 {
			if err := accountaction.TouchAccountMembers(ctx, tx, identity.Account.ID); err != nil {
				return err
			}
		}
		reloaded, err := loadCurrentIdentity(ctx, tx, identity)
		if err != nil {
			return fmt.Errorf("reload user preferences: %w", err)
		}
		updatedIdentity = reloaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updatedIdentity, nil
}
