//go:build server

package servercli

import (
	"context"
	"errors"
	"fmt"

	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/common"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	acmeintegration "github.com/runforyou-ai/luway/internal/integration/acme"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
)

// publicURLCommand 修改部署地址，HTTPS 部署地址自动签发证书时先签发证书。
func publicURLCommand(arguments []string, migrations serverstorage.Migrations) error {
	flags := newFlags("public-url", "<部署地址>")
	if err := parseFlags(flags, arguments, 1); err != nil {
		return err
	}
	config, err := serverconfig.Load()
	if err != nil {
		return fmt.Errorf("load server config: %w", err)
	}
	ctx := context.Background()
	store, err := connect(ctx, config, migrations)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := migrations.Verify(ctx, store.DB().DB); err != nil {
		return err
	}
	db := store.DB()
	certificates := certificateaction.NewCertificates(db, acmeintegration.NewIssuer(acmeintegration.DirectoryURL, serverstorage.NewACMEChallenges(db)))
	fields, err := certificates.SetPublicURL(ctx, flags.Arg(0))
	if errors.Is(err, platformaction.ErrNotInstalled) {
		return errors.New("平台尚未完成首次安装")
	}
	if errors.Is(err, certificateaction.ErrCertificateBusy) {
		return errors.New("部署中正在签发证书，请稍后重试")
	}
	if issueErr, ok := errors.AsType[*certificateaction.CertificateIssueError](err); ok && errors.Is(issueErr, context.DeadlineExceeded) {
		return fmt.Errorf("为 %s 签发证书超时，请稍后重试", issueErr.Domain)
	}
	if issueErr, ok := errors.AsType[*certificateaction.CertificateIssueError](err); ok {
		return fmt.Errorf("为 %s 签发证书失败：%s", issueErr.Domain, issueErr.Reason)
	}
	if err != nil {
		return err
	}
	// 把字段校验错误码转换为命令行说明。
	messages := map[common.FieldCode]string{
		certificateaction.ValidationPublicURLInvalid:          "部署地址必须是不带路径的完整 HTTP 或 HTTPS 地址",
		certificateaction.ValidationPublicURLDomainRequired:   "自动签发证书需要使用域名作为部署地址",
		certificateaction.ValidationCertificateInvalid:        "上传的证书无法解析",
		certificateaction.ValidationCertificateExpired:        "上传的证书已过期",
		certificateaction.ValidationCertificateDomainMismatch: "上传的证书不包含该部署地址的域名",
	}
	for _, code := range fields {
		return errors.New(messages[code])
	}
	fmt.Println("部署地址已修改，部署中的服务器在下次心跳时生效")
	return nil
}
