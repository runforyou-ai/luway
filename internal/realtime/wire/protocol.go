// Package wire 定义实时网关与客户端之间的消息帧。
package wire

import "encoding/json"

// Frame 承载连接认证、频道订阅、恢复游标与业务事件。
type Frame struct {
	Op        string          `json:"op"`
	Ticket    string          `json:"ticket,omitempty"`
	Socket    string          `json:"socket,omitempty"`
	Channel   string          `json:"channel,omitempty"`
	Epoch     string          `json:"epoch,omitempty"`
	Position  uint64          `json:"position,omitempty"`
	Sequence  uint64          `json:"sequence,omitempty"`
	Previous  uint64          `json:"previous,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Info      json.RawMessage `json:"info,omitempty"`
	Recovered bool            `json:"recovered,omitempty"`
	Reason    string          `json:"reason,omitempty"`
}
