//go:build server

package user

import (
	"errors"

	roleaction "github.com/runforyou-ai/cervi/internal/actions/role"
)

var (
	// ErrNotFound 表示当前企业中不存在指定成员。
	ErrNotFound = errors.New("user not found")
	// ErrQueryInvalid 表示企业成员列表查询无效。
	ErrQueryInvalid = errors.New("user list query invalid")
	// ErrLastActiveAdministrator 表示企业至少需要保留一名账号正常的管理员。
	ErrLastActiveAdministrator = roleaction.ErrLastActiveAdministrator
)
