//go:build server

package aimodel

import (
	"context"
	"errors"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// LoadOptional 读取设置引用的指定用途模型；未设置或模型已不可用时返回 nil。
func LoadOptional(ctx context.Context, db bun.IDB, workspaceID string, modelID *string, usage domain.AIModelUsage) (*Model, error) {
	if modelID == nil {
		return nil, nil
	}
	model, err := Resolve(ctx, db, workspaceID, *modelID, usage)
	if errors.Is(err, ErrUnavailable) {
		return nil, nil
	}
	return model, err
}
