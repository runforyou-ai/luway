//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestChannelIndependentUpdates 验证两个页签的交错更新各自保留另一组字段。
func TestChannelIndependentUpdates(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	action := channelaction.NewUpdateMessageChannelAction(f.db)
	basics := channelaction.MessageChannelBasicsInput{Name: "接待渠道新名称", Description: "更新后的说明", DefaultLocale: domain.CustomerLocaleEnglishUnitedStates}
	reception := channelaction.MessageChannelReceptionInput{
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: f.member.OrganizationIdentity.ID},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	}
	// 同时发起两个独立字段更新，提交顺序由数据库锁调度决定。
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; _, err := action.ExecuteBasics(ctx, f.owner, f.channelID, basics); results <- err }()
	go func() {
		<-start
		_, err := action.ExecuteReception(ctx, f.owner, f.channelID, reception)
		results <- err
	}()
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	channel := &servermodels.Channel{}
	if err := f.db.NewSelect().Model(channel).Where("c.id = ?", f.channelID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if channel.Name != basics.Name || channel.Description == nil || *channel.Description != basics.Description || channel.DefaultLocale != string(basics.DefaultLocale) {
		t.Fatalf("基础字段被覆盖: %#v", channel)
	}
	if channel.InitialRoutingTargetID == nil || *channel.InitialRoutingTargetID != reception.NewConversationTarget.ID {
		t.Fatalf("接待目标被覆盖: %#v", channel)
	}
	// 两个入口都以当前身份的企业边界定位记录。
	foreign := newCustomerReadFixture(t)
	if _, err := action.ExecuteBasics(ctx, foreign.owner, f.channelID, basics); !errors.Is(err, channelaction.ErrNotFound) {
		t.Fatalf("跨企业基础更新: %v", err)
	}
	if _, err := action.ExecuteReception(ctx, foreign.owner, f.channelID, reception); !errors.Is(err, channelaction.ErrNotFound) {
		t.Fatalf("跨企业接待更新: %v", err)
	}
}
