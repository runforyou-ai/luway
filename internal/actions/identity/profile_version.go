//go:build server

package identity

import (
	"context"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// profileVersionRow 表示身份资料版本写入后的用户账号。
type profileVersionRow struct {
	ID             string `bun:"id"`
	IdentityID     string `bun:"identity_id"`
	ProfileVersion int64  `bun:"profile_version"`
	Changed        bool   `bun:"changed"`
	DisplayChanged bool   `bun:"display_changed"`
}

// UpdateUserIdentity 更新真人企业身份，登录态可见字段实际变化时同句推进用户身份资料版本，登记本人通知与企业网站访客的接待状态变化通知，返回名称或头像是否实际变化；调用方需先锁定对应用户账号。
func UpdateUserIdentity(ctx context.Context, db bun.IDB, organizationID, identityID string, query *bun.UpdateQuery) (bool, error) {
	query = query.
		Where("oi.organization_id = ? AND oi.id = ? AND oi.type = ?", organizationID, identityID, domain.OrganizationIdentityTypeUser).
		Returning("new.organization_id, new.id, (old.display_name, old.avatar_file_id, old.handles_service_requests, old.work_status) IS DISTINCT FROM (new.display_name, new.avatar_file_id, new.handles_service_requests, new.work_status) AS changed, (old.display_name, old.avatar_file_id) IS DISTINCT FROM (new.display_name, new.avatar_file_id) AS display_changed")
	var rows []profileVersionRow
	if err := db.NewUpdate().With("identity_change", query).
		Model((*servermodels.User)(nil)).
		Set("profile_version = u.profile_version + 1").
		Where("(u.organization_id, u.identity_id) IN (SELECT organization_id, id FROM identity_change WHERE changed)").
		Returning("u.id, u.profile_version, (SELECT display_changed FROM identity_change) AS display_changed").
		Scan(ctx, &rows); err != nil {
		return false, err
	}
	if len(rows) > 0 {
		realtime.Notify(ctx, realtime.WebsiteReceptionChanged(organizationID))
	}
	displayChanged := false
	for _, row := range rows {
		realtime.Notify(ctx, realtime.UserIdentityProfileChanged(organizationID, row.ID, row.ProfileVersion))
		displayChanged = displayChanged || row.DisplayChanged
	}
	return displayChanged, nil
}

// UpdateUserAccount 执行单个用户账号更新，身份资料版本实际推进时登记本人通知并返回企业身份 ID；未命中账号时返回 sql.ErrNoRows。
func UpdateUserAccount(ctx context.Context, organizationID string, query *bun.UpdateQuery) (string, error) {
	var row profileVersionRow
	if err := query.Returning("new.id, new.identity_id, new.profile_version, new.profile_version <> old.profile_version AS changed").Scan(ctx, &row); err != nil {
		return "", err
	}
	if row.Changed {
		realtime.Notify(ctx, realtime.UserIdentityProfileChanged(organizationID, row.ID, row.ProfileVersion))
	}
	return row.IdentityID, nil
}

// UpdateUserAccounts 执行多个用户账号的批量更新，身份资料版本实际推进时逐个账号登记本人通知。
func UpdateUserAccounts(ctx context.Context, organizationID string, query *bun.UpdateQuery) error {
	var rows []profileVersionRow
	if err := query.Returning("new.id, new.identity_id, new.profile_version, new.profile_version <> old.profile_version AS changed").Scan(ctx, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		if row.Changed {
			realtime.Notify(ctx, realtime.UserIdentityProfileChanged(organizationID, row.ID, row.ProfileVersion))
		}
	}
	return nil
}
