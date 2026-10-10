//go:build server

package invitation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/luway/pkg/mail"
	"github.com/runforyou-ai/support/random"
	"github.com/uptrace/bun"
)

const (
	// SendEmailActionName 向受邀邮箱发送一封邀请邮件。
	SendEmailActionName = "invitation.send_email"
	// sendEmailMaxAttempts 是一封邀请邮件的最大发送次数，按任务的指数退避，首次失败后的重试累计等待约 64 分钟。
	sendEmailMaxAttempts = 9
)

// SendEmailInput 定义一封邀请邮件；Locale 是发起人发起邀请时的界面语言。
type SendEmailInput struct {
	WorkspaceID  string        `json:"workspaceId"`
	InvitationID string        `json:"invitationId"`
	Locale       domain.Locale `json:"locale"`
}

// SendEmailAction 为待接受的邀请签发邮件链接令牌并发送邀请邮件。
type SendEmailAction struct {
	db        *bun.DB
	mailer    Mailer
	publicURL func() string
}

// NewSendEmailAction 创建邀请邮件发送操作；publicURL 返回生成邀请链接的部署地址。
func NewSendEmailAction(db *bun.DB, mailer Mailer, publicURL func() string) *SendEmailAction {
	return &SendEmailAction{db: db, mailer: mailer, publicURL: publicURL}
}

// Execute 在事务内锁定仍待接受且未过期的邀请，签发新的邮件链接令牌并写入摘要，提交后发送邀请邮件；邀请已接受、撤销或过期，或部署未配置邮件发送时不发送。
// 每次执行替换邮件链接令牌，只有最后一次签发的邮件链接有效；复制链接的令牌不受影响。发信失败时按任务重试。
func (a *SendEmailAction) Execute(ctx context.Context, input SendEmailInput) error {
	if !a.mailer.Enabled() {
		return nil
	}
	token, tokenHash := random.Token(32)
	var message mail.Message
	var pending bool
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		var row struct {
			Workspace string `bun:"workspace"`
			Inviter   string `bun:"inviter"`
			Email     string `bun:"invited_email"`
		}
		err := tx.NewSelect().TableExpr("workspace_invitations AS inv").
			ColumnExpr("o.name AS workspace, COALESCE(ioi.display_name, '') AS inviter, inv.invited_email").
			Join("JOIN workspaces AS o ON o.id = inv.workspace_id").
			Join("LEFT JOIN users AS iu ON iu.id = inv.invited_by_user_id AND iu.workspace_id = inv.workspace_id").
			Join("LEFT JOIN workspace_identities AS ioi ON ioi.id = iu.identity_id AND ioi.workspace_id = iu.workspace_id").
			Where("inv.workspace_id = ?", input.WorkspaceID).
			Where("inv.id = ?", input.InvitationID).
			Where("inv.status = ?", domain.InvitationStatusPending).
			Where("inv.expires_at > now()").
			For("UPDATE OF inv").
			Scan(ctx, &row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Table("workspace_invitations").
			Set("email_token_hash = ?", tokenHash).
			Where("id = ?", input.InvitationID).
			Exec(ctx); err != nil {
			return err
		}
		message = renderInvitationEmail(input.Locale, row.Workspace, row.Inviter, row.Email, Link(a.publicURL(), token))
		pending = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("issue invitation email token: %w", err)
	}
	if !pending {
		return nil
	}
	return a.mailer.Send(ctx, message)
}

// renderInvitationEmail 按发起人的界面语言生成邀请邮件。
func renderInvitationEmail(locale domain.Locale, workspaceName, inviterName, to, link string) mail.Message {
	data := map[string]any{"Workspace": workspaceName, "Inviter": inviterName}
	language := string(locale)
	return mail.Notice{
		FromName: workspaceName, To: to, Lang: language, Link: link,
		Heading: i18n.LocalizeTemplate(language, i18n.InvitationEmailSubject, data),
		Body:    i18n.LocalizeTemplate(language, i18n.InvitationEmailBody, data),
		Action:  i18n.LocalizeTemplate(language, i18n.InvitationEmailAction, data),
		Note:    i18n.LocalizeTemplate(language, i18n.InvitationEmailExpiry, data),
	}.Message()
}
