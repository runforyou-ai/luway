//go:build server

package device

import servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"

// recordFromModel 转换设备存储模型。
func recordFromModel(input servermodels.Device) Record {
	return Record{
		ID: input.ID, Name: input.Name, Platform: input.Platform, LocalAgents: input.LocalAgents,
		CreatedAt: input.CreatedAt, UpdatedAt: input.UpdatedAt,
	}
}
