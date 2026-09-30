//go:build server

package channel

import (
	"context"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// TestTelegramConnectionAction 测试 Telegram 草稿 Token。
type TestTelegramConnectionAction struct {
	db     *bun.DB
	runner *connectiontest.Runner
	api    telegram.BotAPI
}

// NewTestTelegramConnectionAction 创建 Telegram 连接测试操作。
func NewTestTelegramConnectionAction(db *bun.DB, runner *connectiontest.Runner, api telegram.BotAPI) *TestTelegramConnectionAction {
	return &TestTelegramConnectionAction{db: db, runner: runner, api: api}
}

// Execute 校验渠道和 Token，并且只调用 getMe。
func (a *TestTelegramConnectionAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, input TelegramChannelConnectionTestInput) error {
	if !common.ValidUUID(channelID) {
		return ErrNotFound
	}
	input, fields := normalizeTelegramConnectionTestInput(input)
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	if _, err := loadTelegramChannelDetail(ctx, a.db, identity.Organization.ID, channelID, false); err != nil {
		return err
	}
	if _, err := runTelegramGetMe(ctx, a.runner, a.api, input.BotToken); err != nil {
		return err
	}
	return nil
}
