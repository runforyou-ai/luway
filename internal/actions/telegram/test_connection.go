//go:build server

package telegram

import (
	"context"
	"strings"

	"github.com/runforyou-ai/luway/internal/integration/telegram"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// TestConnectionAction 测试 Telegram 草稿 Token。
type TestConnectionAction struct {
	db     *bun.DB
	runner *connectiontest.Runner
	api    telegram.BotAPI
}

// NewTestConnectionAction 创建 Telegram 连接测试操作。
func NewTestConnectionAction(db *bun.DB, runner *connectiontest.Runner, api telegram.BotAPI) *TestConnectionAction {
	return &TestConnectionAction{db: db, runner: runner, api: api}
}

// Execute 校验渠道和 Token，并且只调用 getMe。
func (a *TestConnectionAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, input ConnectionTestInput) error {
	input.BotToken = strings.TrimSpace(input.BotToken)
	if _, err := loadChannelDetail(ctx, a.db, identity.Workspace.ID, channelID, false); err != nil {
		return err
	}
	if _, err := runTelegramGetMe(ctx, a.runner, a.api, input.BotToken); err != nil {
		return err
	}
	return nil
}
