//go:build server

package translation

import (
	"context"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ValidationTranslationModelInvalid 表示翻译模型不可用或不满足翻译用途。
const ValidationTranslationModelInvalid common.FieldCode = "TRANSLATION_MODEL_INVALID"

// LoadTranslationModelID 读取企业设置的翻译模型编号，未设置时返回 nil。
func LoadTranslationModelID(ctx context.Context, db bun.IDB, workspaceID string) (*string, error) {
	setting := &servermodels.CustomerServiceSetting{}
	if err := db.NewSelect().Model(setting).
		Column("translation_model_id").
		Where("css.workspace_id = ?", workspaceID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load translation model: %w", err)
	}
	return setting.TranslationModelID, nil
}

// GetTranslationSettings 返回当前企业的翻译模型编号，未设置时为 nil。
func GetTranslationSettings(ctx context.Context, db *bun.DB, identity *servermodels.Identity) (*string, error) {
	return LoadTranslationModelID(ctx, db, identity.Workspace.ID)
}

// UpdateTranslationSettings 校验并保存翻译模型编号：模型须满足翻译用途，nil 表示关闭翻译。
func UpdateTranslationSettings(ctx context.Context, db *bun.DB, identity *servermodels.Identity, modelID *string) (*string, error) {
	err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		setting := &servermodels.CustomerServiceSetting{WorkspaceID: identity.Workspace.ID}
		if modelID != nil {
			_, err := aimodel.Lock(ctx, tx, identity.Workspace.ID, *modelID, domain.AIModelUsageTranslation)
			if errors.Is(err, aimodel.ErrUnavailable) {
				return &common.FieldError{Fields: map[string]common.FieldCode{"modelId": ValidationTranslationModelInvalid}}
			}
			if err != nil {
				return fmt.Errorf("check translation model: %w", err)
			}
			setting.TranslationModelID = modelID
		}
		result, err := tx.NewUpdate().Model(setting).Column("translation_model_id").WherePK().Exec(ctx)
		if err != nil {
			return fmt.Errorf("save translation settings: %w", err)
		}
		if count, err := result.RowsAffected(); err != nil {
			return err
		} else if count != 1 {
			return fmt.Errorf("update translation settings affected %d rows", count)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return modelID, nil
}
