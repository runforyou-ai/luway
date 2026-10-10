//go:build server

// Package server 从本机固定位置的配置文件加载并校验企业服务端的启动配置：连接部署所需的数据库与可选的 NATS，以及各服务器可以不同的监听端口、HTTPS 端口、出口 IP 与数据目录；整个部署共用的部署配置保存在数据库中。
package server

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adrg/xdg"
	"github.com/goccy/go-yaml"
	"github.com/nats-io/nkeys"
	"github.com/runforyou-ai/luway/internal/common/brand"
)

// FileName 是配置文件名。
const FileName = "server.yaml"

// natsNamespacePattern 是 NATS 主题命名空间的格式。
var natsNamespacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Config 定义服务端启动配置。
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	NATS     NATSConfig     `yaml:"nats"`
	Log      LogConfig      `yaml:"log"`
	Data     DataConfig     `yaml:"data"`
}

// ServerConfig 定义 HTTP 与 HTTPS 服务监听配置、可信反向代理提供的请求头与本服务器的出口 IP。
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	// HTTPSPort 是本服务器直接提供 HTTPS 的端口，使用部署地址的证书并转发给 Port；为 0 表示由服务器前面的代理提供 HTTPS。
	HTTPSPort int `yaml:"httpsPort"`
	// ClientIPHeader 是反向代理写入的请求来源 IP 请求头名称，用于公开入口按 IP 限速；为空时直接提供 HTTPS 的服务器取 HTTPS 入口写入的访问者地址，其余取连接对端地址；反向代理必须覆盖客户端同名请求头。
	ClientIPHeader string `yaml:"clientIPHeader"`
	// CountryHeader 是反向代理写入的请求来源国家代码请求头名称，用于记录网站访客与新建工作区的地区；为空时不采集地区；反向代理必须覆盖客户端同名请求头。
	CountryHeader string `yaml:"countryHeader"`
	// EgressIP 是本服务器访问外部接口使用的公网出口 IP，供管理员加入微信等平台的 IP 白名单；为空表示未配置。
	EgressIP string `yaml:"egressIP"`
}

// DatabaseConfig 定义 PostgreSQL 连接配置。
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Name     string `yaml:"name"`
	SSLMode  string `yaml:"sslMode"`
}

// NATSConfig 定义任务队列与消息总线使用的 NATS；URL 为空时任务队列使用进程内嵌的 NATS，消息总线经 PostgreSQL 传输。
type NATSConfig struct {
	URL string `yaml:"url"`
	// CalloutSeed 是外部 NATS auth_callout issuer 对应的账户私钥种子。
	CalloutSeed string `yaml:"calloutSeed"`
	// WebSocketURL 是本机代理访问外部 NATS WebSocket 的 HTTP 或 HTTPS 内网地址。
	WebSocketURL string `yaml:"webSocketURL"`
	// SystemURL 是携带 SYS 账户凭据的 NATS 地址，用于强制撤销连接。
	SystemURL string `yaml:"systemURL"`
	// Namespace 是全部主题与任务队列的前缀，同一部署的服务器使用相同命名空间，默认 app。
	Namespace string `yaml:"namespace"`
	// Replicas 是任务队列在 NATS 集群中的副本数，默认 1；内嵌 NATS 只能为 1。
	Replicas int `yaml:"replicas"`
}

// LogConfig 定义控制台日志的输出级别；服务端日志表始终记录全部级别。
type LogConfig struct {
	// Level 是控制台输出的最低级别：debug、info、warn 或 error，默认 info。
	Level string `yaml:"level"`
}

// DataConfig 定义本服务器的数据目录。
type DataConfig struct {
	// Directory 是数据目录的绝对路径，本地文件写入其下的 files；为空时使用平台数据目录下以服务端程序命名的目录。
	Directory string `yaml:"directory"`
}

// FilesDirectory 返回本地文件目录：数据目录下的 files。
func (config DataConfig) FilesDirectory() string {
	return filepath.Join(config.Directory, "files")
}

// logLevels 是控制台日志级别取值对应的 slog 级别。
var logLevels = map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}

// SlogLevel 返回控制台日志级别对应的 slog 级别。
func (config LogConfig) SlogLevel() slog.Level {
	return logLevels[config.Level]
}

// Path 返回配置文件的固定位置：平台配置目录下以服务端程序命名的目录中的 server.yaml。
func Path() string {
	return filepath.Join(xdg.ConfigHome, directoryName(), FileName)
}

// Load 读取并校验配置文件，配置文件不存在时报错。
func Load() (Config, error) {
	path := Path()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("配置文件 %s 不存在，请用 config set 写入启动配置", path)
	}
	if err != nil {
		return Config{}, fmt.Errorf("读取服务端配置文件: %w", err)
	}
	config := defaultConfig()
	if err := yaml.UnmarshalWithOptions(data, &config, yaml.Strict()); err != nil {
		return Config{}, fmt.Errorf("解析服务端配置文件: %w", err)
	}
	config.normalize()
	if err := config.validate(); err != nil {
		return Config{}, fmt.Errorf("配置文件 %s: %w", path, err)
	}
	return config, nil
}

// directoryName 返回配置目录与数据目录的名称：服务端程序名。
func directoryName() string {
	return brand.Build().Slug + "-server"
}

// normalize 统一配置中的枚举和空白字符。
func (config *Config) normalize() {
	config.Server.Host = strings.TrimSpace(config.Server.Host)
	config.Server.ClientIPHeader = strings.TrimSpace(config.Server.ClientIPHeader)
	config.Server.CountryHeader = strings.TrimSpace(config.Server.CountryHeader)
	config.Server.EgressIP = strings.TrimSpace(config.Server.EgressIP)
	config.Database.Host = strings.TrimSpace(config.Database.Host)
	config.Database.User = strings.TrimSpace(config.Database.User)
	config.Database.Password = strings.TrimSpace(config.Database.Password)
	config.Database.Name = strings.TrimSpace(config.Database.Name)
	config.Database.SSLMode = strings.ToLower(strings.TrimSpace(config.Database.SSLMode))
	config.NATS.URL = strings.TrimSpace(config.NATS.URL)
	config.NATS.CalloutSeed = strings.TrimSpace(config.NATS.CalloutSeed)
	config.NATS.WebSocketURL = strings.TrimSpace(config.NATS.WebSocketURL)
	config.NATS.SystemURL = strings.TrimSpace(config.NATS.SystemURL)
	config.NATS.Namespace = strings.TrimSpace(config.NATS.Namespace)
	config.Log.Level = strings.ToLower(strings.TrimSpace(config.Log.Level))
	config.Data.Directory = strings.TrimSpace(config.Data.Directory)
	if config.Data.Directory == "" {
		config.Data.Directory = filepath.Join(xdg.DataHome, directoryName())
	}
}

// defaultConfig 返回服务端默认配置。
func defaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
		NATS: NATSConfig{Namespace: "app", Replicas: 1},
		Log:  LogConfig{Level: "info"},
	}
}

// validate 校验服务端配置。
func (config Config) validate() error {
	// 校验监听主机名、IPv4 地址和带方括号的 IPv6 地址。
	host := config.Server.Host
	validHost := host != "" && !strings.ContainsAny(host, "/\\ \t\r\n")
	if validHost && strings.Contains(host, ":") {
		validHost = len(host) >= 3 && host[0] == '[' && host[len(host)-1] == ']' && net.ParseIP(host[1:len(host)-1]) != nil
	}
	if !validHost {
		return fmt.Errorf("服务监听地址无效")
	}
	if config.Server.Port < 1 || config.Server.Port > 65535 {
		return fmt.Errorf("服务监听端口必须在 1 到 65535 之间")
	}
	if config.Server.EgressIP != "" && net.ParseIP(config.Server.EgressIP) == nil {
		return fmt.Errorf("server.egressIP 必须是 IP 地址")
	}
	if config.Database.Host == "" {
		return fmt.Errorf("必须配置 database.host")
	}
	if config.Database.Port < 1 || config.Database.Port > 65535 {
		return fmt.Errorf("database.port 必须是 1 到 65535 之间的整数")
	}
	if config.Database.User == "" {
		return fmt.Errorf("必须配置 database.user")
	}
	if config.Database.Password == "" {
		return fmt.Errorf("必须配置 database.password")
	}
	if config.Database.Name == "" {
		return fmt.Errorf("必须配置 database.name")
	}
	switch config.Database.SSLMode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return fmt.Errorf("database.sslMode 必须是 disable、allow、prefer、require、verify-ca 或 verify-full")
	}
	if !natsNamespacePattern.MatchString(config.NATS.Namespace) {
		return fmt.Errorf("NATS 命名空间必须匹配 %s", natsNamespacePattern.String())
	}
	if config.NATS.Replicas < 1 || config.NATS.Replicas > 5 || (config.NATS.URL == "" && config.NATS.Replicas != 1) {
		return fmt.Errorf("nats.replicas 必须在 1 到 5 之间，未配置 nats.url 时只能为 1")
	}
	if err := config.NATS.ValidateRealtime(); err != nil {
		return err
	}
	if _, known := logLevels[config.Log.Level]; !known {
		return fmt.Errorf("log.level 必须是 debug、info、warn 或 error")
	}
	if config.Server.HTTPSPort != 0 && (config.Server.HTTPSPort < 1 || config.Server.HTTPSPort > 65535 || config.Server.HTTPSPort == config.Server.Port) {
		return fmt.Errorf("HTTPS 端口必须在 1 到 65535 之间且不同于服务监听端口")
	}
	if !filepath.IsAbs(config.Data.Directory) {
		return fmt.Errorf("data.directory 必须是绝对路径")
	}
	return nil
}

// RealtimePrefix 把命名空间中的下划线和连字符转义为互不冲突的实时主题前缀。
func (config NATSConfig) RealtimePrefix() string {
	return strings.NewReplacer("_", "__", "-", "_h").Replace(config.Namespace) + "_realtime"
}

// ValidateRealtime 校验外部 NATS 的签名种子、代理目标与系统账户连接配置。
func (config NATSConfig) ValidateRealtime() error {
	if config.URL == "" {
		if config.CalloutSeed != "" || config.WebSocketURL != "" || config.SystemURL != "" {
			return errors.New("内嵌 NATS 自动管理签名与 WebSocket，外部配置须与 nats.url 一起填写")
		}
		return nil
	}
	key, err := nkeys.FromSeed([]byte(config.CalloutSeed))
	if err != nil {
		return errors.New("nats.calloutSeed 必须是账户私钥种子")
	}
	defer key.Wipe()
	public, err := key.PublicKey()
	if err != nil || !nkeys.IsValidPublicAccountKey(public) {
		return errors.New("nats.calloutSeed 必须是账户私钥种子")
	}
	ws, err := url.Parse(config.WebSocketURL)
	if err != nil || ws.Host == "" || (ws.Scheme != "http" && ws.Scheme != "https") || ws.User != nil || ws.RawQuery != "" || ws.Fragment != "" {
		return errors.New("nats.webSocketURL 必须是无凭据、查询参数与片段的 HTTP 或 HTTPS 地址")
	}
	for _, address := range append(strings.Split(config.URL, ","), strings.Split(config.SystemURL, ",")...) {
		address = strings.TrimSpace(address)
		parsed, err := url.Parse(address)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "nats" && parsed.Scheme != "tls") || parsed.User == nil {
			return errors.New("nats.url 与 nats.systemURL 必须分别包含 APP 与 SYS 账户凭据")
		}
	}
	return nil
}
