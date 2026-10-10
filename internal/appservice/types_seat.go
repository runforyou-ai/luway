package appservice

// WorkspaceSeats 定义工作区的席位上限与启用的成员数；每个启用的成员占一个席位，Limit 为 0 表示不限。
type WorkspaceSeats struct {
	Limit int `json:"limit"`
	Used  int `json:"used"`
}
