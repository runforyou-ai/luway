//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	commerceaction "github.com/runforyou-ai/luway/internal/actions/commerce"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/actions/servicetimeout"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// newTestTasks 创建不连接 NATS 的任务运行时，并以空处理器注册 AI 聊天标题、个人 AI 员工记忆提取、客服处理周期分配、转人工承接、小结、待补知识起草与授权同步任务，供业务事务投递。
func newTestTasks(db *bun.DB) *servertask.Runtime {
	tasks := servertask.New(db, serverconfig.NATSConfig{})
	if err := tasks.Registry().RegisterJSON(serviceassignment.AssignActionName, func(context.Context, serviceassignment.AssignInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(serviceassignment.BackfillActionName, func(context.Context, serviceassignment.BackfillInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(agentrunaction.AgentChatTitleActionName, func(context.Context, agentrunaction.AgentChatTitleInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(agentrunaction.AgentMemoryActionName, func(context.Context, agentrunaction.AgentMemoryInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(agentrunaction.ReturnedHandoffActionName, func(context.Context, agentrunaction.ReturnedHandoffInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(servicetimeout.ProcessActionName, func(context.Context, servicetimeout.ProcessInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(servicesummary.SummarizeActionName, func(context.Context, servicesummary.SummarizeInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(servicesummary.ReviewActionName, func(context.Context, servicesummary.ReviewInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(servicesummary.HandoffSummaryActionName, func(context.Context, servicesummary.HandoffSummaryInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(servicesummary.ExtractContactProfileActionName, func(context.Context, servicesummary.ExtractContactProfileInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(channelaction.RefreshTelegramContactAvatarActionName, func(context.Context, channelaction.RefreshTelegramContactAvatarInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(knowledgegap.DraftActionName, func(context.Context, knowledgegap.DraftInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(commerceaction.SyncChangesActionName, func(context.Context, commerceaction.SyncChangesInput) error { return nil }); err != nil {
		panic(err)
	}
	if err := tasks.Registry().RegisterJSON(platformaction.SyncLicenseActionName, func(context.Context, platformaction.SyncLicenseInput) error { return nil }); err != nil {
		panic(err)
	}
	return tasks
}

// disableAutoAssignment 把企业内全部成员的最大接待量设为 0，使只验证路由去向的测试不触发自动分配。
func disableAutoAssignment(t *testing.T, db *bun.DB, organizationID string) {
	t.Helper()
	if _, err := db.NewUpdate().Table("users").Set("max_service_sessions = 0").Where("organization_id = ?", organizationID).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// newTestChannelStatusAction 创建渠道启停操作，Telegram 渠道使用不调用外部接口的机器人接口替身。
func newTestChannelStatusAction(db *bun.DB) *channelaction.UpdateMessageChannelStatusAction {
	return channelaction.NewUpdateMessageChannelStatusAction(db, channelaction.NewUpdateTelegramChannelStatusAction(db, connectiontest.NewRunner(time.Second), &telegramBotAPIFake{}))
}
