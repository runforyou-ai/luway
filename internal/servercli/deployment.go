//go:build server

package servercli

import (
	"context"
	"errors"
	"fmt"

	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
)

// preflightCommand 检查本程序能否以当前配置加入部署，确认回退时把部署版本改为本程序版本。
func preflightCommand(arguments []string, migrations serverstorage.Migrations) error {
	flags := newFlags("preflight", "")
	acceptDowngrade := flags.Bool("accept-downgrade", false, "本程序版本低于部署版本时，把部署版本改为本程序版本；部署中须没有运行的服务器")
	if err := parseFlags(flags, arguments, 0); err != nil {
		return err
	}
	config, err := serverconfig.Load()
	if err != nil {
		return fmt.Errorf("load server config: %w", err)
	}
	deployed, err := checkDeployment(context.Background(), config, migrations, *acceptDowngrade)
	if err != nil {
		return err
	}
	switch {
	case deployed == "":
		fmt.Printf("数据库尚未初始化，本程序（%s）可以加入部署\n", buildinfo.Version)
	case buildinfo.CompareVersions(buildinfo.Version, deployed) < 0:
		fmt.Printf("部署版本已由 %s 改为 %s\n", deployed, buildinfo.Version)
	default:
		fmt.Printf("本程序（%s）可以加入部署（部署版本 %s）\n", buildinfo.Version, deployed)
	}
	return nil
}

// checkDeployment 连接数据库，检查数据库已执行的迁移都是本程序已知的迁移，以及本程序版本不低于部署版本；返回部署此前登记的版本，尚未登记时为空。acceptDowngrade 为真且部署中没有运行的服务器时把较高的部署版本改为本程序版本，降低部署版本时部署中不能有运行的服务器，由进程管理器或容器自动重启的较高版本服务器会把部署版本改回。
func checkDeployment(ctx context.Context, config serverconfig.Config, migrations serverstorage.Migrations, acceptDowngrade bool) (string, error) {
	store, err := connect(ctx, config, migrations)
	if err != nil {
		return "", err
	}
	defer store.Close()
	if err := migrations.Verify(ctx, store.DB().DB); err != nil {
		return "", fmt.Errorf("数据库已执行本程序（%s）没有的迁移，不能以本程序加入部署: %w", buildinfo.Version, err)
	}
	deployed, err := serverinstanceaction.CheckAdmission(ctx, store.DB(), buildinfo.Version, acceptDowngrade)
	switch {
	case errors.Is(err, serverinstanceaction.ErrVersionOutdated):
		return deployed, fmt.Errorf("本程序版本 %s 低于部署版本 %s；确认回退时加 --accept-downgrade 参数", buildinfo.Version, deployed)
	case errors.Is(err, serverinstanceaction.ErrServersRunning):
		return deployed, fmt.Errorf("回退前先停止部署中的全部服务器: %w", err)
	}
	return deployed, err
}

// resetServerIDCommand 为平台生成新的服务器标识与签名私钥并删除本地授权。
func resetServerIDCommand(arguments []string, migrations serverstorage.Migrations) error {
	flags := newFlags("reset-server-id", "")
	if err := parseFlags(flags, arguments, 0); err != nil {
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
	if err := store.Migrate(ctx); err != nil {
		return err
	}
	serverID, err := licenseaction.ResetServerID(ctx, store.DB())
	if err != nil {
		return fmt.Errorf("reset server id: %w", err)
	}
	fmt.Println("服务器标识已重置，新服务器标识：" + serverID)
	return nil
}

// connect 按启动配置连接数据库。
func connect(ctx context.Context, config serverconfig.Config, migrations serverstorage.Migrations) (*serverstorage.Store, error) {
	store, err := serverstorage.Connect(ctx, config.Database, migrations, "", 0)
	if err != nil {
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	return store, nil
}
