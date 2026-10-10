//go:build server

package integrationtest

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChannelIdentityAssertion 验证业务系统签名断言核验与撤销直连 Telegram 渠道身份，直连入站不改变断言结果与联系人名称，绑定目标不一致、渠道不适用或以业务系统转发接入时拒绝。
func TestChannelIdentityAssertion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newTelegramGatewayFixture(t)
	f.connect(t, domain.TelegramConnectionDirect)
	secret, err := customerserviceaction.NewRegenerateCustomerIdentitySecretAction(f.db).Execute(ctx, f.identity)
	require.NoError(t, err)
	assertion := customerchataction.NewAssertChannelIdentityAction(f.db, testEnqueuer)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	sign := func(userID, channelID, externalID string) string {
		return signCustomer(t, secret, jwt.MapClaims{
			"sub": userID, "name": "张三", "email": "zhang@example.com", "exp": time.Now().Add(time.Hour).Unix(),
			"channel_id": channelID, "external_id": externalID,
		})
	}

	// 直连入站的发送者为未核验联系人，断言后核验为企业用户并写入名称与邮箱。
	const sender int64 = 2001
	external := strconv.FormatInt(sender, 10)
	require.NoError(t, f.forward(t, sender, ""))
	user := "assert-a-" + suffix
	require.NoError(t, assertion.Verify(ctx, f.channelID, external, sign(user, f.channelID, external)))
	verified := f.senderIdentity(t, sender)
	profile := f.senderProfile(t, sender)
	contact := f.contact(t, *verified.ContactID)
	require.NotNil(t, contact.DisplayName, "断言后联系人名称")
	require.Equal(t, "张三", *contact.DisplayName, "断言后联系人名称")
	require.NotNil(t, verified.VerifiedUserID, "断言后 identity=%+v", verified)
	require.Equal(t, user, *verified.VerifiedUserID)
	require.True(t, profile.IdentityVerified, "断言后 profile=%+v", profile)
	require.Equal(t, user, profile.ExternalUserID)
	require.Equal(t, "zhang@example.com", profile.Email)

	// 直连入站不给出核验结论，已断言的核验与联系人名称保持不变，平台姓名只更新渠道身份。
	require.NoError(t, f.forward(t, sender, ""))
	kept := f.senderIdentity(t, sender)
	require.NotNil(t, kept.VerifiedUserID, "入站后 identity=%+v", kept)
	require.Equal(t, user, *kept.VerifiedUserID)
	require.NotNil(t, kept.DisplayName)
	require.Equal(t, "TG 2001", *kept.DisplayName)
	contact = f.contact(t, *kept.ContactID)
	require.NotNil(t, contact.DisplayName, "入站后联系人名称")
	require.Equal(t, "张三", *contact.DisplayName, "入站后联系人名称")

	// 渠道编号大小写不同的签名与路径按同一渠道处理。
	upper := strings.ToUpper(f.channelID)
	require.NoError(t, assertion.Verify(ctx, upper, external, sign(user, upper, external)), "大写渠道编号")

	// 签名未绑定本次渠道与外部编号时拒绝。
	rejected := map[string]string{
		"other external": sign(user, f.channelID, "9999"),
		"other channel":  sign(user, f.identity.Workspace.ID, external),
		"unbound":        signCustomer(t, secret, jwt.MapClaims{"sub": user, "exp": time.Now().Add(time.Hour).Unix()}),
		"wrong secret":   signCustomer(t, "other-secret", jwt.MapClaims{"sub": user, "exp": time.Now().Add(time.Hour).Unix(), "channel_id": f.channelID, "external_id": external}),
	}
	for name, token := range rejected {
		assert.ErrorIs(t, assertion.Verify(ctx, f.channelID, external, token), customerchataction.ErrCustomerIdentityInvalid, name)
	}

	// 尚未发来消息的外部编号直接建立已核验的渠道身份。
	const newcomer int64 = 2002
	newcomerExternal := strconv.FormatInt(newcomer, 10)
	require.NoError(t, assertion.Verify(ctx, f.channelID, newcomerExternal, sign(user, f.channelID, newcomerExternal)))
	created := f.senderIdentity(t, newcomer)
	require.NotNil(t, created.VerifiedUserID, "新渠道身份 = %+v", created)
	require.Equal(t, user, *created.VerifiedUserID)
	require.Equal(t, *verified.ContactID, *created.ContactID)

	// 撤销只对当前核验的企业用户生效。
	require.NoError(t, assertion.Revoke(ctx, f.channelID, external, sign("assert-b-"+suffix, f.channelID, external)))
	kept = f.senderIdentity(t, sender)
	require.NotNil(t, kept.VerifiedUserID, "其他企业用户撤销后 identity=%+v", kept)
	require.Equal(t, user, *kept.VerifiedUserID)
	require.NoError(t, assertion.Revoke(ctx, f.channelID, external, sign(user, f.channelID, external)))
	revoked := f.senderIdentity(t, sender)
	require.Nil(t, revoked.VerifiedUserID, "撤销后 identity=%+v", revoked)
	require.False(t, f.senderProfile(t, sender).IdentityVerified)

	// 不经外部平台投递的渠道和不合法的外部编号不接受断言。
	website, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "官网", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	require.ErrorIs(t, assertion.Verify(ctx, website.ID, external, sign(user, website.ID, external)), channelaction.ErrNotFound, "网站渠道断言")
	// 业务系统转发接入时入站消息自带核验结论，不接受断言。
	f.connect(t, domain.TelegramConnectionGateway)
	require.ErrorIs(t, assertion.Verify(ctx, f.channelID, external, sign(user, f.channelID, external)), channelaction.ErrNotFound, "转发接入断言")
	var validation *conversationaction.ValidationError
	require.ErrorAs(t, assertion.Verify(ctx, f.channelID, " ", sign(user, f.channelID, " ")), &validation, "空白外部编号")
}
