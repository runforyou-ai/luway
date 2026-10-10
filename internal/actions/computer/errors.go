//go:build server

package computer

import "errors"

var (
	// ErrNotFound 表示当前成员不能管理指定电脑：电脑不存在、已撤销，或是其他成员的个人电脑。
	ErrNotFound = errors.New("computer not found")
	// ErrCredentialInvalid 表示电脑凭据无效、电脑已撤销、工作区已暂停，或个人电脑的主人已不是工作区的有效成员。
	ErrCredentialInvalid = errors.New("computer credential invalid")
)
