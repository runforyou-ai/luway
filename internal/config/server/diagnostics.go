//go:build server

package server

import (
	"net/url"
	"strconv"
	"strings"
)

// Diagnostics 定义写入诊断信息的服务端配置，只包含不含密码、密钥和地址凭据的字段。
type Diagnostics struct {
	DeploymentName string              `json:"deploymentName"`
	PublicURL      string              `json:"publicURL"`
	Listen         string              `json:"listen"`
	TLSMode        string              `json:"tlsMode"`
	Database       DatabaseDiagnostics `json:"database"`
	NATS           NATSDiagnostics     `json:"nats"`
	Storage        StorageDiagnostics  `json:"storage"`
	SMTP           SMTPDiagnostics     `json:"smtp"`
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

// NATSDiagnostics 定义去掉凭据的 NATS 地址与命名空间。
type NATSDiagnostics struct {
	URL       string `json:"url"`
	Namespace string `json:"namespace"`
}

// StorageDiagnostics 定义文件存储方式：S3Enabled 为假时文件写入 LocalDirectory，为真时写入对象存储桶。
type StorageDiagnostics struct {
	LocalDirectory string `json:"localDirectory"`
	S3Enabled      bool   `json:"s3Enabled"`
	Endpoint       string `json:"endpoint"`
	PublicBaseURL  string `json:"publicBaseURL"`
	Region         string `json:"region"`
	Bucket         string `json:"bucket"`
	ForcePathStyle bool   `json:"forcePathStyle"`
}

// SMTPDiagnostics 定义邮件发送配置，Enabled 为假表示未配置 SMTP 主机。
type SMTPDiagnostics struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"`
	FromAddress string `json:"fromAddress"`
}

// Diagnostics 返回本配置中可写入诊断信息的字段，地址中的凭据被去掉。
func (config Config) Diagnostics() Diagnostics {
	return Diagnostics{
		DeploymentName: config.Deployment.Name,
		PublicURL:      config.Server.PublicURL,
		Listen:         config.Server.Host + ":" + strconv.Itoa(config.Server.Port),
		TLSMode:        config.TLS.Mode,
		Database: DatabaseDiagnostics{
			Host: config.Database.Host, Port: config.Database.Port, User: config.Database.User,
			Name: config.Database.Name, SSLMode: config.Database.SSLMode,
		},
		NATS: NATSDiagnostics{URL: withoutCredentials(config.NATS.URL), Namespace: config.NATS.Namespace},
		Storage: StorageDiagnostics{
			LocalDirectory: config.Storage.LocalDirectory, S3Enabled: config.Storage.S3.Enabled,
			Endpoint: withoutCredentials(config.Storage.S3.Endpoint), PublicBaseURL: withoutCredentials(config.Storage.S3.PublicBaseURL),
			Region: config.Storage.S3.Region, Bucket: config.Storage.S3.Bucket, ForcePathStyle: config.Storage.S3.ForcePathStyle,
		},
		SMTP: SMTPDiagnostics{
			Enabled: config.Email.SMTP.Enabled(), Host: config.Email.SMTP.Host, Port: config.Email.SMTP.Port,
			Security: config.Email.SMTP.Security, FromAddress: config.Email.SMTP.FromAddress,
		},
		ClientsDir: config.Clients.Directory,
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
			parts[index] = part[strings.LastIndex(part, "@")+1:]
			continue
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parts[index] = parsed.String()
	}
	return strings.Join(parts, ",")
}
