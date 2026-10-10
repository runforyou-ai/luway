package appservice

import "context"

// ComputerManagementBackend 定义电脑注册、个人电脑与工作区电脑管理的业务调用。
type ComputerManagementBackend interface {
	// RegisterComputer 把执行器所在电脑注册为当前成员的个人电脑，返回电脑凭据；同一安装重复注册时更换凭据。
	//appservice:route POST /computers perm=none
	RegisterComputer(context.Context, RequestMeta, ComputerRegistrationInput) (ComputerRegistration, error)
	// ListComputers 返回当前成员未撤销的个人电脑。
	//appservice:route GET /computers perm=none
	ListComputers(context.Context, RequestMeta) (ComputerList, error)
	// RevokeComputer 撤销当前成员的个人电脑，或在成员角色授予工作区管理权限时撤销工作区电脑，派发给它且未结束的操作立即结算，工作区电脑同时解除 AI 员工的绑定。
	//appservice:route DELETE /computers/{computerID:uuid} perm=none
	RevokeComputer(context.Context, RequestMeta, string) error
	// ListWorkspaceComputers 返回当前工作区未撤销的工作区电脑。
	//appservice:route GET /workspace-computers perm=none
	ListWorkspaceComputers(context.Context, RequestMeta) (ComputerList, error)
	// CreateWorkspaceComputer 添加工作区电脑，返回执行器连接使用的电脑凭据。
	//appservice:route POST /workspace-computers status=201 perm=workspace.manage
	CreateWorkspaceComputer(context.Context, RequestMeta, WorkspaceComputerInput) (ComputerRegistration, error)
	// ResetComputerCredential 为工作区电脑签发新凭据，旧凭据立即失效。
	//appservice:route POST /workspace-computers/{computerID:uuid}/credential perm=workspace.manage
	ResetComputerCredential(context.Context, RequestMeta, string) (ComputerRegistration, error)
}
