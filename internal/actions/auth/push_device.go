//go:build server

package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrPushDeviceInvalid 表示推送应用标识、平台或设备编号无效。
var ErrPushDeviceInvalid = errors.New("push device invalid")

// PushDeviceInput 定义移动设备的推送目标：注册设备的客户端应用标识、设备平台与推送服务分配的设备编号。
type PushDeviceInput struct {
	App      string
	Platform domain.PushPlatform
	DeviceID string
}

// SetPushDeviceAction 把移动设备的推送目标记到当前登录会话。
type SetPushDeviceAction struct {
	db *bun.DB
}

// NewSetPushDeviceAction 创建登记推送设备操作。
func NewSetPushDeviceAction(db *bun.DB) *SetPushDeviceAction {
	return &SetPushDeviceAction{db: db}
}

// Execute 把推送目标写入当前登录会话；同一设备此前登记在其他会话上时从那些会话上移除，设备只推送给最近在其上登录的会话；当前会话已失效时返回 ErrIdentityNotFound。
func (a *SetPushDeviceAction) Execute(ctx context.Context, identity *servermodels.AccountIdentity, input PushDeviceInput) error {
	app, deviceID := strings.TrimSpace(input.App), strings.TrimSpace(input.DeviceID)
	// 推送服务以逗号分隔目标设备，设备编号不得含逗号。
	if app == "" || utf8.RuneCountInString(app) > 255 || !input.Platform.Valid() ||
		deviceID == "" || utf8.RuneCountInString(deviceID) > 128 || strings.Contains(deviceID, ",") {
		return ErrPushDeviceInvalid
	}
	return serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		// 同一设备的登记按提交顺序串行执行。
		if err := serverstorage.XactLock(ctx, tx, serverstorage.LockPushDevice, serverstorage.LockKey(app, string(input.Platform), deviceID)); err != nil {
			return err
		}
		var sessionID string
		err := tx.NewSelect().Model((*servermodels.AccountSession)(nil)).Column("id").
			Where("id = ? AND account_id = ? AND expires_at > now()", identity.Session.ID, identity.Account.ID).
			For("UPDATE").Scan(ctx, &sessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrIdentityNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*servermodels.AccountSession)(nil)).
			Set("push_app = NULL, push_platform = NULL, push_device_id = NULL").
			Where("push_app = ? AND push_platform = ? AND push_device_id = ? AND id <> ?", app, input.Platform, deviceID, sessionID).
			Exec(ctx); err != nil {
			return err
		}
		_, err = tx.NewUpdate().Model((*servermodels.AccountSession)(nil)).
			Set("push_app = ?, push_platform = ?, push_device_id = ?", app, input.Platform, deviceID).
			Where("id = ?", sessionID).
			Exec(ctx)
		return err
	})
}
