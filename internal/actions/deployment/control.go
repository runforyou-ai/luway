//go:build server

package deployment

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
	// SyncLicenseActionName 是向 control 登记实例并拉取授权的任务 Action 名称。
	SyncLicenseActionName = "deployment.sync_license"
	// SyncLicenseScheduleKey 是每天与 control 同步一次的定时计划标识。
	SyncLicenseScheduleKey = "instance-license-sync"
)

// SyncLicenseEnqueueOptions 是投递与 control 同步任务的选项：同一时刻最多一个待执行的同步，失败时按任务运行时的退避重试。
var SyncLicenseEnqueueOptions = servertask.EnqueueOptions{Queue: "maintenance", MaxAttempts: 5, IdempotencyKey: "instance-license-sync"}

var (
	// ErrActivationCodeInvalid 表示激活码不存在、不属于本产品或授权已到期。
	ErrActivationCodeInvalid = errors.New("activation code invalid")
	// ErrActivationInstanceMismatch 表示激活码已绑定其他实例，或本实例已绑定其他授权。
	ErrActivationInstanceMismatch = errors.New("activation code belongs to another instance or the instance holds another license")
	// ErrInstanceKeyMismatch 表示 control 登记的实例公钥与本实例签名私钥不一致。
	ErrInstanceKeyMismatch = errors.New("instance key does not match the key registered in control")
	// ErrLicenseNotIssued 表示 control 中没有适用于本实例的授权。
	ErrLicenseNotIssued = errors.New("control has no license for the instance")
	// ErrControlUnavailable 表示无法连接 control 或 control 暂时不可用。
	ErrControlUnavailable = errors.New("control unavailable")
)

// SyncLicenseInput 是与 control 同步任务的输入。
type SyncLicenseInput struct{}

// ControlIdentity 读取实例在 control 中的身份；部署尚未完成首次安装时返回 ErrNotInstalled。
func ControlIdentity(ctx context.Context, db bun.IDB) (control.Identity, error) {
	deployment, err := Load(ctx, db)
	if err != nil {
		return control.Identity{}, err
	}
	return control.Identity{InstanceID: deployment.InstanceID, PrivateKey: ed25519.NewKeyFromSeed(deployment.InstancePrivateKey)}, nil
}

// ResetInstance 为部署生成新的实例标识与签名私钥并删除本地授权，返回新实例标识。
func ResetInstance(ctx context.Context, db *bun.DB) (string, error) {
	var instanceID string
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		deployment, err := Lock(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model((*servermodels.InstanceLicense)(nil)).
			Where("instance_id = ?", deployment.InstanceID).
			Exec(ctx); err != nil {
			return err
		}
		return tx.NewUpdate().Model((*servermodels.Deployment)(nil)).
			Set("instance_id = uuidv7()").
			Set("instance_private_key = gen_random_bytes(32)").
			Set("updated_at = now()").
			Where("instance_id = ?", deployment.InstanceID).
			Returning("instance_id").
			Scan(ctx, &instanceID)
	})
	return instanceID, err
}

// OnlineLicenseAction 经 control 激活与同步实例授权。
type OnlineLicenseAction struct {
	db      *bun.DB
	keys    license.Keys
	control *control.Client
}

// NewOnlineLicenseAction 创建在线授权操作，keys 是用于验签的 control 签名公钥。
func NewOnlineLicenseAction(db *bun.DB, keys license.Keys, client *control.Client) *OnlineLicenseAction {
	return &OnlineLicenseAction{db: db, keys: keys, control: client}
}

// Activate 用激活码向 control 换取本实例的授权码并保存为实例授权。
func (a *OnlineLicenseAction) Activate(ctx context.Context, operator *servermodels.AccountIdentity, activationCode string) (License, error) {
	code, err := a.control.Activate(ctx, strings.TrimSpace(activationCode))
	if err != nil {
		return License{}, controlError(err)
	}
	return a.store(ctx, operator, code)
}

// Sync 由部署管理员立即向 control 登记实例并拉取授权；control 没有授权、授权码已到期或不晚于本地授权时，本地授权有效则返回本地授权。
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
	if current.Status == domain.LicenseStatusActive {
		return current, nil
	}
	return License{}, err
}

// SyncTask 是与 control 同步的后台任务：部署尚未完成首次安装、control 中没有授权或授权码已到期、不晚于本地授权时保留本地授权。
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

// fetch 登记实例后拉取 control 中本实例当前的授权码。
func (a *OnlineLicenseAction) fetch(ctx context.Context) (string, error) {
	if err := a.control.Register(ctx); err != nil {
		return "", controlError(err)
	}
	code, err := a.control.License(ctx)
	if err != nil {
		return "", controlError(err)
	}
	return code, nil
}

// store 验签 control 返回的授权码并保存，授权码不晚于当前授权时返回当前授权。
func (a *OnlineLicenseAction) store(ctx context.Context, operator *servermodels.AccountIdentity, code string) (License, error) {
	claims, err := license.Parse(code, a.keys)
	if err != nil {
		return License{}, fmt.Errorf("%w: %w", ErrLicenseInvalid, err)
	}
	output, err := storeLicense(ctx, a.db, operator, claims)
	if errors.Is(err, ErrLicenseSuperseded) {
		return NewLicenseQuery(a.db).Execute(ctx)
	}
	return output, err
}

// controlError 把 control 客户端错误转换为部署授权错误。
func controlError(err error) error {
	for source, target := range map[error]error{
		control.ErrActivationCodeInvalid: ErrActivationCodeInvalid,
		control.ErrInstanceMismatch:      ErrActivationInstanceMismatch,
		control.ErrInstanceKeyMismatch:   ErrInstanceKeyMismatch,
		control.ErrLicenseNotFound:       ErrLicenseNotIssued,
		control.ErrUnavailable:           ErrControlUnavailable,
	} {
		if errors.Is(err, source) {
			return fmt.Errorf("%w: %w", target, err)
		}
	}
	return err
}
