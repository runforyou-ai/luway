//go:build server

package file

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// temporaryFileLifetime 是未激活临时文件的有效期。
const temporaryFileLifetime = 24 * time.Hour

// expiredColumn 按数据库时钟计算文件过期标记，未设置过期时间的文件视为已过期。
const expiredColumn = "(f.expires_at IS NULL OR f.expires_at <= now()) AS expired"

// ErrFileNotFound 表示企业中没有可用的指定文件。
var ErrFileNotFound = errors.New("file not found")

// GetQuery 读取企业文件元数据。
type GetQuery struct {
	db *bun.DB
}

// Location 定义生成公开地址所需的文件位置。
type Location struct {
	ID             string                    `bun:"id"`
	StorageBackend domain.FileStorageBackend `bun:"storage_backend"`
	StorageKey     string                    `bun:"storage_key"`
}

// NewGetQuery 创建文件查询。
func NewGetQuery(db *bun.DB) *GetQuery {
	return &GetQuery{db: db}
}

// Execute 返回当前企业中的指定文件。
func (q *GetQuery) Execute(ctx context.Context, identity *servermodels.Identity, fileID string) (*servermodels.File, error) {
	return get(ctx, q.db, identity.Workspace.ID, fileID)
}

// ExecuteByStorageKey 返回当前企业中使用指定存储键的文件。
func (q *GetQuery) ExecuteByStorageKey(ctx context.Context, identity *servermodels.Identity, storageKey string) (*servermodels.File, error) {
	record := &servermodels.File{}
	err := selectFile(q.db, record).
		Where("f.workspace_id = ?", identity.Workspace.ID).
		Where("f.storage_key = ?", storageKey).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get file by storage key: %w", err)
	}
	return record, nil
}

// ListActiveLocations 批量返回当前企业已关联文件的存储位置。
func (q *GetQuery) ListActiveLocations(ctx context.Context, identity *servermodels.Identity, fileIDs []string) ([]Location, error) {
	fileIDs, valid := common.NormalizeUUIDs(fileIDs)
	if !valid {
		return nil, ErrFileNotFound
	}
	locations := make([]Location, 0, len(fileIDs))
	if len(fileIDs) == 0 {
		return locations, nil
	}
	if err := q.db.NewSelect().TableExpr("files AS f").
		ColumnExpr("f.id::text, f.storage_backend, f.storage_key").
		Where("f.workspace_id = ?", identity.Workspace.ID).
		Where("f.id IN (?)", bun.List(fileIDs)).
		Where("f.status = ?", domain.FileStatusActive).
		Scan(ctx, &locations); err != nil {
		return nil, fmt.Errorf("list active file locations: %w", err)
	}
	return locations, nil
}

// get 读取企业中的指定文件。
func get(ctx context.Context, db bun.IDB, workspaceID, fileID string) (*servermodels.File, error) {
	if !str.IsUUID(fileID) {
		return nil, ErrFileNotFound
	}
	record := &servermodels.File{}
	if err := selectFile(db, record).
		Where("f.id = ? AND f.workspace_id = ?", fileID, workspaceID).
		Scan(ctx); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFileNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}
	return record, nil
}

// selectFile 创建读取文件全部列及过期标记的查询。
func selectFile(db bun.IDB, record *servermodels.File) *bun.SelectQuery {
	return db.NewSelect().Model(record).ColumnExpr("f.*").ColumnExpr(expiredColumn)
}
