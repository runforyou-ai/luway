//go:build server

package server

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/runforyou-ai/support/str"
)

// Diagnostics 定义写入诊断信息的服务端启动配置与本机目录，只包含不含密码和地址凭据的字段。
type Diagnostics struct {
	EgressIP       string              `json:"egressIP"`
	Listen         string              `json:"listen"`
	HTTPSPort      int                 `json:"httpsPort"`
	Database       DatabaseDiagnostics `json:"database"`
	NATS           NATSDiagnostics     `json:"nats"`
	LogLevel       string              `json:"logLevel"`
	LocalDirectory string              `json:"localDirectory"`
	ClientsDir     string              `json:"clientsDirectory"`
}

// DatabaseDiagnostics 定义 PostgreSQL 连接的地址、账号名、库名与 SSL 模式。
type DatabaseDiagnostics struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	Name    string `json:"name"`
	SSLMode string `json:"sslMode"`
}

// NATSDiagnostics 定义去掉凭据的 NATS 地址、命名空间与任务队列副本数。
type NATSDiagnostics struct {
	URL       string `json:"url"`
	Namespace string `json:"namespace"`
	Replicas  int    `json:"replicas"`
}

// Diagnostics 返回本配置中可写入诊断信息的字段、本地文件目录与客户端目录 clientsDirectory，地址中的凭据被去掉。
func (config Config) Diagnostics(clientsDirectory string) Diagnostics {
	return Diagnostics{
		EgressIP:  config.Server.EgressIP,
		Listen:    config.Server.Host + ":" + strconv.Itoa(config.Server.Port),
		HTTPSPort: config.Server.HTTPSPort,
		Database: DatabaseDiagnostics{
			Host: config.Database.Host, Port: config.Database.Port, User: config.Database.User,
			Name: config.Database.Name, SSLMode: config.Database.SSLMode,
		},
		NATS:           NATSDiagnostics{URL: withoutCredentials(config.NATS.URL), Namespace: config.NATS.Namespace, Replicas: config.NATS.Replicas},
		LogLevel:       config.Log.Level,
		LocalDirectory: config.Data.FilesDirectory(),
		ClientsDir:     clientsDirectory,
	}
}

// withoutCredentials 去掉逗号分隔的各个地址中的账号、密码、令牌和查询参数；没有协议前缀的地址去掉最后一个 @ 及之前的内容。
func withoutCredentials(addresses string) string {
	if addresses == "" {
		return ""
	}
	parts := strings.Split(addresses, ",")
	for index, part := range parts {
		part = strings.TrimSpace(part)
		parsed, err := url.Parse(part)
		if err != nil || parsed.Host == "" {
			parts[index] = str.AfterLast(part, "@")
			continue
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parts[index] = parsed.String()
	}
	return strings.Join(parts, ",")
}
