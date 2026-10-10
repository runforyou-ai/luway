//go:build server

package conversation

import (
	"errors"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/common"
)

var (
	// ErrAgentUnavailable 表示 AI 员工不存在、已停用或没有有效的托管配置版本。
	ErrAgentUnavailable = errors.New("agent unavailable")
	// ErrAgentTargetNotFound 表示 AI 聊天目标不存在或不可用。
	ErrAgentTargetNotFound = errors.New("AI conversation target not found")
	// ErrMessageUnavailable 表示目标消息不存在或已经删除。
	ErrMessageUnavailable = errors.New("conversation message unavailable")
	// ErrMentionTargetInvalid 表示当前用户的提及目标校验失败。
	ErrMentionTargetInvalid = errors.New("conversation mention target invalid")
	// ErrConversationNotFound 表示会话不存在或当前身份无权访问。
	ErrConversationNotFound = chatstate.ErrConversationNotFound
	// ErrDirectTargetNotFound 表示内部单聊目标不存在或不可用。
	ErrDirectTargetNotFound = errors.New("direct conversation target not found")
	// ErrGroupMemberNotFound 表示待加入群聊的成员不存在或不可用。
	ErrGroupMemberNotFound = errors.New("group conversation member not found")
	// ErrGroupOwnerRequired 表示群主身份校验失败。
	ErrGroupOwnerRequired = chatstate.ErrGroupOwnerRequired
	// ErrDataInvariant 表示聊天持久关系不完整或互相矛盾。
	ErrDataInvariant = servicestate.ErrDataInvariant
)

// ConflictError 表示语言无关的消息写入冲突。
type ConflictError struct {
	Reason string
}

// Error 返回稳定冲突原因。
func (e *ConflictError) Error() string { return "conversation conflict: " + e.Reason }

// ValidationError 表示会话业务输入不合法。
type ValidationError = common.FieldError
