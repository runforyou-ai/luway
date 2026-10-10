//go:build server

package direct

import (
	"context"
	"strconv"

	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/appservice"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// GetInboxContext 读取锚点当前资格与同一快照内的列表邻域。
func (o *inboxOps) GetInboxContext(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.InboxContextInput) (appservice.InboxContext, error) {
	result, err := o.loadInbox.ReadContext(ctx, identity, inboxaction.ContextInput{
		Query:    inboxLoadInput(input.Query),
		AnchorID: input.AnchorID, AnchorCursor: input.AnchorCursor, BeforeLimit: input.BeforeLimit, AfterLimit: input.AfterLimit,
	})
	if err != nil {
		return appservice.InboxContext{}, inboxReadError(meta, err)
	}
	// 锚点与窗口一起解析头像，筛选外但可读的锚点仅放在独立结果中。
	summaries := result.Window.Conversations
	if result.Anchor.Conversation != nil {
		summaries = append(summaries, *result.Anchor.Conversation)
	}
	conversations, err := o.inboxConversationsFromActions(ctx, meta, identity, summaries)
	if err != nil {
		return appservice.InboxContext{}, err
	}
	anchor := appservice.InboxConversationResult{ID: input.AnchorID, Availability: appservice.InboxConversationUnavailable}
	if result.Anchor.Conversation != nil {
		anchor.Conversation = &conversations[len(conversations)-1]
		anchor.Availability = appservice.InboxConversationOutsideQuery
		if result.Anchor.MatchesQuery {
			anchor.Availability = appservice.InboxConversationMatching
		}
		conversations = conversations[:len(conversations)-1]
	}
	return appservice.InboxContext{Anchor: anchor, Window: appservice.InboxWindow{
		Conversations: conversations, StartCursor: result.Window.StartCursor, EndCursor: result.Window.EndCursor,
		PinOrderVersion: strconv.FormatInt(result.Window.PinOrderVersion, 10),
		HasBefore:       result.Window.HasBefore, HasAfter: result.Window.HasAfter,
	}}, nil
}

// ReadInboxWindow 按原始边界重读完整连续范围。
func (o *inboxOps) ReadInboxWindow(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.InboxWindowInput) (appservice.InboxWindow, error) {
	window, err := o.loadInbox.ReadWindow(ctx, identity, inboxaction.ReadWindowInput{
		Query:       inboxLoadInput(input.Query),
		StartCursor: input.StartCursor, EndCursor: input.EndCursor,
	})
	if err != nil {
		return appservice.InboxWindow{}, inboxReadError(meta, err)
	}
	conversations, err := o.inboxConversationsFromActions(ctx, meta, identity, window.Conversations)
	if err != nil {
		return appservice.InboxWindow{}, err
	}
	return appservice.InboxWindow{
		Conversations: conversations, StartCursor: window.StartCursor, EndCursor: window.EndCursor,
		PinOrderVersion: strconv.FormatInt(window.PinOrderVersion, 10), HasBefore: window.HasBefore, HasAfter: window.HasAfter,
	}, nil
}
