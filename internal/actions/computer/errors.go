//go:build server

package computer

import "errors"

var (
	// ErrNotFound 表示当前成员名下不存在指定的未撤销电脑。
	ErrNotFound = errors.New("computer not found")
	// ErrCredentialInvalid 表示电脑凭据无效、电脑已撤销，或电脑主人已不是工作区的有效成员。
	ErrCredentialInvalid = errors.New("computer credential invalid")
)
