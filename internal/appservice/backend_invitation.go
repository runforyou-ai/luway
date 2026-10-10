package appservice

import "context"

// InvitationBackend 定义成员邀请的业务调用。
type InvitationBackend interface {
	// ListInvitations 返回当前工作区待接受的成员邀请。
	//appservice:route GET /invitations perm=workspace.manage
	ListInvitations(context.Context, RequestMeta) (InvitationList, error)
	// CreateInvitation 邀请账号加入当前工作区，返回只展示一次的邀请链接。
	//appservice:route POST /invitations status=201 perm=workspace.manage
	CreateInvitation(context.Context, RequestMeta, InvitationInput) (InvitationCreated, error)
	// RegenerateInvitation 撤销原邀请并以相同内容重新生成邀请链接。
	//appservice:route POST /invitations/{invitationID:uuid}/regenerate perm=workspace.manage
	RegenerateInvitation(context.Context, RequestMeta, string) (InvitationCreated, error)
	// RevokeInvitation 撤销待接受的邀请。
	//appservice:route DELETE /invitations/{invitationID:uuid} perm=workspace.manage
	RevokeInvitation(context.Context, RequestMeta, string) error
	// PreviewInvitation 按邀请令牌返回工作区名称、邀请人和掩码后的受邀邮箱。
	//appservice:route POST /invitation-previews auth=public
	PreviewInvitation(context.Context, RequestMeta, InvitationTokenInput) (InvitationPreview, error)
	// AcceptInvitation 由当前账号接受邀请并加入工作区。
	//appservice:route POST /invitation-acceptances auth=account
	AcceptInvitation(context.Context, RequestMeta, InvitationTokenInput) (Workspace, error)
}
