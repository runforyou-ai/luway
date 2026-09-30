package domain

// ConnectionProbeCategory 标识连接测试探测的外部能力类别。
type ConnectionProbeCategory string

const (
	ConnectionProbeModelProvider ConnectionProbeCategory = "model_provider"
	ConnectionProbeTelegram      ConnectionProbeCategory = "telegram"
	ConnectionProbeMCPServer     ConnectionProbeCategory = "mcp_server"
	ConnectionProbeWebSearch     ConnectionProbeCategory = "web_search"
)

// ConnectionProbeLocation 标识连接测试实际执行的位置。
type ConnectionProbeLocation string

const (
	ConnectionProbeServer ConnectionProbeLocation = "server"
)
