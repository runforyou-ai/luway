//go:build server

package direct

import (
	"context"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/diagnostic"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// ExportDiagnosticBundle 生成当前服务端的运行摘要与日志压缩包。
func (o *platformOps) ExportDiagnosticBundle(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.DiagnosticBundle, error) {
	summary, err := o.GetPlatformDiagnostics(ctx, meta, account)
	if err != nil {
		return appservice.DiagnosticBundle{}, err
	}
	content, err := diagnostic.Build(o.dataDirectory, summary)
	if err != nil {
		return appservice.DiagnosticBundle{}, platformError(meta, err, i18n.ErrorPlatformDiagnosticsFailed)
	}
	slog.InfoContext(ctx, "诊断包已生成", "bytes", len(content))
	return appservice.DiagnosticBundle{Name: "diagnostics-" + summary.GeneratedAt.Format("20060102-150405") + ".zip", Content: content}, nil
}

// UploadDiagnosticBundle 生成诊断包并主动上传到 control。
func (o *platformOps) UploadDiagnosticBundle(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, _ appservice.DiagnosticUploadInput) error {
	bundle, err := o.ExportDiagnosticBundle(ctx, meta, account)
	if err != nil {
		return err
	}
	if err := o.control.UploadDiagnostic(ctx, bundle.Content); err != nil {
		return platformError(meta, err, i18n.ErrorPlatformDiagnosticsFailed)
	}
	slog.InfoContext(ctx, "诊断包已上传")
	return nil
}
