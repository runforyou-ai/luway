//go:build server

// Package server 从本机固定位置的配置文件加载并校验企业服务端的启动配置：连接部署所需的数据库与可选的 NATS，以及各服务器可以不同的监听端口、HTTPS 端口与数据目录；整个部署共用的部署配置保存在数据库中。
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
	"strings"

	"github.com/adrg/xdg"
	"github.com/goccy/go-yaml"
	"github.com/runforyou-ai/luway/internal/common/brand"
)

// FileName 是配置文件名。
const FileName = "server.yaml"

// Config 定义服务端启动配置。
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	NATS     NATSConfig     `yaml:"nats"`
	Log      LogConfig      `yaml:"log"`
	Data     DataConfig     `yaml:"data"`
	// Extension 是程序组成专属的启动配置项，键与取值由挂接内容读取和校验，核心不解释。
	Extension map[string]string `yaml:"extension"`
}

// ServerConfig 定义 HTTP 与 HTTPS 服务监听配置、可信反向代理提供的请求头。
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	// HTTPSPort 是本服务器直接提供 HTTPS 的端口，使用部署地址的证书并转发给 Port；为 0 表示由服务器前面的代理提供 HTTPS。
	HTTPSPort int `yaml:"httpsPort"`
	// ClientIPHeader 是反向代理写入的请求来源 IP 请求头名称，用于公开入口按 IP 限速；为空时直接提供 HTTPS 的服务器取 HTTPS 入口写入的访问者地址，其余取连接对端地址；反向代理必须覆盖客户端同名请求头。
	ClientIPHeader string `yaml:"clientIPHeader"`
	// CountryHeader 是反向代理写入的请求来源国家代码请求头名称，用于记录网站访客与新建工作区的地区；为空时不采集地区；反向代理必须覆盖客户端同名请求头。
	CountryHeader string `yaml:"countryHeader"`
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

// NATSConfig 定义内部消息服务地址；为空时启动持久化的内嵌 NATS。
type NATSConfig struct {
	URL string `yaml:"url"`
}

// LogConfig 定义控制台与本机文件日志的输出级别。
type LogConfig struct {
	// Level 是控制台与本机文件输出的最低级别：debug、info、warn 或 error，默认 info。
	Level string `yaml:"level"`
}

// DataConfig 定义本服务器的数据目录。
type DataConfig struct {
	// Directory 是数据目录的绝对路径；为空时使用平台数据目录下以服务端程序命名的目录。
	Directory string `yaml:"directory"`
}

// logLevels 是日志级别取值对应的 slog 级别。
var logLevels = map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}

// SlogLevel 返回日志级别对应的 slog 级别。
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
	config.Database.Host = strings.TrimSpace(config.Database.Host)
	config.Database.User = strings.TrimSpace(config.Database.User)
	config.Database.Password = strings.TrimSpace(config.Database.Password)
	config.Database.Name = strings.TrimSpace(config.Database.Name)
	config.Database.SSLMode = strings.ToLower(strings.TrimSpace(config.Database.SSLMode))
	config.NATS.URL = strings.TrimSpace(config.NATS.URL)
	config.Log.Level = strings.ToLower(strings.TrimSpace(config.Log.Level))
	config.Data.Directory = strings.TrimSpace(config.Data.Directory)
	for key, value := range config.Extension {
		config.Extension[key] = strings.TrimSpace(value)
	}
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
		Log: LogConfig{Level: "info"},
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
	if err := config.NATS.Validate(); err != nil {
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

// Validate 校验外部 NATS 的连接地址，空值使用内嵌服务。
func (config NATSConfig) Validate() error {
	if config.URL == "" {
		return nil
	}
	for _, address := range strings.Split(config.URL, ",") {
		u, err := url.Parse(strings.TrimSpace(address))
		if err != nil || u.Host == "" || (u.Scheme != "nats" && u.Scheme != "tls") || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("nats.url 必须是 NATS 或 TLS 连接地址")
		}
	}
	return nil
}
