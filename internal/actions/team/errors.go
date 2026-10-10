//go:build server

package team

import "errors"

var (
	// ErrNotFound 表示当前企业中不存在指定团队。
	ErrNotFound = errors.New("team not found")
	// ErrMemberNotFound 表示当前团队中不存在指定成员关系。
	ErrMemberNotFound = errors.New("team member not found")
	// ErrMemberInvalid 表示团队成员身份参数无效。
	ErrMemberInvalid = errors.New("team member invalid")
)
