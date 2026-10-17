//go:build server

package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	"github.com/runforyou-ai/luway/internal/common/diagnostic"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/integration/supportplane"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
)

// diagnosticCommand 导出本机日志与启动配置摘要，并按参数主动上传。
func diagnosticCommand(arguments []string, migrations serverstorage.Migrations) error {
	flags := newFlags("diagnostics", "")
	output := flags.String("output", "diagnostics.zip", "诊断包保存位置")
	upload := flags.Bool("upload", false, "向 control 上传诊断包")
	if err := parseFlags(flags, arguments, 0); err != nil {
		return err
	}
	config, err := serverconfig.Load()
	if err != nil {
		return err
	}
	// 导出只读取本机配置与日志，可在应用停止时执行。
	summary := map[string]any{"version": buildinfo.Version, "generated_at": time.Now().UTC(), "configuration": config.Diagnostics("")} //clock:local
	bundle, err := diagnostic.Build(config.Data.Directory, summary)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*output, bundle, 0600); err != nil {
		return err
	}
	fmt.Printf("诊断包已保存：%s\n", *output)
	if *upload {
		ctx := context.Background()
		store, err := connect(ctx, config, migrations)
		if err != nil {
			return err
		}
		defer store.Close()
		client := supportplane.New(supportplane.BaseURL, buildinfo.Version, func(ctx context.Context) (supportplane.Identity, error) {
			return licenseaction.ControlIdentity(ctx, store.DB())
		})
		if err := client.UploadDiagnostic(ctx, bundle); err != nil {
			return err
		}
		fmt.Println("诊断包已上传")
	}
	return nil
}
