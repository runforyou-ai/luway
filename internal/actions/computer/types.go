//go:build server

// Package computer 实现电脑的注册、凭据认证、在线记录、操作领取与结果上报，并在电脑离线或撤销时结算派发给它且未结束的调用。
package computer

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// RegisterInput 定义执行器注册电脑时上报的本机信息。
type RegisterInput struct {
	InstallID string
	Name      string
	Platform  domain.ComputerPlatform
}

// Record 定义电脑记录，Online 表示执行器当前在线。
type Record struct {
	ID         string
	Name       string
	Platform   domain.ComputerPlatform
	Online     bool
	LastSeenAt *time.Time
	CreatedAt  time.Time
}

// Registration 定义注册结果与只返回一次的电脑凭据。
type Registration struct {
	Record     Record
	Credential string
}

// Identity 定义以电脑凭据认证的电脑。
type Identity struct {
	OrganizationID string
	ComputerID     string
}

// CapabilitiesInput 定义执行器上报的执行能力、版本与同时执行的操作上限。
type CapabilitiesInput struct {
	Capabilities    domain.ComputerCapabilities
	ExecutorVersion string
	MaxConcurrency  int
}

// Operation 定义执行器领取到的一次操作，编号即工具调用编号。
type Operation struct {
	ID        string
	Operation domain.ComputerOperation
}

// recordFromModel 转换电脑存储模型。
func recordFromModel(input servermodels.Computer, now time.Time) Record {
	return Record{
		ID: input.ID, Name: input.Name, Platform: input.Platform, Online: input.Online(now),
		LastSeenAt: input.LastSeenAt, CreatedAt: input.CreatedAt,
	}
}
