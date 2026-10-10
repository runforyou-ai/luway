package appservice

import "context"

// SeatBackend 定义工作区席位的业务调用。
type SeatBackend interface {
	// GetWorkspaceSeats 返回当前工作区的席位上限与启用的成员数。
	//appservice:route GET /seats perm=none
	GetWorkspaceSeats(context.Context, RequestMeta) (WorkspaceSeats, error)
}
