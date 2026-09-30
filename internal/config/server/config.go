//go:build server

// Package server 统一加载并校验企业服务端运行配置。
package server

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/email"
)

var natsNamespacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// deploymentNameMaxLength 是部署名称的最大字符数。
const deploymentNameMaxLength = 64

// operatorCredentialMinLength 是运营凭据的最小长度，运营接口经公网可达，凭据是其唯一访问控制手段。
const operatorCredentialMinLength = 32

// Config 定义服务端运行配置。
type Config struct {
	Deployment DeploymentConfig `yaml:"deployment"`
	Branding   BrandingConfig   `yaml:"branding"`
	Server     ServerConfig     `yaml:"server"`
	Database   DatabaseConfig   `yaml:"database"`
	NATS       NATSConfig       `yaml:"nats"`
	TLS        TLSConfig        `yaml:"tls"`
	Storage    StorageConfig    `yaml:"storage"`
	Email      EmailConfig      `yaml:"email"`
}

// DeploymentConfig 定义部署名称与形态、自托管的注册开关，以及官方托管所需的运营凭据、可信官方身份服务和 Web 客户端凭据。
type DeploymentConfig struct {
	// Name 是展示给连接者的部署名称，桌面端与移动端连接服务器后据此确认连对了部署；为空时界面展示部署地址。
	Name string                `yaml:"name"`
	Mode domain.DeploymentMode `yaml:"mode"`
	// RegistrationOpen 表示自托管部署是否允许任何人在登录页注册本地账号。
	RegistrationOpen                bool   `yaml:"registrationOpen"`
	OperatorCredential              string `yaml:"operatorCredential"`
	OfficialIdentityIssuer          string `yaml:"officialIdentityIssuer"`
	OfficialIdentityWebClientID     string `yaml:"officialIdentityWebClientId"`
	OfficialIdentityWebClientSecret string `yaml:"officialIdentityWebClientSecret"`
}

// BrandingConfig 定义部署级品牌覆盖，留空的字段沿用构建品牌。
type BrandingConfig struct {
	// Names 按界面语言标签覆盖产品名称，如 en-US、zh-CN。
	Names map[string]string `yaml:"names"`
	// SDKName 是网站嵌入脚本在宿主页注册的全局对象名。
	SDKName string `yaml:"sdkName"`
	// IconPath 是替换 Web 端网站图标的本地 PNG 文件路径。
	IconPath string `yaml:"iconPath"`
}

// Override 返回品牌覆盖值。
func (config BrandingConfig) Override() brand.Override {
	return brand.Override{Names: config.Names, SDKName: config.SDKName}
}

// ServerConfig 定义部署地址、HTTP 服务监听配置与可信反向代理提供的请求头。
type ServerConfig struct {
	// PublicURL 是各端连接和 Web 访问使用的部署地址，也是服务端生成对外链接的根地址。
	PublicURL string `yaml:"publicURL"`
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	// VisitorCountryHeader 是反向代理写入的访客国家代码请求头名称，为空时不采集访客地区；反向代理必须覆盖客户端同名请求头。
	VisitorCountryHeader string `yaml:"visitorCountryHeader"`
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

// NATSConfig 定义 NATS 连接和任务命名空间。
type NATSConfig struct {
	URL       string `yaml:"url"`
	Namespace string `yaml:"namespace"`
}

// TLSConfig 定义企业服务端 TLS 入口配置。
type TLSConfig struct {
	Mode      string `yaml:"mode"`
	ACMEEmail string `yaml:"acmeEmail"`
}

// StorageConfig 定义企业服务端文件存储配置。
type StorageConfig struct {
	LocalDirectory string   `yaml:"localDirectory"`
	S3             S3Config `yaml:"s3"`
}

// S3Config 定义 S3 兼容对象存储配置，关闭时文件写入本地目录。
type S3Config struct {
	Enabled         bool   `yaml:"enabled"`
	Endpoint        string `yaml:"endpoint"`
	PublicBaseURL   string `yaml:"publicBaseURL"`
	Region          string `yaml:"region"`
	Bucket          string `yaml:"bucket"`
	AccessKeyID     string `yaml:"accessKeyID"`
	SecretAccessKey string `yaml:"secretAccessKey"`
	ForcePathStyle  bool   `yaml:"forcePathStyle"`
}

// EmailConfig 定义部署级邮件发送配置。
type EmailConfig struct {
	SMTP SMTPConfig `yaml:"smtp"`
}

// SMTPConfig 定义 SMTP 发信服务，主机为空时关闭邮件发送。
type SMTPConfig struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
	Security    string `yaml:"security"`
	FromAddress string `yaml:"fromAddress"`
}

// Enabled 判断是否配置了 SMTP 发信服务。
func (config SMTPConfig) Enabled() bool {
	return config.Host != ""
}

// Load 从显式配置文件和环境变量加载服务端配置。
func Load(path string) (Config, error) {
	config := defaultConfig()
	if strings.TrimSpace(path) != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("读取服务端配置文件: %w", err)
		}
		if err := yaml.UnmarshalWithOptions(data, &config, yaml.Strict()); err != nil {
			return Config{}, fmt.Errorf("解析服务端配置文件: %w", err)
		}
	}
	if err := applyEnvironment(&config); err != nil {
		return Config{}, err
	}
	config.normalize()
	if err := config.validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// normalize 统一配置中的枚举和空白字符。
func (config *Config) normalize() {
	config.Deployment.Mode = domain.DeploymentMode(strings.ToLower(strings.TrimSpace(string(config.Deployment.Mode))))
	config.Deployment.OperatorCredential = strings.TrimSpace(config.Deployment.OperatorCredential)
	config.Deployment.OfficialIdentityIssuer = strings.TrimSpace(config.Deployment.OfficialIdentityIssuer)
	config.Deployment.OfficialIdentityWebClientID = strings.TrimSpace(config.Deployment.OfficialIdentityWebClientID)
	config.Deployment.OfficialIdentityWebClientSecret = strings.TrimSpace(config.Deployment.OfficialIdentityWebClientSecret)
	for locale, name := range config.Branding.Names {
		config.Branding.Names[locale] = strings.TrimSpace(name)
	}
	config.Branding.SDKName = strings.TrimSpace(config.Branding.SDKName)
	config.Branding.IconPath = strings.TrimSpace(config.Branding.IconPath)
	config.Server.PublicURL = strings.TrimRight(strings.TrimSpace(config.Server.PublicURL), "/")
	config.Server.Host = strings.TrimSpace(config.Server.Host)
	config.Server.VisitorCountryHeader = strings.TrimSpace(config.Server.VisitorCountryHeader)
	config.Database.Host = strings.TrimSpace(config.Database.Host)
	config.Database.User = strings.TrimSpace(config.Database.User)
	config.Database.Password = strings.TrimSpace(config.Database.Password)
	config.Database.Name = strings.TrimSpace(config.Database.Name)
	config.Database.SSLMode = strings.ToLower(strings.TrimSpace(config.Database.SSLMode))
	config.NATS.URL = strings.TrimSpace(config.NATS.URL)
	config.NATS.Namespace = strings.TrimSpace(config.NATS.Namespace)
	config.TLS.Mode = strings.ToLower(strings.TrimSpace(config.TLS.Mode))
	config.TLS.ACMEEmail = strings.TrimSpace(config.TLS.ACMEEmail)
	config.Storage.LocalDirectory = strings.TrimSpace(config.Storage.LocalDirectory)
	config.Storage.S3.Endpoint = strings.TrimRight(strings.TrimSpace(config.Storage.S3.Endpoint), "/")
	config.Storage.S3.PublicBaseURL = strings.TrimRight(strings.TrimSpace(config.Storage.S3.PublicBaseURL), "/")
	config.Storage.S3.Region = strings.TrimSpace(config.Storage.S3.Region)
	config.Storage.S3.Bucket = strings.TrimSpace(config.Storage.S3.Bucket)
	config.Storage.S3.AccessKeyID = strings.TrimSpace(config.Storage.S3.AccessKeyID)
	config.Storage.S3.SecretAccessKey = strings.TrimSpace(config.Storage.S3.SecretAccessKey)
	config.Email.SMTP.Host = strings.TrimSpace(config.Email.SMTP.Host)
	config.Email.SMTP.Username = strings.TrimSpace(config.Email.SMTP.Username)
	config.Email.SMTP.Security = strings.ToLower(strings.TrimSpace(config.Email.SMTP.Security))
	config.Email.SMTP.FromAddress = strings.TrimSpace(config.Email.SMTP.FromAddress)
}

// defaultConfig 返回服务端默认配置。
func defaultConfig() Config {
	return Config{
		Deployment: DeploymentConfig{Mode: domain.DeploymentModeSelfHosted},
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: 8080,
		},
		TLS: TLSConfig{Mode: "off"},
		Storage: StorageConfig{
			LocalDirectory: "data/files",
		},
		Email: EmailConfig{SMTP: SMTPConfig{Port: 587, Security: "starttls"}},
	}
}

// applyEnvironment 使用已设置的环境变量覆盖文件配置。
func applyEnvironment(config *Config) error {
	applyDeploymentModeEnvironment("DEPLOYMENT_MODE", &config.Deployment.Mode)
	applyStringEnvironment("OPERATOR_CREDENTIAL", &config.Deployment.OperatorCredential)
	applyStringEnvironment("OFFICIAL_IDENTITY_ISSUER", &config.Deployment.OfficialIdentityIssuer)
	applyStringEnvironment("OFFICIAL_IDENTITY_WEB_CLIENT_ID", &config.Deployment.OfficialIdentityWebClientID)
	applyStringEnvironment("OFFICIAL_IDENTITY_WEB_CLIENT_SECRET", &config.Deployment.OfficialIdentityWebClientSecret)
	applyStringEnvironment("PUBLIC_URL", &config.Server.PublicURL)
	applyStringEnvironment("DEPLOYMENT_NAME", &config.Deployment.Name)
	applyBrandNameEnvironment("BRAND_NAME", &config.Branding.Names)
	applyStringEnvironment("BRAND_SDK_NAME", &config.Branding.SDKName)
	applyStringEnvironment("BRAND_ICON_PATH", &config.Branding.IconPath)
	applyStringEnvironment("WAILS_SERVER_HOST", &config.Server.Host)
	applyStringEnvironment("VISITOR_COUNTRY_HEADER", &config.Server.VisitorCountryHeader)
	applyStringEnvironment("TLS_MODE", &config.TLS.Mode)
	applyStringEnvironment("TLS_ACME_EMAIL", &config.TLS.ACMEEmail)
	applyStringEnvironment("FILE_STORAGE_PATH", &config.Storage.LocalDirectory)
	applyStringEnvironment("S3_ENDPOINT", &config.Storage.S3.Endpoint)
	applyStringEnvironment("S3_PUBLIC_BASE_URL", &config.Storage.S3.PublicBaseURL)
	applyStringEnvironment("S3_REGION", &config.Storage.S3.Region)
	applyStringEnvironment("S3_BUCKET", &config.Storage.S3.Bucket)
	applyStringEnvironment("S3_ACCESS_KEY_ID", &config.Storage.S3.AccessKeyID)
	applyStringEnvironment("S3_SECRET_ACCESS_KEY", &config.Storage.S3.SecretAccessKey)
	applyStringEnvironment("SMTP_HOST", &config.Email.SMTP.Host)
	applyStringEnvironment("SMTP_USERNAME", &config.Email.SMTP.Username)
	applyStringEnvironment("SMTP_PASSWORD", &config.Email.SMTP.Password)
	applyStringEnvironment("SMTP_SECURITY", &config.Email.SMTP.Security)
	applyStringEnvironment("SMTP_FROM_ADDRESS", &config.Email.SMTP.FromAddress)
	applyStringEnvironment("POSTGRES_HOST", &config.Database.Host)
	applyStringEnvironment("POSTGRES_USER", &config.Database.User)
	applyStringEnvironment("POSTGRES_PASSWORD", &config.Database.Password)
	applyStringEnvironment("POSTGRES_DB", &config.Database.Name)
	applyStringEnvironment("POSTGRES_SSLMODE", &config.Database.SSLMode)
	applyStringEnvironment("NATS_URL", &config.NATS.URL)
	applyStringEnvironment("NATS_NAMESPACE", &config.NATS.Namespace)

	serverPort, err := intEnvironment("WAILS_SERVER_PORT", config.Server.Port)
	if err != nil {
		return err
	}
	config.Server.Port = serverPort
	databasePort, err := intEnvironment("POSTGRES_PORT", config.Database.Port)
	if err != nil {
		return err
	}
	config.Database.Port = databasePort
	smtpPort, err := intEnvironment("SMTP_PORT", config.Email.SMTP.Port)
	if err != nil {
		return err
	}
	config.Email.SMTP.Port = smtpPort
	if err := applyBoolEnvironment("REGISTRATION_OPEN", &config.Deployment.RegistrationOpen); err != nil {
		return err
	}
	if err := applyBoolEnvironment("S3_ENABLED", &config.Storage.S3.Enabled); err != nil {
		return err
	}
	if err := applyBoolEnvironment("S3_FORCE_PATH_STYLE", &config.Storage.S3.ForcePathStyle); err != nil {
		return err
	}
	return nil
}

// validate 校验服务端配置。
func (config Config) validate() error {
	if err := config.Deployment.validate(); err != nil {
		return err
	}
	if err := config.Branding.validate(); err != nil {
		return err
	}
	// 部署地址是不带路径、查询、片段和凭据的完整 HTTP 地址，托管部署必须使用 HTTPS。
	publicURL, err := url.Parse(config.Server.PublicURL)
	if err != nil || (publicURL.Scheme != "https" && publicURL.Scheme != "http") || publicURL.Host == "" ||
		publicURL.User != nil || publicURL.Path != "" || publicURL.RawQuery != "" || publicURL.Fragment != "" {
		return fmt.Errorf("必须配置 server.publicURL 或 PUBLIC_URL，且为不带路径的完整 HTTP 地址")
	}
	if config.Deployment.Mode.Managed() && publicURL.Scheme != "https" {
		return fmt.Errorf("managed 模式下 server.publicURL 必须是 HTTPS 地址")
	}
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
		return fmt.Errorf("必须配置 database.host 或 POSTGRES_HOST")
	}
	if config.Database.Port < 1 || config.Database.Port > 65535 {
		return fmt.Errorf("database.port 必须是 1 到 65535 之间的整数")
	}
	if config.Database.User == "" {
		return fmt.Errorf("必须配置 database.user 或 POSTGRES_USER")
	}
	if config.Database.Password == "" {
		return fmt.Errorf("必须配置 database.password 或 POSTGRES_PASSWORD")
	}
	if config.Database.Name == "" {
		return fmt.Errorf("必须配置 database.name 或 POSTGRES_DB")
	}
	switch config.Database.SSLMode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return fmt.Errorf("database.sslMode 必须是 disable、allow、prefer、require、verify-ca 或 verify-full")
	}
	if config.NATS.URL == "" {
		return fmt.Errorf("必须配置 NATS 地址")
	}
	if !natsNamespacePattern.MatchString(config.NATS.Namespace) {
		return fmt.Errorf("NATS 命名空间必须匹配 %s", natsNamespacePattern.String())
	}
	mode := config.TLS.Mode
	if mode != "auto" && mode != "external" && mode != "off" {
		return fmt.Errorf("TLS 模式必须是 auto、external 或 off")
	}
	if mode == "auto" && (config.Server.Port == 80 || config.Server.Port == 443) {
		return fmt.Errorf("TLS 自动模式下服务监听端口不能是 80 或 443")
	}
	if config.Storage.LocalDirectory == "" {
		return fmt.Errorf("必须配置本地文件存储目录")
	}
	if config.Storage.S3.Enabled {
		if !common.ValidHTTPBaseURL(config.Storage.S3.Endpoint) {
			return fmt.Errorf("storage.s3.endpoint 必须是完整的 HTTP 地址")
		}
		if !common.ValidHTTPBaseURL(config.Storage.S3.PublicBaseURL) {
			return fmt.Errorf("storage.s3.publicBaseURL 必须是客户端可访问的完整 HTTP 地址")
		}
		if config.Storage.S3.Region == "" {
			return fmt.Errorf("必须配置 storage.s3.region 或 S3_REGION")
		}
		if config.Storage.S3.Bucket == "" {
			return fmt.Errorf("必须配置 storage.s3.bucket 或 S3_BUCKET")
		}
		if config.Storage.S3.AccessKeyID == "" {
			return fmt.Errorf("必须配置 storage.s3.accessKeyID 或 S3_ACCESS_KEY_ID")
		}
		if config.Storage.S3.SecretAccessKey == "" {
			return fmt.Errorf("必须配置 storage.s3.secretAccessKey 或 S3_SECRET_ACCESS_KEY")
		}
	}
	return config.Email.SMTP.validate()
}

// validate 校验已开启的 SMTP 发信配置。
func (config SMTPConfig) validate() error {
	if !config.Enabled() {
		return nil
	}
	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("email.smtp.port 必须是 1 到 65535 之间的整数")
	}
	if config.Security != "starttls" && config.Security != "tls" && config.Security != "none" {
		return fmt.Errorf("email.smtp.security 必须是 starttls、tls 或 none")
	}
	if (config.Username == "") != (config.Password == "") {
		return fmt.Errorf("email.smtp.username 与 email.smtp.password 必须同时配置")
	}
	if !email.Valid(config.FromAddress) {
		return fmt.Errorf("必须配置有效的 email.smtp.fromAddress 或 SMTP_FROM_ADDRESS")
	}
	return nil
}

// validate 校验品牌覆盖后的品牌和网站图标文件。
func (config BrandingConfig) validate() error {
	if err := brand.Build().WithOverride(config.Override()).Validate(); err != nil {
		return fmt.Errorf("branding 无效: %w", err)
	}
	if config.IconPath == "" {
		return nil
	}
	if !strings.EqualFold(filepath.Ext(config.IconPath), ".png") {
		return fmt.Errorf("branding.iconPath 必须是 PNG 文件")
	}
	if info, err := os.Stat(config.IconPath); err != nil || info.IsDir() {
		return fmt.Errorf("branding.iconPath 指向的文件不可读取")
	}
	return nil
}

// validate 校验部署名称、部署形态及托管部署必需的配置。
func (config DeploymentConfig) validate() error {
	if config.Name != strings.TrimSpace(config.Name) || len([]rune(config.Name)) > deploymentNameMaxLength {
		return fmt.Errorf("deployment.name 不能以空白开头或结尾，且不超过 %d 个字符", deploymentNameMaxLength)
	}
	if !config.Mode.Valid() {
		return fmt.Errorf("deployment.mode 必须是 self_hosted 或 managed")
	}
	if !config.Mode.Managed() {
		if config.OperatorCredential != "" || config.OfficialIdentityIssuer != "" ||
			config.OfficialIdentityWebClientID != "" || config.OfficialIdentityWebClientSecret != "" {
			return fmt.Errorf("deployment.operatorCredential 和 deployment.officialIdentity* 只在 managed 模式下使用")
		}
		return nil
	}
	// 托管部署的账号来自官方身份服务，不提供本地注册。
	if config.RegistrationOpen {
		return fmt.Errorf("deployment.registrationOpen 只在 self_hosted 模式下使用")
	}
	if len([]rune(config.OperatorCredential)) < operatorCredentialMinLength {
		return fmt.Errorf("deployment.operatorCredential 至少需要 %d 个字符", operatorCredentialMinLength)
	}
	// 官方身份服务的 issuer 是不带凭据、查询和片段的 HTTPS 地址，按原样与 ID Token 的 iss 比对。
	issuer, err := url.Parse(config.OfficialIdentityIssuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return fmt.Errorf("deployment.officialIdentityIssuer 必须是完整的 HTTPS 地址")
	}
	if config.OfficialIdentityWebClientID == "" || config.OfficialIdentityWebClientSecret == "" {
		return fmt.Errorf("deployment.officialIdentityWebClientId 和 deployment.officialIdentityWebClientSecret 不能为空")
	}
	return nil
}

// applyDeploymentModeEnvironment 覆盖非空部署形态环境变量。
func applyDeploymentModeEnvironment(name string, target *domain.DeploymentMode) {
	value, ok := os.LookupEnv(name)
	if ok && strings.TrimSpace(value) != "" {
		*target = domain.DeploymentMode(strings.TrimSpace(value))
	}
}

// applyStringEnvironment 覆盖非空字符串环境变量。
func applyStringEnvironment(name string, target *string) {
	value, ok := os.LookupEnv(name)
	if ok && strings.TrimSpace(value) != "" {
		*target = strings.TrimSpace(value)
	}
}

// applyBrandNameEnvironment 用非空环境变量覆盖构建品牌所有语言的产品名称。
func applyBrandNameEnvironment(name string, target *map[string]string) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return
	}
	names := make(map[string]string)
	for locale := range brand.Build().Names {
		names[locale] = strings.TrimSpace(value)
	}
	*target = names
}

// applyBoolEnvironment 覆盖非空布尔环境变量。
func applyBoolEnvironment(name string, target *bool) error {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s 必须是布尔值", name)
	}
	*target = parsed
	return nil
}

// intEnvironment 读取整数环境变量。
func intEnvironment(name string, fallback int) (int, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s 必须是整数", name)
	}
	return parsed, nil
}
