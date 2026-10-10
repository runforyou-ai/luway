//go:build server

package server

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing/fstest"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/runforyou-ai/luway/pkg/goosecheck"
	"github.com/runforyou-ai/support/arr"
)

// coreMigrationFiles 是核心的服务端 PostgreSQL 迁移文件。
//
//go:embed migrations/*.sql
var coreMigrationFiles embed.FS

// errMigrationsUnset 表示迁移未经 NewMigrations 或 CoreMigrations 构造。
var errMigrationsUnset = errors.New("migrations are not set")

// Migrations 是服务端程序的数据库迁移：一个或多个迁移来源合成的单一迁移历史，共用一张版本表与一把迁移锁。
type Migrations struct {
	files fs.FS
}

// CoreMigrationFiles 返回核心迁移来源，迁移文件位于来源根目录。
func CoreMigrationFiles() fs.FS {
	files, err := fs.Sub(coreMigrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return files
}

// coreMigrations 是只含核心迁移来源的迁移，首次使用时合成。
var coreMigrations = sync.OnceValue(func() Migrations {
	migrations, err := NewMigrations(MigrationSource{Files: CoreMigrationFiles()})
	if err != nil {
		panic(err)
	}
	return migrations
})

// CoreMigrations 返回只含核心迁移来源的迁移。
func CoreMigrations() Migrations {
	return coreMigrations()
}

// SharedModelAllowedFunction 是判断共享模型对工作区是否可用的 SQL 扩展点 shared_model_allowed(workspace_id uuid, model_id uuid) boolean：核心迁移定义默认实现（不开放共享模型），模型解析与调用经它判断，其他迁移来源可声明覆盖。
const SharedModelAllowedFunction = "shared_model_allowed"

// extensionFunctions 是核心迁移定义默认实现、其他迁移来源可声明覆盖的 SQL 函数扩展点，名称均为小写。
var extensionFunctions = []string{SharedModelAllowedFunction}

// MigrationSource 是一个迁移来源：根目录下的 .sql 迁移文件，以及该来源声明覆盖的 SQL 函数扩展点。
type MigrationSource struct {
	Files     fs.FS
	Overrides []string
}

// NewMigrations 合并多个迁移来源，各来源根目录下的 .sql 文件按文件名中的版本号组成一个迁移历史；第一个来源是核心迁移。
// 版本号在全部来源中重复时返回错误。迁移文件 Up 段（Goose 的 Down 指令行之前）任何位置出现 SQL 函数扩展点的名称（含注释、限定模式、引号与动态 SQL）即视为定义或改动该扩展点：
// 核心迁移只能在一个文件中定义默认实现；其他来源须在 Overrides 中声明才能改动，最多一个来源，版本晚于默认实现，且声明的来源须至少改动一次；不满足时返回错误。
func NewMigrations(sources ...MigrationSource) (Migrations, error) {
	merged := fstest.MapFS{}
	owners := map[int64]string{}
	// definitions 按扩展点记录定义它的来源序号与版本。
	type definition struct {
		source  int
		version int64
		name    string
	}
	definitions := map[string][]definition{}
	for index, source := range sources {
		names, err := fs.Glob(source.Files, "*.sql")
		if err != nil {
			return Migrations{}, fmt.Errorf("list migrations: %w", err)
		}
		for _, name := range names {
			version, err := goose.NumericComponent(name)
			if err != nil {
				return Migrations{}, fmt.Errorf("parse migration version %s: %w", name, err)
			}
			if owner, ok := owners[version]; ok {
				return Migrations{}, fmt.Errorf("migration version %d is used by both %s and source %d %s", version, owner, index, name)
			}
			content, err := fs.ReadFile(source.Files, name)
			if err != nil {
				return Migrations{}, fmt.Errorf("read migration %s: %w", name, err)
			}
			// 按 Goose 的指令规则取 Up 段：以 -- 开头且含区分大小写的 +goose 的行是指令，命令不区分大小写地等于 Down 时截断；Up 段任何位置出现扩展点名称都记为定义或改动。
			up, offset := string(content), 0
			for line := range strings.Lines(up) {
				if strings.HasPrefix(strings.TrimSpace(line), "--") && strings.Contains(line, "+goose") &&
					strings.EqualFold(strings.TrimSpace(strings.Replace(strings.ReplaceAll(line, "--", ""), "+goose", "", 1)), "Down") {
					up = up[:offset]
					break
				}
				offset += len(line)
			}
			up = strings.ToLower(up)
			for _, function := range extensionFunctions {
				if strings.Contains(up, function) {
					definitions[function] = append(definitions[function], definition{source: index, version: version, name: name})
				}
			}
			owners[version] = fmt.Sprintf("source %d %s", index, name)
			merged[name] = &fstest.MapFile{Data: content, Mode: 0o444}
		}
	}
	for _, function := range extensionFunctions {
		var defaults []definition
		overrides := map[int]bool{}
		for _, item := range definitions[function] {
			if item.source == 0 {
				defaults = append(defaults, item)
				continue
			}
			if !slices.Contains(sources[item.source].Overrides, function) {
				return Migrations{}, fmt.Errorf("migration %s redefines SQL extension point %s without declaring the override", item.name, function)
			}
			overrides[item.source] = true
		}
		if len(defaults) != 1 {
			return Migrations{}, fmt.Errorf("core migrations must define SQL extension point %s in exactly one file, found %d", function, len(defaults))
		}
		if len(overrides) > 1 {
			return Migrations{}, fmt.Errorf("SQL extension point %s is overridden by %d migration sources", function, len(overrides))
		}
		for index, source := range sources {
			if index > 0 && slices.Contains(source.Overrides, function) && !overrides[index] {
				return Migrations{}, fmt.Errorf("migration source %d declares an override of SQL extension point %s but none of its migrations defines it", index, function)
			}
		}
		for _, item := range definitions[function] {
			if item.source != 0 && item.version < defaults[0].version {
				return Migrations{}, fmt.Errorf("migration %s overrides SQL extension point %s before its default %s", item.name, function, defaults[0].name)
			}
		}
	}
	return Migrations{files: merged}, nil
}

// Versions 按从小到大返回全部迁移版本。
func (m Migrations) Versions() ([]int64, error) {
	if m.files == nil {
		return nil, errMigrationsUnset
	}
	names, err := fs.Glob(m.files, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	versions := make([]int64, 0, len(names))
	for _, name := range names {
		version, err := goose.NumericComponent(name)
		if err != nil {
			return nil, fmt.Errorf("parse migration version %s: %w", name, err)
		}
		versions = append(versions, version)
	}
	slices.Sort(versions)
	return versions, nil
}

// up 检查待执行迁移，并在 Goose 迁移锁内执行 PostgreSQL 数据库迁移。
func (m Migrations) up(ctx context.Context, db *sql.DB) error {
	// 抢锁失败时每秒重试一次，最长等待 5 分钟。
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300))
	if err != nil {
		return fmt.Errorf("create migration lock: %w", err)
	}
	provider, err := m.provider(db, goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}

	// 已执行版本缺少迁移源文件时拒绝启动，数据库需要重建。
	if err := goosecheck.VerifyApplied(ctx, db, goose.DialectPostgres, provider); err != nil {
		return err
	}

	// 无锁检查到迁移已全部应用时直接返回；版本表尚未建立时检查报错，由加锁的 Up 建表并迁移。
	if pending, err := provider.HasPending(ctx); err == nil && !pending {
		return nil
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return nil
	}
	slog.InfoContext(ctx, "PostgreSQL 数据库迁移完成", "applied", len(results))
	return nil
}

// Verify 校验数据库中已执行的迁移版本都有迁移源文件。
func (m Migrations) Verify(ctx context.Context, db *sql.DB) error {
	provider, err := m.provider(db)
	if err != nil {
		return err
	}
	return goosecheck.VerifyApplied(ctx, db, goose.DialectPostgres, provider)
}

// MigrationVersion 返回数据库版本表中已执行的最新迁移版本，与程序含有哪些迁移来源无关。
func MigrationVersion(ctx context.Context, db *sql.DB) (int64, error) {
	provider, err := CoreMigrations().provider(db)
	if err != nil {
		return 0, err
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("read migration version: %w", err)
	}
	return version, nil
}

// MigrationState 是数据库迁移相对程序迁移的执行情况。
type MigrationState struct {
	// Applied 是已执行且本程序已知的迁移版本，从小到大排列。
	Applied []int64
	// Pending 是本程序有而数据库尚未执行的迁移版本，从小到大排列。
	Pending []int64
}

// State 读取数据库已执行与待执行的迁移；数据库有程序没有的迁移时报错。
func (m Migrations) State(ctx context.Context, db *sql.DB) (MigrationState, error) {
	provider, err := m.provider(db)
	if err != nil {
		return MigrationState{}, err
	}
	if err := goosecheck.VerifyApplied(ctx, db, goose.DialectPostgres, provider); err != nil {
		return MigrationState{}, err
	}
	statuses, err := provider.Status(ctx)
	if err != nil {
		return MigrationState{}, fmt.Errorf("read migration status: %w", err)
	}
	applied, pending := arr.Partition(statuses, func(status *goose.MigrationStatus) bool { return status.State == goose.StateApplied })
	version := func(status *goose.MigrationStatus) int64 { return status.Source.Version }
	return MigrationState{Applied: arr.Map(applied, version), Pending: arr.Map(pending, version)}, nil
}

// Rollback 在 Goose 迁移锁内按版本从大到小撤销已执行但不在 keep 中的迁移，返回撤销的版本；数据库有程序没有的迁移时报错且不撤销任何迁移。
func (m Migrations) Rollback(ctx context.Context, db *sql.DB, keep []int64) ([]int64, error) {
	state, err := m.State(ctx, db)
	if err != nil {
		return nil, err
	}
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300))
	if err != nil {
		return nil, fmt.Errorf("create migration lock: %w", err)
	}
	provider, err := m.provider(db, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, err
	}
	var rolledBack []int64
	for _, version := range slices.Backward(state.Applied) {
		if slices.Contains(keep, version) {
			continue
		}
		if _, err := provider.ApplyVersion(ctx, version, false); err != nil {
			return rolledBack, fmt.Errorf("roll back migration %d: %w", version, err)
		}
		rolledBack = append(rolledBack, version)
	}
	return rolledBack, nil
}

// provider 创建使用这些迁移、允许乱序执行的 PostgreSQL 迁移器。
func (m Migrations) provider(db *sql.DB, options ...goose.ProviderOption) (*goose.Provider, error) {
	if m.files == nil {
		return nil, errMigrationsUnset
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, m.files, append(options, goose.WithAllowOutofOrder(true))...)
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return provider, nil
}
