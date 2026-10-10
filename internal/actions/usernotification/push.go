//go:build server

package usernotification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/actions/notificationtask"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/control"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// 官方推送中继接受的标题与正文最大字符数。
const (
	pushTitleMaxRunes = 200
	pushBodyMaxRunes  = 1000
)

// pushDeviceGroup 是一个账号在同一应用与平台上已登录的推送设备。
type pushDeviceGroup struct {
	app       string
	platform  domain.PushPlatform
	deviceIDs []string
}

// loadPushDevices 读取账号未过期登录会话上的推送设备，按账号返回各应用与平台的设备分组。
func loadPushDevices(ctx context.Context, db bun.IDB, accountIDs []string) (map[string][]pushDeviceGroup, error) {
	var sessions []servermodels.AccountSession
	if err := db.NewSelect().Model(&sessions).
		Column("account_id", "push_app", "push_platform", "push_device_id").
		Where("account_id IN (?) AND push_device_id IS NOT NULL AND expires_at > now()", bun.List(accountIDs)).
		OrderExpr("account_id, push_app, push_platform, push_device_id").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load push devices: %w", err)
	}
	groups := map[string][]pushDeviceGroup{}
	for _, session := range sessions {
		app, platform := *session.PushApp, domain.PushPlatform(*session.PushPlatform)
		current := groups[session.AccountID]
		if count := len(current); count > 0 && current[count-1].app == app && current[count-1].platform == platform {
			current[count-1].deviceIDs = append(current[count-1].deviceIDs, *session.PushDeviceID)
			continue
		}
		groups[session.AccountID] = append(current, pushDeviceGroup{app: app, platform: platform, deviceIDs: []string{*session.PushDeviceID}})
	}
	return groups, nil
}

// Pusher 执行离线推送任务，把用户通知经 control 推送到接收账号已登录的移动设备。
type Pusher struct {
	db      *bun.DB
	inbox   *inbox.LoadInboxQuery
	control *control.Client
}

// NewPusher 创建离线推送任务执行器。
func NewPusher(db *bun.DB, client *control.Client) *Pusher {
	return &Pusher{db: db, inbox: inbox.NewLoadInboxQuery(db), control: client}
}

// Push 在接收成员仍可接收通知且仍能阅读该会话时，把通知经 control 推送到任务设备中仍登记在其未过期登录会话上的部分；推送无法送达时记录警告并结束，control 暂时不可用时返回错误由任务重试。
func (p *Pusher) Push(ctx context.Context, input notificationtask.PushInput) error {
	var recipients []recipient
	if err := recipientsQuery(p.db, input.WorkspaceID).Where("u.id = ?", input.UserID).Scan(ctx, &recipients); err != nil {
		return fmt.Errorf("load push recipient: %w", err)
	}
	if len(recipients) == 0 {
		return nil
	}
	conversations, err := p.inbox.ReadByIDs(ctx, recipients[0].identity(), []string{input.ConversationID}, nil)
	if err != nil {
		return fmt.Errorf("read push conversation: %w", err)
	}
	if conversations[0].Conversation == nil {
		return nil
	}
	devices, err := loadPushDevices(ctx, p.db, []string{input.AccountID})
	if err != nil {
		return err
	}
	var deviceIDs []string
	for _, group := range devices[input.AccountID] {
		if group.app != input.App || group.platform != input.Platform {
			continue
		}
		for _, deviceID := range group.deviceIDs {
			if slices.Contains(input.DeviceIDs, deviceID) {
				deviceIDs = append(deviceIDs, deviceID)
			}
		}
	}
	if len(deviceIDs) == 0 {
		return nil
	}
	err = p.control.Push(ctx, control.PushRequest{
		NotificationID: input.NotificationID, App: input.App, Platform: string(input.Platform), DeviceIDs: deviceIDs,
		Title: str.Substr(input.Title, 0, pushTitleMaxRunes), Body: str.Substr(input.Body, 0, pushBodyMaxRunes),
		Data: map[string]string{"workspaceId": input.WorkspaceID, "conversationId": input.ConversationID, "view": string(input.View)},
	})
	if errors.Is(err, control.ErrPushUndeliverable) {
		slog.WarnContext(ctx, "离线推送无法送达", "notification_id", input.NotificationID, "app", input.App, "platform", input.Platform, "error", err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("push notification: %w", err)
	}
	return nil
}
