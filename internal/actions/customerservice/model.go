//go:build server

package customerservice

import (
	"context"
	"errors"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// LoadModel 读取设置引用的指定用途模型；未设置或模型已不可用时返回 nil。
func LoadModel(ctx context.Context, db bun.IDB, workspaceID string, modelID *string, usage domain.AIModelUsage) (*aimodel.Model, error) {
	if modelID == nil {
		return nil, nil
	}
	model, err := aimodel.Resolve(ctx, db, workspaceID, *modelID, usage)
	if errors.Is(err, aimodel.ErrUnavailable) {
		return nil, nil
	}
	return model, err
}
