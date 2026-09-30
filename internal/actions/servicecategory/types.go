//go:build server

// Package servicecategory 实现企业咨询分类目录的查询和维护。
package servicecategory

import (
	"errors"
	"time"

	"github.com/runforyou-ai/cervi/internal/common"
)

var (
	// ErrNotFound 表示当前企业中不存在指定的未归档咨询分类。
	ErrNotFound = errors.New("service category not found")
	// ErrLimitReached 表示企业未归档咨询分类已达数量上限。
	ErrLimitReached = errors.New("service category limit reached")
)

const (
	ValidationNameRequired       common.FieldCode = "SERVICE_CATEGORY_NAME_REQUIRED"
	ValidationNameTooLong        common.FieldCode = "SERVICE_CATEGORY_NAME_TOO_LONG"
	ValidationNameDuplicate      common.FieldCode = "SERVICE_CATEGORY_NAME_DUPLICATE"
	ValidationDescriptionTooLong common.FieldCode = "SERVICE_CATEGORY_DESCRIPTION_TOO_LONG"
	ValidationTeamInvalid        common.FieldCode = "SERVICE_CATEGORY_TEAM_INVALID"
)

// Input 定义咨询分类可编辑字段；TeamID 为空表示按渠道失败路由。
type Input struct {
	Name        string
	Description string
	TeamID      *string
}

// Record 定义咨询分类详情字段。
type Record struct {
	ID          string    `bun:"id"`
	Name        string    `bun:"name"`
	Description string    `bun:"description"`
	TeamID      *string   `bun:"team_id"`
	TeamName    *string   `bun:"team_name"`
	CreatedAt   time.Time `bun:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at"`
}
