//go:build server

package license

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	licensecode "github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/integration/supportplane"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// RegisterDeploymentActionName 是首次安装登记部署的后台任务。
const RegisterDeploymentActionName = "platform.register_deployment"

// RegisterDeploymentEnqueueOptions 配置首次登记的重试。
var RegisterDeploymentEnqueueOptions = servertask.EnqueueOptions{Queue: servertask.QueueMaintenance, MaxAttempts: 5, IdempotencyKey: "register-deployment"}

// RegisterDeploymentInput 是首次登记任务的输入。
type RegisterDeploymentInput struct{}

var (
	ErrActivationCodeInvalid        = errors.New("activation code invalid")
	ErrActivationDeploymentMismatch = errors.New("activation code belongs to another deployment")
	ErrDeploymentKeyMismatch        = errors.New("deployment key mismatch")
	ErrControlUnavailable           = errors.New("control unavailable")
)

// ControlIdentity 读取部署身份与签名私钥。
func ControlIdentity(ctx context.Context, db bun.IDB) (supportplane.Identity, error) {
	platform, err := platformaction.Load(ctx, db)
	if err != nil {
		return supportplane.Identity{}, err
	}
	return supportplane.Identity{DeploymentID: platform.DeploymentID, PrivateKey: ed25519.NewKeyFromSeed(platform.DeploymentPrivateKey)}, nil
}

// ResetDeploymentID 为平台生成新的部署标识与签名私钥，删除本地授权，并清空首次登记时间，返回新部署标识。
func ResetDeploymentID(ctx context.Context, db *bun.DB) (string, error) {
	var deploymentID string
	err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		platform, err := platformaction.Lock(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model((*servermodels.License)(nil)).
			Where("deployment_id = ?", platform.DeploymentID).
			Exec(ctx); err != nil {
			return err
		}
		return tx.NewUpdate().Model((*servermodels.Platform)(nil)).
			Set("deployment_id = uuidv7()").
			Set("deployment_private_key = gen_random_bytes(32)").
			Set("control_registered_at = NULL").
			Where("deployment_id = ?", platform.DeploymentID).
			Returning("deployment_id").
			Scan(ctx, &deploymentID)
	})
	return deploymentID, err
}

// OnlineLicenseAction 完成首次部署登记与在线激活。
type OnlineLicenseAction struct {
	db      *bun.DB
	keys    licensecode.Keys
	control *supportplane.Client
	state   platformaction.DeploymentReloader
}

// NewOnlineLicenseAction 创建部署登记与在线激活操作。
func NewOnlineLicenseAction(db *bun.DB, keys licensecode.Keys, client *supportplane.Client, state platformaction.DeploymentReloader) *OnlineLicenseAction {
	return &OnlineLicenseAction{db: db, keys: keys, control: client, state: state}
}

// Register 在首次安装完成后登记部署身份。
func (a *OnlineLicenseAction) Register(ctx context.Context, _ RegisterDeploymentInput) error {
	platform, err := platformaction.Load(ctx, a.db)
	if err != nil {
		return err
	}
	if platform.ControlRegisteredAt != nil {
		return nil
	}
	if err := a.control.Register(ctx); err != nil {
		slog.WarnContext(ctx, "部署登记失败", "error", err)
		return controlError(err)
	}
	_, err = a.db.NewUpdate().Model((*servermodels.Platform)(nil)).Set("control_registered_at = now()").Where("deployment_id = ?", platform.DeploymentID).Exec(ctx)
	if err == nil {
		slog.InfoContext(ctx, "部署已登记", "deployment_id", platform.DeploymentID)
	}
	return err
}

// Activate 用激活码取得授权并保存生效能力。
func (a *OnlineLicenseAction) Activate(ctx context.Context, operator *servermodels.AccountIdentity, activationCode string) (License, error) {
	code, err := a.control.Activate(ctx, strings.TrimSpace(activationCode))
	if err != nil {
		return License{}, controlError(err)
	}
	claims, err := licensecode.Parse(code, a.keys)
	if err != nil {
		return License{}, fmt.Errorf("%w: %w", ErrLicenseInvalid, err)
	}
	current, err := storeLicense(ctx, a.db, a.state, operator, claims)
	if err != nil {
		return License{}, err
	}
	_, err = a.db.NewUpdate().Model((*servermodels.Platform)(nil)).Set("control_registered_at = now()").Where("deployment_id = ?", claims.DeploymentID).Exec(ctx)
	return current, err
}

// controlError 把 Control 错误转换为授权操作错误。
func controlError(err error) error {
	for source, target := range map[error]error{
		supportplane.ErrActivationCodeInvalid: ErrActivationCodeInvalid,
		supportplane.ErrDeploymentMismatch:    ErrActivationDeploymentMismatch,
		supportplane.ErrDeploymentKeyMismatch: ErrDeploymentKeyMismatch,
		supportplane.ErrUnavailable:           ErrControlUnavailable,
	} {
		if errors.Is(err, source) {
			return fmt.Errorf("%w: %w", target, err)
		}
	}
	return err
}
