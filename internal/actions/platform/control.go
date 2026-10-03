//go:build server

package platform

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"

	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/control"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

const (
	// SyncLicenseActionName 是向 control 登记服务器并拉取授权的任务 Action 名称。
	SyncLicenseActionName = "platform.sync_license"
	// SyncLicenseScheduleKey 是每天与 control 同步一次的定时计划标识。
	SyncLicenseScheduleKey = "license-sync"
)

// SyncLicenseEnqueueOptions 是投递与 control 同步任务的选项：同一时刻最多一个待执行的同步，失败时按任务运行时的退避重试。
var SyncLicenseEnqueueOptions = servertask.EnqueueOptions{Queue: "maintenance", MaxAttempts: 5, IdempotencyKey: "license-sync"}

var (
	// ErrActivationCodeInvalid 表示激活码不存在、不属于本产品或授权已到期。
	ErrActivationCodeInvalid = errors.New("activation code invalid")
	// ErrActivationServerMismatch 表示激活码已绑定其他服务器，或本服务器已绑定其他授权。
	ErrActivationServerMismatch = errors.New("activation code belongs to another server or the server holds another license")
	// ErrServerKeyMismatch 表示 control 登记的服务器公钥与本服务器签名私钥不一致。
	ErrServerKeyMismatch = errors.New("server key does not match the key registered in control")
	// ErrLicenseNotIssued 表示 control 中没有适用于本服务器的授权。
	ErrLicenseNotIssued = errors.New("control has no license for the server")
	// ErrControlUnavailable 表示无法连接 control 或 control 暂时不可用。
	ErrControlUnavailable = errors.New("control unavailable")
)

// SyncLicenseInput 是与 control 同步任务的输入。
type SyncLicenseInput struct{}

// ControlIdentity 读取服务器在 control 中的身份；平台尚未完成首次安装时返回 ErrNotInstalled。
func ControlIdentity(ctx context.Context, db bun.IDB) (control.Identity, error) {
	platform, err := Load(ctx, db)
	if err != nil {
		return control.Identity{}, err
	}
	return control.Identity{ServerID: platform.ServerID, PrivateKey: ed25519.NewKeyFromSeed(platform.ServerPrivateKey)}, nil
}

// ResetServerID 为平台生成新的服务器标识与签名私钥并删除本地授权，返回新服务器标识。
func ResetServerID(ctx context.Context, db *bun.DB) (string, error) {
	var serverID string
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		platform, err := Lock(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model((*servermodels.License)(nil)).
			Where("server_id = ?", platform.ServerID).
			Exec(ctx); err != nil {
			return err
		}
		return tx.NewUpdate().Model((*servermodels.Platform)(nil)).
			Set("server_id = uuidv7()").
			Set("server_private_key = gen_random_bytes(32)").
			Set("updated_at = now()").
			Where("server_id = ?", platform.ServerID).
			Returning("server_id").
			Scan(ctx, &serverID)
	})
	return serverID, err
}

// OnlineLicenseAction 经 control 激活与同步授权。
type OnlineLicenseAction struct {
	db      *bun.DB
	keys    license.Keys
	control *control.Client
}

// NewOnlineLicenseAction 创建在线授权操作，keys 是用于验签的 control 签名公钥。
func NewOnlineLicenseAction(db *bun.DB, keys license.Keys, client *control.Client) *OnlineLicenseAction {
	return &OnlineLicenseAction{db: db, keys: keys, control: client}
}

// Activate 用激活码向 control 换取本服务器的授权码并保存为授权。
func (a *OnlineLicenseAction) Activate(ctx context.Context, operator *servermodels.AccountIdentity, activationCode string) (License, error) {
	code, err := a.control.Activate(ctx, strings.TrimSpace(activationCode))
	if err != nil {
		return License{}, controlError(err)
	}
	return a.store(ctx, operator, code)
}

// Sync 由平台管理员立即向 control 登记服务器并拉取授权；control 没有授权时只要本地有授权即返回本地授权，control 的授权码已到期或不晚于本地授权时本地授权有效则返回本地授权。
func (a *OnlineLicenseAction) Sync(ctx context.Context, operator *servermodels.AccountIdentity) (License, error) {
	code, err := a.fetch(ctx)
	if err == nil {
		var output License
		output, err = a.store(ctx, operator, code)
		if err == nil {
			return output, nil
		}
	}
	if !errors.Is(err, ErrLicenseNotIssued) && !errors.Is(err, ErrLicenseExpired) {
		return License{}, err
	}
	current, queryErr := NewLicenseQuery(a.db).Execute(ctx)
	if queryErr != nil {
		return License{}, queryErr
	}
	if current.Status == domain.LicenseStatusActive || (errors.Is(err, ErrLicenseNotIssued) && current.Status != domain.LicenseStatusNone) {
		return current, nil
	}
	return License{}, err
}

// SyncTask 是与 control 同步的后台任务：平台尚未完成首次安装、control 中没有授权或授权码已到期、不晚于本地授权时保留本地授权。
func (a *OnlineLicenseAction) SyncTask(ctx context.Context, _ SyncLicenseInput) error {
	if _, err := Load(ctx, a.db); errors.Is(err, ErrNotInstalled) {
		return nil
	} else if err != nil {
		return err
	}
	code, err := a.fetch(ctx)
	if errors.Is(err, ErrLicenseNotIssued) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = a.store(ctx, nil, code)
	if errors.Is(err, ErrLicenseExpired) {
		return nil
	}
	return err
}

// fetch 登记服务器后拉取 control 中本服务器当前的授权码；control 中查不到授权时在本地授权上记录首次查不到的时间。
func (a *OnlineLicenseAction) fetch(ctx context.Context) (string, error) {
	if err := a.control.Register(ctx); err != nil {
		return "", controlError(err)
	}
	code, err := a.control.License(ctx)
	if errors.Is(err, control.ErrLicenseNotFound) {
		if _, updateErr := a.db.NewUpdate().Model((*servermodels.License)(nil)).
			Set("control_missing_at = now()").
			Where("server_id = (SELECT pf.server_id FROM platforms AS pf LIMIT 1)").
			Where("control_missing_at IS NULL").
			Exec(ctx); updateErr != nil {
			return "", updateErr
		}
	}
	if err != nil {
		return "", controlError(err)
	}
	return code, nil
}

// store 验签 control 返回的授权码并保存，授权码不晚于当前授权时返回当前授权；授权码属于本服务器时清空 control 中查不到授权的记录。
func (a *OnlineLicenseAction) store(ctx context.Context, operator *servermodels.AccountIdentity, code string) (License, error) {
	claims, err := license.Parse(code, a.keys)
	if err != nil {
		return License{}, fmt.Errorf("%w: %w", ErrLicenseInvalid, err)
	}
	output, err := storeLicense(ctx, a.db, operator, claims)
	superseded := errors.Is(err, ErrLicenseSuperseded)
	// 授权码已保存、不晚于当前授权或已到期时都表明 control 中有本服务器的授权。
	if err == nil || superseded || errors.Is(err, ErrLicenseExpired) {
		if _, updateErr := a.db.NewUpdate().Model((*servermodels.License)(nil)).
			Set("control_missing_at = NULL").
			Where("server_id = ?", claims.ServerID).
			Where("control_missing_at IS NOT NULL").
			Exec(ctx); updateErr != nil {
			return License{}, updateErr
		}
		output.ControlMissingAt = nil
	}
	if superseded {
		return NewLicenseQuery(a.db).Execute(ctx)
	}
	return output, err
}

// controlError 把 control 客户端错误转换为平台授权错误。
func controlError(err error) error {
	for source, target := range map[error]error{
		control.ErrActivationCodeInvalid: ErrActivationCodeInvalid,
		control.ErrServerMismatch:        ErrActivationServerMismatch,
		control.ErrServerKeyMismatch:     ErrServerKeyMismatch,
		control.ErrLicenseNotFound:       ErrLicenseNotIssued,
		control.ErrUnavailable:           ErrControlUnavailable,
	} {
		if errors.Is(err, source) {
			return fmt.Errorf("%w: %w", target, err)
		}
	}
	return err
}
