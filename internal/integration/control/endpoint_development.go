//go:build !production

package control

// BaseURL 是本地开发 control 的服务地址。
const BaseURL = "http://localhost:8090"

// Environment 是上报错误事件时标注的运行环境。
const Environment = "development"
