//go:build server

package reportpage

import "github.com/runforyou-ai/luway/internal/actions/contactprofile"

// RequesterNameExpr 返回发起人展示名称表达式；实现见 contactprofile.RequesterNameExpr。
func RequesterNameExpr(contactAlias, channelIdentityName, workspaceIdentityAlias string) string {
	return contactprofile.RequesterNameExpr(contactAlias, channelIdentityName, workspaceIdentityAlias)
}

// RequesterAvatarExpr 返回发起人头像文件编号表达式；实现见 contactprofile.RequesterAvatarExpr。
func RequesterAvatarExpr(channelIdentityAlias, workspaceIdentityAlias string) string {
	return contactprofile.RequesterAvatarExpr(channelIdentityAlias, workspaceIdentityAlias)
}

// RequesterChannelJoins 拼出发起人与渠道 JOIN；实现见 contactprofile.RequesterChannelJoins。
func RequesterChannelJoins() (string, []any) {
	return contactprofile.RequesterChannelJoins()
}
