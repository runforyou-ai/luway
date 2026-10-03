//go:build server

// Package commerce 维护平台与商业服务的配对，读取商业服务变更源并应用工作区权益与积分充值订单。
package commerce

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"time"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/commerce"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

var (
	// ErrPairingCodeInvalid 表示配对码格式无效。
	ErrPairingCodeInvalid = errors.New("commerce pairing code invalid")
	// ErrPairingRejected 表示商业服务拒绝配对，如令牌已使用、已过期或实例不匹配。
	ErrPairingRejected = errors.New("commerce pairing rejected")
	// ErrUnavailable 表示无法连接商业服务或商业服务暂时不可用。
	ErrUnavailable = errors.New("commerce unavailable")
	// ErrNotPaired 表示平台尚未与商业服务配对，或商业服务中本服务器未配对。
	ErrNotPaired = errors.New("commerce not paired")
	// ErrInvalidData 表示商业服务的响应或变更不符合接口约定。
	ErrInvalidData = errors.New("commerce data invalid")
)

// Pairing 定义平台与商业服务的配对状态与最近一次读取变更源的结果；最近一次读取成功时 Failure 为空。
type Pairing struct {
	URL            string
	ServiceID      string
	PairedAt       time.Time
	ChangeSequence int64
	SyncedAt       *time.Time
	FailedAt       *time.Time
	Failure        *domain.CommerceSyncFailure
}

// Identity 读取服务器在商业服务中的身份；平台尚未完成首次安装时返回 platform.ErrNotInstalled。
func Identity(ctx context.Context, db bun.IDB) (commerce.Identity, error) {
	platform, err := platformaction.Load(ctx, db)
	if err != nil {
		return commerce.Identity{}, err
	}
	return commerce.Identity{ServerID: platform.ServerID, PrivateKey: ed25519.NewKeyFromSeed(platform.ServerPrivateKey)}, nil
}

// PairingQuery 读取平台与商业服务的配对。
type PairingQuery struct {
	db *bun.DB
}

// NewPairingQuery 创建配对查询。
func NewPairingQuery(db *bun.DB) *PairingQuery {
	return &PairingQuery{db: db}
}

// Execute 返回当前服务器标识的配对，未配对时返回 nil。
func (q *PairingQuery) Execute(ctx context.Context) (*Pairing, error) {
	pairing, err := loadPairing(ctx, q.db.NewSelect())
	if errors.Is(err, ErrNotPaired) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	output := pairingFromModel(pairing)
	return &output, nil
}

// PairAction 用商业服务的配对码完成配对或替换现有配对。
type PairAction struct {
	db       *bun.DB
	client   *commerce.Client
	enqueuer servertask.TxEnqueuer
}

// NewPairAction 创建配对操作。
func NewPairAction(db *bun.DB, client *commerce.Client, enqueuer servertask.TxEnqueuer) *PairAction {
	return &PairAction{db: db, client: client, enqueuer: enqueuer}
}

// Execute 向商业服务确认配对码后保存商业服务地址、标识与公钥，删除原配对与已应用的权益，并投递从头读取变更源的任务。
func (a *PairAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, code string) (Pairing, error) {
	parsed, err := commerce.ParsePairingCode(code)
	if err != nil {
		return Pairing{}, fmt.Errorf("%w: %w", ErrPairingCodeInvalid, err)
	}
	if err := a.client.ConfirmPairing(ctx, parsed); err != nil {
		return Pairing{}, clientError(err)
	}
	var output Pairing
	err = a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := platformaction.LockAdmin(ctx, tx, operator); err != nil {
			return err
		}
		platform, err := platformaction.Lock(ctx, tx)
		if err != nil {
			return err
		}
		if err := deletePairing(ctx, tx); err != nil {
			return err
		}
		pairing := &servermodels.CommercePairing{
			ServerID: platform.ServerID, URL: parsed.URL, ServiceID: parsed.ServiceID, PublicKey: parsed.PublicKey,
		}
		if _, err := tx.NewInsert().Model(pairing).
			Column("server_id", "url", "service_id", "public_key").
			Returning("*").
			Exec(ctx); err != nil {
			return fmt.Errorf("save commerce pairing: %w", err)
		}
		output = pairingFromModel(pairing)
		_, err = a.enqueuer.EnqueueIn(ctx, tx, SyncChangesActionName, SyncChangesInput{}, SyncChangesEnqueueOptions)
		return err
	})
	return output, err
}

// UnpairAction 解除平台与商业服务的配对。
type UnpairAction struct {
	db *bun.DB
}

// NewUnpairAction 创建解除配对操作。
func NewUnpairAction(db *bun.DB) *UnpairAction {
	return &UnpairAction{db: db}
}

// Execute 删除配对与已应用的工作区权益；已入账的充值积分保留。
func (a *UnpairAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity) error {
	return a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := platformaction.LockAdmin(ctx, tx, operator); err != nil {
			return err
		}
		if _, err := platformaction.Lock(ctx, tx); err != nil {
			return err
		}
		return deletePairing(ctx, tx)
	})
}

// deletePairing 在事务内删除配对与全部工作区权益。
func deletePairing(ctx context.Context, tx bun.Tx) error {
	if _, err := tx.NewDelete().Model((*servermodels.CommercePairing)(nil)).Where("true").Exec(ctx); err != nil {
		return fmt.Errorf("delete commerce pairing: %w", err)
	}
	if _, err := tx.NewDelete().Model((*servermodels.WorkspaceEntitlement)(nil)).Where("true").Exec(ctx); err != nil {
		return fmt.Errorf("delete workspace entitlements: %w", err)
	}
	return nil
}

// loadPairing 按给定查询读取当前服务器标识的配对，未配对时返回 ErrNotPaired。
func loadPairing(ctx context.Context, query *bun.SelectQuery) (*servermodels.CommercePairing, error) {
	pairing := &servermodels.CommercePairing{}
	err := query.Model(pairing).
		Where("cp.server_id = (SELECT pf.server_id FROM platforms AS pf LIMIT 1)").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotPaired
	}
	if err != nil {
		return nil, fmt.Errorf("read commerce pairing: %w", err)
	}
	return pairing, nil
}

// pairingFromModel 把配对记录转换为配对状态。
func pairingFromModel(pairing *servermodels.CommercePairing) Pairing {
	var failure *domain.CommerceSyncFailure
	if pairing.Failure != "" {
		value := domain.CommerceSyncFailure(pairing.Failure)
		failure = &value
	}
	return Pairing{
		URL: pairing.URL, ServiceID: pairing.ServiceID, PairedAt: pairing.CreatedAt, ChangeSequence: pairing.ChangeSequence,
		SyncedAt: pairing.SyncedAt, FailedAt: pairing.FailedAt, Failure: failure,
	}
}

// clientError 把商业服务客户端错误转换为配对错误。
func clientError(err error) error {
	for source, target := range map[error]error{
		commerce.ErrPairingRejected: ErrPairingRejected,
		commerce.ErrNotPaired:       ErrNotPaired,
		commerce.ErrUnavailable:     ErrUnavailable,
		commerce.ErrInvalidResponse: ErrInvalidData,
	} {
		if errors.Is(err, source) {
			return fmt.Errorf("%w: %w", target, err)
		}
	}
	return err
}
