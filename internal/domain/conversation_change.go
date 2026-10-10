package domain

import (
	"encoding/json"
	"fmt"
)

// ConversationChanges 是会话变更通知携带的变化类别集合；过程更新以外的变化都伴随会话摘要与列表行变化，零值表示只有这两处变化。
type ConversationChanges uint8

const (
	// ConversationChangeTimeline 表示时间线变化：消息、投递状态、附件取回、AI 运行与排队、回复资格。
	ConversationChangeTimeline ConversationChanges = 1 << iota
	// ConversationChangeService 表示服务周期变化：开启、流转、关闭、评价、交接摘要、周期小结与终态运行的业务查询。
	ConversationChangeService
	// ConversationChangeParticipants 表示参与方变化：成员、AI 员工、团队、渠道、联系人与访客资料，群资料与群成员。
	ConversationChangeParticipants
	// ConversationChangeFiles 表示会话共享文件区变化：文件新增、修改与删除。
	ConversationChangeFiles
	// ConversationChangeProcess 表示电脑上执行中的工具调用有了新的过程更新，会话摘要与列表行不变。
	ConversationChangeProcess
)

// conversationChangeNames 按传输顺序列出各变化类别的名称。
var conversationChangeNames = []struct {
	change ConversationChanges
	name   string
}{
	{ConversationChangeTimeline, "timeline"},
	{ConversationChangeService, "service"},
	{ConversationChangeParticipants, "participants"},
	{ConversationChangeFiles, "files"},
	{ConversationChangeProcess, "process"},
}

// MarshalJSON 把变化类别集合编码为名称数组，零值编码为空数组。
func (c ConversationChanges) MarshalJSON() ([]byte, error) {
	names := make([]string, 0, len(conversationChangeNames))
	for _, entry := range conversationChangeNames {
		if c&entry.change != 0 {
			names = append(names, entry.name)
		}
	}
	return json.Marshal(names)
}

// UnmarshalJSON 从名称数组解码变化类别集合，未知名称返回错误。
func (c *ConversationChanges) UnmarshalJSON(data []byte) error {
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return err
	}
	var changes ConversationChanges
	for _, name := range names {
		matched := false
		for _, entry := range conversationChangeNames {
			if entry.name == name {
				changes |= entry.change
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("unknown conversation change %q", name)
		}
	}
	*c = changes
	return nil
}

// ConversationChanges 返回时间线中该系统事件额外代表的变化类别：群事件改变参与方，客户会话事件改变服务周期。
func (t ConversationSystemEventType) ConversationChanges() ConversationChanges {
	switch t {
	case ConversationSystemEventGroupRenamed, ConversationSystemEventGroupMembersAdded, ConversationSystemEventGroupMemberRemoved,
		ConversationSystemEventGroupMemberLeft, ConversationSystemEventGroupOwnerTransferred, ConversationSystemEventGroupDissolved:
		return ConversationChangeParticipants
	case ConversationSystemEventServiceSessionHandedOff, ConversationSystemEventServiceSessionClaimed, ConversationSystemEventServiceSessionTakenOver,
		ConversationSystemEventServiceSessionTransferred, ConversationSystemEventServiceSessionClosed, ConversationSystemEventServiceSessionReopened,
		ConversationSystemEventServiceSessionReturned, ConversationSystemEventServiceSessionAssigned, ConversationSystemEventServiceSessionRated,
		ConversationSystemEventServiceSessionEmailCollected, ConversationSystemEventServiceSessionEmailNotified, ConversationSystemEventServiceStatusChanged:
		return ConversationChangeService
	}
	return 0
}
