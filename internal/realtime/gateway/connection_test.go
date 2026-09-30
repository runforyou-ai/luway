//go:build server

package gateway

import (
	"reflect"
	"testing"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
)

// TestConnectionQueue 验证发送队列按会话与种类保留最高版本并合并会话变化类别，失权事件不合并，溢出时清空队列并结束事件流。
func TestConnectionQueue(t *testing.T) {
	current := newConnection(New(nil, nil, "test", Options{QueueSize: 4}), func() {}, streamRoute{allowed: memberFrameTypes})
	current.send(protocol.ConversationChanged{ConversationID: "a", Version: 2, Changes: domain.ConversationChangeParticipants})
	current.send(protocol.ConversationChanged{ConversationID: "a", Version: 1, Changes: domain.ConversationChangeService})
	current.send(protocol.ConversationStateChanged{ConversationID: "a", Version: 5})
	current.send(protocol.ConversationRemoved{ConversationID: "a"})
	current.send(protocol.ConversationRemoved{ConversationID: "a"})
	current.send(protocol.ConversationChanged{ConversationID: "a", Version: 3, Changes: domain.ConversationChangeTimeline})
	want := []protocol.Frame{
		protocol.ConversationChanged{ConversationID: "a", Version: 3, Changes: domain.ConversationChangeTimeline | domain.ConversationChangeService | domain.ConversationChangeParticipants},
		protocol.ConversationStateChanged{ConversationID: "a", Version: 5},
		protocol.ConversationRemoved{ConversationID: "a"},
		protocol.ConversationRemoved{ConversationID: "a"},
	}
	if !reflect.DeepEqual(current.queue, want) {
		t.Fatalf("queue = %#v, want %#v", current.queue, want)
	}

	// 队列已满时新事件触发慢连接结束，结束后不再入队。
	current.send(protocol.IdentityProfileChanged{Version: 1})
	current.send(protocol.Ping{})
	if !current.closing || len(current.queue) != 0 {
		t.Fatalf("closing = %v, queue = %#v", current.closing, current.queue)
	}
}

// TestConnectionQueueTyping 验证输入状态按会话与发送者只保留最新一条，不同发送者分别入队。
func TestConnectionQueueTyping(t *testing.T) {
	current := newConnection(New(nil, nil, "test", Options{QueueSize: 4}), func() {}, streamRoute{allowed: memberFrameTypes})
	current.send(protocol.ConversationTyping{ConversationID: "a", SenderSubjectID: "s1", Active: true})
	current.send(protocol.ConversationTyping{ConversationID: "a", SenderSubjectID: "s2", Active: true})
	current.send(protocol.ConversationTyping{ConversationID: "a", SenderSubjectID: "s1"})
	current.send(protocol.ConversationTyping{ConversationID: "b", SenderSubjectID: "s1", Active: true})
	want := []protocol.Frame{
		protocol.ConversationTyping{ConversationID: "a", SenderSubjectID: "s1"},
		protocol.ConversationTyping{ConversationID: "a", SenderSubjectID: "s2", Active: true},
		protocol.ConversationTyping{ConversationID: "b", SenderSubjectID: "s1", Active: true},
	}
	if !reflect.DeepEqual(current.queue, want) {
		t.Fatalf("queue = %#v, want %#v", current.queue, want)
	}
}

// TestConnectionRevokeDiscardsQueue 验证撤销清除未发送的事件并进入关闭状态。
func TestConnectionRevokeDiscardsQueue(t *testing.T) {
	current := newConnection(New(nil, nil, "test", Options{QueueSize: 4}), func() {}, streamRoute{allowed: memberFrameTypes})
	current.send(protocol.ConversationChanged{ConversationID: "a", Version: 1})
	current.revoke(realtime.KindSessionLoggedOut)
	if !current.closing || len(current.queue) != 0 || current.epoch != 1 {
		t.Fatalf("closing = %v, queue = %#v, epoch = %d", current.closing, current.queue, current.epoch)
	}
}

// TestVisitorConnectionDropsInternalFrames 验证访客事件流只下发公开事件，成员专用事件直接丢弃。
func TestVisitorConnectionDropsInternalFrames(t *testing.T) {
	current := newConnection(New(nil, nil, "test", Options{QueueSize: 4}), func() {}, streamRoute{allowed: visitorFrameTypes})
	current.send(protocol.ConversationStateChanged{ConversationID: "a", Version: 1})
	current.send(protocol.ConversationRemoved{ConversationID: "a"})
	current.send(protocol.IdentityProfileChanged{Version: 1})
	current.send(protocol.ConversationTyping{ConversationID: "a", SenderSubjectID: "s1", Active: true})
	current.send(protocol.ServiceAttention{ConversationID: "a", ServiceSessionID: "s", Reason: domain.ServiceAttentionAssigned})
	current.send(protocol.ConversationChanged{ConversationID: "a", Version: 7})
	want := []protocol.Frame{protocol.ConversationChanged{ConversationID: "a", Version: 7}}
	if !reflect.DeepEqual(current.queue, want) {
		t.Fatalf("queue = %#v, want %#v", current.queue, want)
	}
}

// TestMemberConnectionDeliversPersonalFrames 验证成员事件流下发个人置顶顺序与客服处理周期提醒，不同周期的提醒分别入队。
func TestMemberConnectionDeliversPersonalFrames(t *testing.T) {
	current := newConnection(New(nil, nil, "test", Options{QueueSize: 4}), func() {}, streamRoute{allowed: memberFrameTypes})
	current.send(protocol.PinOrderChanged{Version: 3})
	current.send(protocol.ServiceAttention{ConversationID: "a", ServiceSessionID: "s1", Reason: domain.ServiceAttentionAssigned})
	current.send(protocol.ServiceAttention{ConversationID: "b", ServiceSessionID: "s2", Reason: domain.ServiceAttentionAssigned})
	want := []protocol.Frame{
		protocol.PinOrderChanged{Version: 3},
		protocol.ServiceAttention{ConversationID: "a", ServiceSessionID: "s1", Reason: domain.ServiceAttentionAssigned},
		protocol.ServiceAttention{ConversationID: "b", ServiceSessionID: "s2", Reason: domain.ServiceAttentionAssigned},
	}
	if !reflect.DeepEqual(current.queue, want) {
		t.Fatalf("queue = %#v, want %#v", current.queue, want)
	}
}
