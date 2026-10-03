//go:build server

package commerce

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"time"

	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/httpsig"
	"github.com/uptrace/bun"
)

// ErrNotificationUnauthorized 表示通知没有使用已配对商业服务的有效签名。
var ErrNotificationUnauthorized = errors.New("commerce notification unauthorized")

// notificationMaxSkew 是通知签名时间与本机时间允许的最大偏差。
const notificationMaxSkew = 300 * time.Second

// ReceiveNotificationAction 接收商业服务的变更通知。
type ReceiveNotificationAction struct {
	db       *bun.DB
	enqueuer servertask.Enqueuer
}

// NewReceiveNotificationAction 创建接收通知操作。
func NewReceiveNotificationAction(db *bun.DB, enqueuer servertask.Enqueuer) *ReceiveNotificationAction {
	return &ReceiveNotificationAction{db: db, enqueuer: enqueuer}
}

// Execute 用已配对商业服务的公钥验签通知后投递读取变更源的任务；通知不携带数据，重放只多触发一次读取，不登记 nonce。
func (a *ReceiveNotificationAction) Execute(ctx context.Context, request *http.Request, body []byte) error {
	pairing, err := loadPairing(ctx, a.db.NewSelect())
	if errors.Is(err, ErrNotPaired) {
		return ErrNotificationUnauthorized
	}
	if err != nil {
		return err
	}
	if err := httpsig.Verify(request, body, pairing.ServiceID, ed25519.PublicKey(pairing.PublicKey), time.Now(), notificationMaxSkew); err != nil {
		return fmt.Errorf("%w: %w", ErrNotificationUnauthorized, err)
	}
	_, err = a.enqueuer.Enqueue(ctx, SyncChangesActionName, SyncChangesInput{}, SyncChangesEnqueueOptions)
	return err
}
