//go:build server

// Package device 实现成员本机设备的注册、查询与撤销。
package device

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// RegisterInput 定义设备注册上报的本机信息。
type RegisterInput struct {
	InstallID string
	Name      string
	Platform  domain.DevicePlatform
}

// Record 定义设备记录，LocalAgents 是设备上报的已安装且可用的本机 Agent。
type Record struct {
	ID          string
	Name        string
	Platform    domain.DevicePlatform
	LocalAgents []domain.LocalAgentKind
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
