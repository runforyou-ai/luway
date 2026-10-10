//go:build server

package inbox

import "errors"

// ErrQueryInvalid 表示收件箱范围或筛选条件无效。
var ErrQueryInvalid = errors.New("inbox query invalid")

// ErrConversationUnavailable 表示检索的会话不存在或当前身份不可阅读。
var ErrConversationUnavailable = errors.New("inbox conversation unavailable")

// ErrCursorInvalid 表示分页游标失效或不属于当前查询。
var ErrCursorInvalid = errors.New("inbox cursor invalid")
