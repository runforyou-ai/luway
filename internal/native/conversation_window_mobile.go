//go:build !server && (android || ios)

package native

// NewConversationWindowOpener 禁用移动端会话独立窗口能力。
func NewConversationWindowOpener() ConversationWindows {
	return nil
}
