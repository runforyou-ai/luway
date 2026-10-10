//go:build server

package servercli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/uptrace/bun"
)

// programInfo 是 version --json 输出的程序版本与程序的数据库迁移版本。
type programInfo struct {
	Version    string  `json:"version"`
	Migrations []int64 `json:"migrations"`
}

// versionCommand 输出程序版本与程序的最新数据库迁移版本，--json 时输出 programInfo。
func versionCommand(arguments []string, programMigrations serverstorage.Migrations) error {
	flags := newFlags("version", "")
	asJSON := flags.Bool("json", false, "以 JSON 输出版本与全部数据库迁移版本")
	if err := parseFlags(flags, arguments, 0); err != nil {
		return err
	}
	migrations, err := programMigrations.Versions()
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(programInfo{Version: buildinfo.Version, Migrations: migrations})
	}
	fmt.Println(buildinfo.Version)
	if len(migrations) > 0 {
		fmt.Printf("数据库迁移版本 %d\n", migrations[len(migrations)-1])
	}
	return nil
}

// migrateCommand 查看数据库迁移状态，或把数据库结构撤回到较低版本程序的迁移。
func migrateCommand(arguments []string, migrations serverstorage.Migrations) error {
	if len(arguments) == 0 {
		fmt.Fprintln(os.Stderr, "用法：migrate status | migrate down --to <文件>")
		return errUsage
	}
	switch arguments[0] {
	case "status":
		return migrateStatusCommand(arguments[1:], migrations)
	case "down":
		return migrateDownCommand(arguments[1:], migrations)
	}
	return fmt.Errorf("未知的 migrate 子命令 %q", arguments[0])
}

// migrateStatusCommand 输出数据库已执行与待执行的迁移数量及最新版本。
func migrateStatusCommand(arguments []string, migrations serverstorage.Migrations) error {
	flags := newFlags("migrate status", "")
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
	state, err := migrations.State(ctx, store.DB().DB)
	if err != nil {
		return err
	}
	fmt.Printf("已执行 %d 个迁移", len(state.Applied))
	if len(state.Applied) > 0 {
		fmt.Printf("，最新版本 %d", state.Applied[len(state.Applied)-1])
	}
	fmt.Printf("\n待执行 %d 个迁移\n", len(state.Pending))
	return nil
}

// migrateDownCommand 撤销数据库中已执行、但较低版本程序没有的迁移；较低版本程序的迁移版本取自其 version --json 的输出。
func migrateDownCommand(arguments []string, migrations serverstorage.Migrations) error {
	flags := newFlags("migrate down", "")
	to := flags.String("to", "", "较低版本程序 version --json 的输出文件，- 表示从标准输入读取")
	if err := parseFlags(flags, arguments, 0); err != nil {
		return err
	}
	if *to == "" {
		flags.Usage()
		return errUsage
	}
	var input io.Reader = os.Stdin
	if *to != "-" {
		file, err := os.Open(*to)
		if err != nil {
			return err
		}
		defer file.Close()
		input = file
	}
	var target programInfo
	if err := json.NewDecoder(input).Decode(&target); err != nil {
		return fmt.Errorf("读取较低版本程序的迁移版本: %w", err)
	}
	if target.Version == "" || len(target.Migrations) == 0 {
		return errors.New("较低版本程序的版本或迁移版本为空")
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
	// 数据库已与较低版本一致时无需撤销。
	state, err := migrations.State(ctx, store.DB().DB)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(state.Applied, func(version int64) bool { return !slices.Contains(target.Migrations, version) }) {
		fmt.Printf("数据库没有版本 %s 之外的迁移，无需撤销\n", target.Version)
		return nil
	}
	// 在部署准入锁内撤销迁移，部署中有运行的服务器时拒绝，撤销期间其他服务器不能加入部署。
	var rolledBack []int64
	err = serverinstanceaction.WithStoppedDeployment(ctx, store.DB(), func(ctx context.Context, _ bun.Conn) error {
		var rollbackErr error
		rolledBack, rollbackErr = migrations.Rollback(ctx, store.DB().DB, target.Migrations)
		return rollbackErr
	})
	if errors.Is(err, serverinstanceaction.ErrServersRunning) {
		return fmt.Errorf("先停止部署中的全部服务器: %w", err)
	}
	if err != nil {
		return err
	}
	fmt.Printf("已撤销 %d 个版本 %s 没有的迁移\n", len(rolledBack), target.Version)
	return nil
}
