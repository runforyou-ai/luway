package server

import (
	"context"
	"encoding/json"
	"uuid"

	"github.com/uptrace/bun"
)

// Transaction 返回当前 RunInTx 的数据库事务。
func Transaction(ctx context.Context) bun.Tx {
	hooks, _ := ctx.Value(afterCommitKey{}).(*afterCommitHooks)
	if hooks == nil {
		panic("serverstorage: transaction required")
	}
	return hooks.bunTx
}

// PutOutbox 在当前事务中持久登记一个有序消息批次。
func PutOutbox(ctx context.Context, kind string, payload any) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	id := uuid.NewV7().String()
	_, err = Transaction(ctx).NewRaw("INSERT INTO message_outbox (id, kind, payload) VALUES (?, ?, ?::jsonb)", id, kind, string(data)).Exec(ctx)
	return id, err
}
