//go:build server

package servercli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
)

// statusCommand 输出部署版本与部署中的服务器。
func statusCommand(arguments []string, migrations serverstorage.Migrations) error {
	if err := parseFlags(newFlags("status", ""), arguments, 0); err != nil {
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
	deployed, err := serverinstanceaction.CheckAdmission(ctx, store.DB(), buildinfo.Version, false)
	if err != nil && !errors.Is(err, serverinstanceaction.ErrVersionOutdated) {
		return err
	}
	if deployed == "" {
		fmt.Println("部署版本：数据库尚未初始化")
		return nil
	}
	fmt.Printf("部署版本：%s\n", deployed)
	if err != nil {
		fmt.Printf("本程序版本 %s 低于部署版本，不能加入部署\n", buildinfo.Version)
	}
	instances, err := serverinstanceaction.ListInstanceSummaries(ctx, store.DB())
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		fmt.Println("服务器：无")
		return nil
	}
	now, err := serverstorage.Now(ctx, store.DB())
	if err != nil {
		return err
	}
	fmt.Println("服务器：")
	table := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, instance := range instances {
		state := "失联"
		if instance.Online {
			state = "在线"
		}
		fmt.Fprintf(table, "  %s\t%s\t%s\t心跳 %d 秒前\n", instance.Hostname, instance.Version, state, int(now.Sub(instance.HeartbeatAt).Seconds()))
	}
	return table.Flush()
}
