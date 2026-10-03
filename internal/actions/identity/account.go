//go:build server

package identity

import (
	"context"
	"errors"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// ErrAccountEmailTaken 表示邮箱已被平台内其他账号使用。
var ErrAccountEmailTaken = errors.New("account email is taken")

// NewAccount 定义创建账号所需的已校验字段；PasswordHash 为空时账号没有本地密码。
type NewAccount struct {
	Email           string
	EmailVerified   bool
	PasswordHash    string
	DisplayName     string
	Locale          domain.Locale
	TimeZone        string
	IsPlatformAdmin bool
}

// CreateAccount 在调用方事务内创建有效账号。
func CreateAccount(ctx context.Context, db bun.IDB, input NewAccount) (*servermodels.Account, error) {
	account := &servermodels.Account{
		Email:           input.Email,
		PasswordHash:    input.PasswordHash,
		DisplayName:     input.DisplayName,
		Locale:          string(input.Locale),
		TimeZone:        input.TimeZone,
		Status:          string(domain.AccountStatusActive),
		IsPlatformAdmin: input.IsPlatformAdmin,
	}
	if input.EmailVerified {
		now := time.Now()
		account.EmailVerifiedAt = &now
	}
	if _, err := db.NewInsert().Model(account).
		Column("email", "email_verified_at", "password_hash", "display_name", "locale", "time_zone", "status", "is_platform_admin").
		Returning("id, created_at, updated_at").
		Exec(ctx); err != nil {
		if pgerr.UniqueViolationOn(err, "accounts_email_unique") {
			return nil, ErrAccountEmailTaken
		}
		return nil, err
	}
	return account, nil
}

// TouchAccountMembers 推进账号全部成员身份的资料版本并登记本人通知，供账号级资料变化后各工作区的登录态刷新。
func TouchAccountMembers(ctx context.Context, db bun.IDB, accountID string) error {
	var rows []struct {
		OrganizationID string `bun:"organization_id"`
		ID             string `bun:"id"`
		ProfileVersion int64  `bun:"profile_version"`
	}
	if err := db.NewUpdate().Model((*servermodels.User)(nil)).
		Set("profile_version = profile_version + 1").
		Set("updated_at = now()").
		Where("account_id = ?", accountID).
		Returning("organization_id, id, profile_version").
		Scan(ctx, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		realtime.Notify(ctx, realtime.UserIdentityProfileChanged(row.OrganizationID, row.ID, row.ProfileVersion))
	}
	return nil
}

// UpdateAccountEmail 修改账号邮箱，邮箱实际变化时清除验证时间并推进全部成员身份的资料版本。
func UpdateAccountEmail(ctx context.Context, db bun.IDB, accountID, email string) error {
	var changed []string
	err := db.NewUpdate().Model((*servermodels.Account)(nil)).
		Set("email = ?", email).
		Set("email_verified_at = NULL").
		Set("updated_at = now()").
		Where("id = ?", accountID).
		Where("email IS DISTINCT FROM ?", email).
		Returning("id").
		Scan(ctx, &changed)
	if pgerr.UniqueViolationOn(err, "accounts_email_unique") {
		return ErrAccountEmailTaken
	}
	if err != nil || len(changed) == 0 {
		return err
	}
	return TouchAccountMembers(ctx, db, accountID)
}
