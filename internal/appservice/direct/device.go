//go:build server

package direct

import (
	"context"
	"errors"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	deviceaction "github.com/runforyou-ai/luway/internal/actions/device"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// deviceOps 持有本机设备的 Action 和 Query。
type deviceOps struct {
	registerDevice      *deviceaction.RegisterDeviceAction
	listDevices         *deviceaction.ListDevicesQuery
	revokeDevice        *deviceaction.RevokeDeviceAction
	deviceAuthenticator *deviceaction.AuthenticateDeviceAction
	reportLocalAgents   *deviceaction.ReportLocalAgentsAction
}

// newDeviceOps 创建本机设备的业务实现依赖。
func newDeviceOps(db *bun.DB) deviceOps {
	return deviceOps{
		registerDevice:      deviceaction.NewRegisterDeviceAction(db),
		listDevices:         deviceaction.NewListDevicesQuery(db),
		revokeDevice:        deviceaction.NewRevokeDeviceAction(db),
		deviceAuthenticator: deviceaction.NewAuthenticateDeviceAction(db),
		reportLocalAgents:   deviceaction.NewReportLocalAgentsAction(db),
	}
}

// RegisterDevice 注册当前用户的本机设备。
func (o *directOperations) RegisterDevice(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.DeviceRegistrationInput) (appservice.Device, error) {
	record, err := o.registerDevice.Execute(ctx, identity, deviceaction.RegisterInput{
		InstallID: input.InstallID, Name: input.Name, Platform: domain.DevicePlatform(input.Platform),
	})
	if err != nil {
		return appservice.Device{}, o.deviceError(meta, err, i18n.ErrorDeviceRegisterFailed)
	}
	return deviceFromAction(*record), nil
}

// ListDevices 返回当前用户已注册的设备。
func (o *directOperations) ListDevices(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.DeviceList, error) {
	records, err := o.listDevices.Execute(ctx, identity)
	if err != nil {
		return appservice.DeviceList{}, o.deviceError(meta, err, i18n.ErrorDeviceListFailed)
	}
	devices := make([]appservice.Device, 0, len(records))
	for _, record := range records {
		devices = append(devices, deviceFromAction(record))
	}
	return appservice.DeviceList{Devices: devices}, nil
}

// RevokeDevice 撤销当前用户的设备。
func (o *directOperations) RevokeDevice(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, deviceID string) error {
	if err := o.revokeDevice.Execute(ctx, identity, deviceID); err != nil {
		return o.deviceError(meta, err, i18n.ErrorDeviceRevokeFailed)
	}
	return nil
}

// deviceError 转换本机设备操作错误。设备注册信息由客户端程序上报，校验失败按注册失败收敛。
func (o *directOperations) deviceError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if mapped := commonActionError(meta, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, deviceaction.ErrNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorDeviceNotFound)
	}
	if errors.Is(err, chatstate.ErrConversationNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	return appservice.FailedError(meta, failureKey, err)
}

// deviceFromAction 转换设备输出。
func deviceFromAction(input deviceaction.Record) appservice.Device {
	localAgents := make([]appservice.LocalAgentKind, 0, len(input.LocalAgents))
	for _, kind := range input.LocalAgents {
		localAgents = append(localAgents, appservice.LocalAgentKind(kind))
	}
	return appservice.Device{
		ID: input.ID, Name: input.Name, Platform: appservice.DevicePlatform(input.Platform), LocalAgents: localAgents,
		CreatedAt: input.CreatedAt, UpdatedAt: input.UpdatedAt,
	}
}
