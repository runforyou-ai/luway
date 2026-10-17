//go:build server

package channelinbound

import (
	"errors"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// ChannelMessage 定义入站侧持有的平台消息身份与引用快照，落库时再转为投递包 API。
type ChannelMessage struct {
	AccountID      string
	ConversationID string
	MessageID      string
	Reply          *ChannelReply
}

// ChannelReply 保存同一聊天内被引用消息的编号和一层快照。
type ChannelReply struct {
	MessageID   string
	Body        string
	SenderName  string
	SenderIsBot bool
}

// deliveryInbound 转为 channeldelivery.Inbound，供 RecordInbound / MatchesInbound 使用。
func (m *ChannelMessage) deliveryInbound() *channeldelivery.Inbound {
	if m == nil {
		return nil
	}
	inbound := &channeldelivery.Inbound{
		AccountID: m.AccountID, ConversationID: m.ConversationID, MessageID: m.MessageID,
	}
	if m.Reply != nil {
		inbound.Reply = &channeldelivery.Reply{
			MessageID: m.Reply.MessageID, Body: m.Reply.Body,
			SenderName: m.Reply.SenderName, SenderIsBot: m.Reply.SenderIsBot,
		}
	}
	return inbound
}

// Input 定义渠道文本与附件入站事务的稳定事实。
type Input struct {
	ChannelMessage   *ChannelMessage
	ClientMessageID  *string
	Attachment       *UploadedFile
	ExternalMedia    *ExternalMedia
	ReplyToMessageID string
	ExternalID       string
	// Unsupported 表示发起人发送的消息内容类型暂不支持，写入一条正文为空的不支持消息。
	Unsupported bool
	// IdentityAsserted 表示本次入站请求自身给出核验结论（网站与业务系统转发），新消息按 VerifiedUserID 同步核验；为假时渠道身份的核验由渠道身份断言维护，入站保持不变。
	IdentityAsserted        bool
	VerifiedUserID          string
	Email                   string
	SignedProfile           *domain.SignedContactProfile
	VisitorContext          *domain.VisitorContext
	DisplayName             *string
	RequestedConversationID *string
	SingleConversation      bool
	Body                    string
	IdempotencyKey          string
	OriginatedAt            time.Time
}

// UploadedFile 定义网站访客附件消息待关联的已上传文件。
type UploadedFile struct {
	FileID      string
	ImageWidth  int
	ImageHeight int
}

// ExternalMedia 定义外部平台随消息送达、内容尚待取回的媒体附件。
type ExternalMedia struct {
	ExternalID  string
	FileName    string
	ContentType string
	ByteSize    int64
	ImageWidth  int
	ImageHeight int
}

// messageType 返回本次入站写入的消息类型。
func (i Input) messageType() domain.MessageType {
	if i.Unsupported {
		return domain.MessageTypeUnsupported
	}
	if i.Attachment != nil || i.ExternalMedia != nil {
		return domain.MessageTypeAttachment
	}
	return domain.MessageTypeText
}

// Result 返回渠道入站事务创建或取得的事实。
type Result struct {
	ReplyTo              *conversationaction.ConversationMessageReference
	Attachment           *Attachment
	Session              *servermodels.ServiceSession
	Message              *servermodels.Message
	ChannelIdentityID    string
	Inserted             bool
	CreatedConversation  bool
	OpenedServiceSession bool
}

// errNewSessionRouteRequired 表示本次写入需要开启新客服处理周期，须先锁定路由目标再重做。
var errNewSessionRouteRequired = errors.New("new service session route required")
