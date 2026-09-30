//go:build server

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// TestLoadMergesFileAndEnvironment 验证环境变量覆盖显式配置文件。
func TestLoadMergesFileAndEnvironment(t *testing.T) {
	clearServerEnvironment(t)
	t.Setenv("PUBLIC_URL", "https://app.example.com/")
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
server:
  host: 127.0.0.1
  port: 18080
database:
  host: file.internal
  port: 5432
  user: file
  password: file-secret
  name: file
  sslMode: disable
nats:
  url: nats://file:4222
  namespace: file
tls:
  mode: off
storage:
  localDirectory: data/files
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POSTGRES_HOST", "database.internal")
	t.Setenv("POSTGRES_PORT", "5433")
	t.Setenv("POSTGRES_USER", "environment")
	t.Setenv("POSTGRES_PASSWORD", "secret@value")
	t.Setenv("POSTGRES_DB", "app")
	t.Setenv("POSTGRES_SSLMODE", "require")
	t.Setenv("NATS_URL", "nats://environment:4222")
	t.Setenv("NATS_NAMESPACE", "environment")
	t.Setenv("WAILS_SERVER_PORT", "28080")
	t.Setenv("TLS_MODE", "external")
	t.Setenv("TLS_ACME_EMAIL", "admin@example.com")
	t.Setenv("FILE_STORAGE_PATH", t.TempDir())

	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Database.Host != "database.internal" || config.Database.Port != 5433 || config.Database.User != "environment" || config.Database.Password != "secret@value" || config.Database.Name != "app" || config.Database.SSLMode != "require" || config.Server.Port != 28080 {
		t.Fatalf("环境变量未覆盖文件配置: %#v", config)
	}
	if config.TLS.Mode != "external" || config.TLS.ACMEEmail != "admin@example.com" {
		t.Fatalf("TLS 环境变量未覆盖文件配置: %#v", config.TLS)
	}
	if config.NATS.URL != "nats://environment:4222" || config.NATS.Namespace != "environment" {
		t.Fatalf("NATS 环境变量未覆盖文件配置: %#v", config.NATS)
	}
}

// TestLoadRejectsUnknownFileField 验证配置文件会拒绝未知字段。
func TestLoadRejectsUnknownFileField(t *testing.T) {
	clearServerEnvironment(t)
	t.Setenv("PUBLIC_URL", "https://app.example.com/")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("unknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("未知配置字段未被拒绝")
	}
}

// TestValidationRequiresDatabaseName 验证必须指定数据库名称。
func TestValidationRequiresDatabaseName(t *testing.T) {
	config := validTestConfig()
	config.Database.Name = ""
	config.normalize()
	if err := config.validate(); err == nil {
		t.Fatal("接受了未指定数据库名称的配置")
	}
}

// TestValidationRejectsInvalidNATSConfig 验证 NATS 地址和命名空间。
func TestValidationRejectsInvalidNATSConfig(t *testing.T) {
	for _, nats := range []NATSConfig{
		{Namespace: "app"},
		{URL: "nats://127.0.0.1:4222", Namespace: "INVALID"},
	} {
		config := validTestConfig()
		config.NATS = nats
		config.normalize()
		if err := config.validate(); err == nil {
			t.Fatalf("接受了无效 NATS 配置: %#v", nats)
		}
	}
}

// clearServerEnvironment 清除可能影响配置测试的服务端环境变量。
func clearServerEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"WAILS_SERVER_HOST", "WAILS_SERVER_PORT",
		"POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB", "POSTGRES_SSLMODE",
		"NATS_URL", "NATS_NAMESPACE",
		"TLS_MODE", "TLS_ACME_EMAIL", "FILE_STORAGE_PATH",
		"S3_ENABLED", "S3_ENDPOINT", "S3_PUBLIC_BASE_URL", "S3_REGION", "S3_BUCKET",
		"S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY", "S3_FORCE_PATH_STYLE",
		"SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_SECURITY", "SMTP_FROM_ADDRESS",
		"PUBLIC_URL", "DEPLOYMENT_NAME", "REGISTRATION_OPEN", "DEPLOYMENT_MODE", "OPERATOR_CREDENTIAL", "OFFICIAL_IDENTITY_ISSUER",
		"OFFICIAL_IDENTITY_WEB_CLIENT_ID", "OFFICIAL_IDENTITY_WEB_CLIENT_SECRET",
		"BRAND_NAME", "BRAND_SDK_NAME", "BRAND_ICON_PATH",
	} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

// TestValidationRejectsAutoTLSPortConflict 验证 TLS 自动模式与服务监听端口的互异性。
func TestValidationRejectsAutoTLSPortConflict(t *testing.T) {
	config := validTestConfig()
	config.Server.Port = 443
	config.TLS.Mode = "auto"
	config.Storage.LocalDirectory = t.TempDir()
	config.normalize()
	if err := config.validate(); err == nil {
		t.Fatal("TLS 自动模式接受了 443 服务监听端口")
	}
}

// validTestConfig 返回满足基础校验的服务端测试配置。
func validTestConfig() Config {
	config := defaultConfig()
	config.Server.PublicURL = "https://app.example.com"
	config.Database.Host = "127.0.0.1"
	config.Database.Port = 5432
	config.Database.User = "app"
	config.Database.Password = "secret"
	config.Database.Name = "app"
	config.Database.SSLMode = "disable"
	config.NATS.URL = "nats://127.0.0.1:4222"
	config.NATS.Namespace = "app"
	return config
}

// TestStorageS3Environment 验证对象存储环境变量覆盖文件配置。
func TestStorageS3Environment(t *testing.T) {
	clearServerEnvironment(t)
	t.Setenv("PUBLIC_URL", "https://app.example.com/")
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
database:
  host: 127.0.0.1
  port: 5432
  user: app
  password: secret
  name: app
  sslMode: disable
nats:
  url: nats://127.0.0.1:4222
  namespace: app
storage:
  localDirectory: data/files
  s3:
    endpoint: https://file.example.com
    region: file-region
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("S3_ENABLED", "true")
	t.Setenv("S3_ENDPOINT", "https://s3.example.com/")
	t.Setenv("S3_PUBLIC_BASE_URL", "https://cdn.example.com/")
	t.Setenv("S3_REGION", "us-east-1")
	t.Setenv("S3_BUCKET", "app")
	t.Setenv("S3_ACCESS_KEY_ID", "access-key")
	t.Setenv("S3_SECRET_ACCESS_KEY", "secret-key")
	t.Setenv("S3_FORCE_PATH_STYLE", "true")

	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := S3Config{
		Enabled: true, Endpoint: "https://s3.example.com", PublicBaseURL: "https://cdn.example.com",
		Region: "us-east-1", Bucket: "app", AccessKeyID: "access-key", SecretAccessKey: "secret-key", ForcePathStyle: true,
	}
	if config.Storage.S3 != want {
		t.Fatalf("对象存储配置 = %#v, want %#v", config.Storage.S3, want)
	}
}

// TestValidationRequiresCompleteS3Setting 验证启用对象存储时必须填写完整配置。
func TestValidationRequiresCompleteS3Setting(t *testing.T) {
	complete := S3Config{
		Enabled: true, Endpoint: "https://s3.example.com", PublicBaseURL: "https://cdn.example.com",
		Region: "us-east-1", Bucket: "app", AccessKeyID: "access-key", SecretAccessKey: "secret-key",
	}
	config := validTestConfig()
	config.Storage.S3 = complete
	config.normalize()
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	// Enabled 为 false 时其余 S3 字段无需填写。
	config = validTestConfig()
	config.Storage.S3 = S3Config{Endpoint: "relative"}
	config.normalize()
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	for _, broken := range []S3Config{
		{Enabled: true, PublicBaseURL: complete.PublicBaseURL, Region: complete.Region, Bucket: complete.Bucket, AccessKeyID: complete.AccessKeyID, SecretAccessKey: complete.SecretAccessKey},
		{Enabled: true, Endpoint: "cdn.example.com", PublicBaseURL: complete.PublicBaseURL, Region: complete.Region, Bucket: complete.Bucket, AccessKeyID: complete.AccessKeyID, SecretAccessKey: complete.SecretAccessKey},
		{Enabled: true, Endpoint: complete.Endpoint, Region: complete.Region, Bucket: complete.Bucket, AccessKeyID: complete.AccessKeyID, SecretAccessKey: complete.SecretAccessKey},
		{Enabled: true, Endpoint: complete.Endpoint, PublicBaseURL: complete.PublicBaseURL, Bucket: complete.Bucket, AccessKeyID: complete.AccessKeyID, SecretAccessKey: complete.SecretAccessKey},
		{Enabled: true, Endpoint: complete.Endpoint, PublicBaseURL: complete.PublicBaseURL, Region: complete.Region, AccessKeyID: complete.AccessKeyID, SecretAccessKey: complete.SecretAccessKey},
		{Enabled: true, Endpoint: complete.Endpoint, PublicBaseURL: complete.PublicBaseURL, Region: complete.Region, Bucket: complete.Bucket, SecretAccessKey: complete.SecretAccessKey},
		{Enabled: true, Endpoint: complete.Endpoint, PublicBaseURL: complete.PublicBaseURL, Region: complete.Region, Bucket: complete.Bucket, AccessKeyID: complete.AccessKeyID},
	} {
		config := validTestConfig()
		config.Storage.S3 = broken
		config.normalize()
		if err := config.validate(); err == nil {
			t.Fatalf("接受了不完整的对象存储配置: %#v", broken)
		}
	}
}

// TestDeploymentDefaultsToSelfHosted 验证未配置部署形态时使用自托管，且不接受托管专用字段。
func TestDeploymentDefaultsToSelfHosted(t *testing.T) {
	config := validTestConfig()
	config.normalize()
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	if config.Deployment.Mode != domain.DeploymentModeSelfHosted {
		t.Fatalf("默认部署形态不是自托管: %q", config.Deployment.Mode)
	}
	config.Deployment.OperatorCredential = strings.Repeat("c", 32)
	if err := config.validate(); err == nil {
		t.Fatal("自托管模式接受了运营凭据")
	}
}

// TestManagedDeploymentValidation 验证托管部署的 HTTPS 部署地址、运营凭据和官方身份 issuer 校验。
func TestManagedDeploymentValidation(t *testing.T) {
	valid := func() Config {
		config := validTestConfig()
		config.Deployment = DeploymentConfig{
			Mode:                            domain.DeploymentModeManaged,
			OperatorCredential:              strings.Repeat("c", 32),
			OfficialIdentityIssuer:          "https://account.runforyou.app",
			OfficialIdentityWebClientID:     "web-client",
			OfficialIdentityWebClientSecret: "web-secret",
		}
		return config
	}
	config := valid()
	config.normalize()
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*Config){
		"部署地址非 HTTPS":       func(c *Config) { c.Server.PublicURL = "http://app.example.com" },
		"开放本地注册":            func(c *Config) { c.Deployment.RegistrationOpen = true },
		"运营凭据过短":            func(c *Config) { c.Deployment.OperatorCredential = strings.Repeat("c", 31) },
		"部署形态取值无效":          func(c *Config) { c.Deployment.Mode = "hosted" },
		"缺少身份 issuer":       func(c *Config) { c.Deployment.OfficialIdentityIssuer = "" },
		"身份 issuer 非 HTTPS": func(c *Config) { c.Deployment.OfficialIdentityIssuer = "http://account.runforyou.app" },
		"身份 issuer 带查询":     func(c *Config) { c.Deployment.OfficialIdentityIssuer = "https://account.runforyou.app?x=1" },
		"缺少 Web 客户端 ID":     func(c *Config) { c.Deployment.OfficialIdentityWebClientID = "" },
		"缺少 Web 客户端密钥":      func(c *Config) { c.Deployment.OfficialIdentityWebClientSecret = "" },
	} {
		config := valid()
		mutate(&config)
		config.normalize()
		if err := config.validate(); err == nil {
			t.Fatalf("%s 的配置通过了校验", name)
		}
	}
}

// TestDeploymentEnvironment 验证部署配置的环境变量覆盖与大小写规范化。
func TestDeploymentEnvironment(t *testing.T) {
	clearServerEnvironment(t)
	t.Setenv("PUBLIC_URL", "https://app.example.com/")
	t.Setenv("POSTGRES_HOST", "127.0.0.1")
	t.Setenv("POSTGRES_PORT", "5432")
	t.Setenv("POSTGRES_USER", "app")
	t.Setenv("POSTGRES_PASSWORD", "secret")
	t.Setenv("POSTGRES_DB", "app")
	t.Setenv("POSTGRES_SSLMODE", "disable")
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("NATS_NAMESPACE", "app")
	t.Setenv("DEPLOYMENT_MODE", "Managed")
	t.Setenv("OPERATOR_CREDENTIAL", strings.Repeat("c", 40))
	t.Setenv("OFFICIAL_IDENTITY_ISSUER", " https://account.runforyou.app ")
	t.Setenv("OFFICIAL_IDENTITY_WEB_CLIENT_ID", " web-client ")
	t.Setenv("OFFICIAL_IDENTITY_WEB_CLIENT_SECRET", " web-secret ")

	config, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if config.Deployment.Mode != domain.DeploymentModeManaged {
		t.Fatalf("部署形态未按环境变量覆盖: %q", config.Deployment.Mode)
	}
	if config.Deployment.OfficialIdentityIssuer != "https://account.runforyou.app" {
		t.Fatalf("身份 issuer 未按环境变量覆盖: %q", config.Deployment.OfficialIdentityIssuer)
	}
	if config.Deployment.OfficialIdentityWebClientID != "web-client" || config.Deployment.OfficialIdentityWebClientSecret != "web-secret" {
		t.Fatalf("Web 客户端凭据未按环境变量覆盖: %q %q", config.Deployment.OfficialIdentityWebClientID, config.Deployment.OfficialIdentityWebClientSecret)
	}
}

// TestSMTPEnvironmentAndValidation 验证 SMTP 环境变量覆盖默认值，以及开启发信时的字段校验。
func TestSMTPEnvironmentAndValidation(t *testing.T) {
	clearServerEnvironment(t)
	t.Setenv("PUBLIC_URL", "https://app.example.com/")
	t.Setenv("POSTGRES_HOST", "127.0.0.1")
	t.Setenv("POSTGRES_PORT", "5432")
	t.Setenv("POSTGRES_USER", "app")
	t.Setenv("POSTGRES_PASSWORD", "secret")
	t.Setenv("POSTGRES_DB", "app")
	t.Setenv("POSTGRES_SSLMODE", "disable")
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("NATS_NAMESPACE", "app")
	config, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if config.Email.SMTP.Enabled() || config.Email.SMTP.Port != 587 || config.Email.SMTP.Security != "starttls" {
		t.Fatalf("默认 SMTP 配置 = %#v", config.Email.SMTP)
	}
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "465")
	t.Setenv("SMTP_SECURITY", "TLS")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "secret")
	t.Setenv("SMTP_FROM_ADDRESS", "support@example.com")
	config, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	want := SMTPConfig{Host: "smtp.example.com", Port: 465, Username: "mailer", Password: "secret", Security: "tls", FromAddress: "support@example.com"}
	if config.Email.SMTP != want {
		t.Fatalf("SMTP 配置 = %#v, want %#v", config.Email.SMTP, want)
	}
	for name, invalid := range map[string]func(*SMTPConfig){
		"发件地址":  func(smtp *SMTPConfig) { smtp.FromAddress = "support" },
		"加密方式":  func(smtp *SMTPConfig) { smtp.Security = "ssl" },
		"端口":    func(smtp *SMTPConfig) { smtp.Port = 0 },
		"只有用户名": func(smtp *SMTPConfig) { smtp.Password = "" },
	} {
		smtp := want
		invalid(&smtp)
		if err := smtp.validate(); err == nil {
			t.Errorf("%s无效时校验通过", name)
		}
	}
}

// TestPublicURLValidation 验证部署地址必须是不带路径的完整 HTTP 地址，并去除末尾斜杠。
func TestPublicURLValidation(t *testing.T) {
	config := validTestConfig()
	config.Server.PublicURL = " https://app.example.com/ "
	config.normalize()
	if err := config.validate(); err != nil || config.Server.PublicURL != "https://app.example.com" {
		t.Fatalf("publicURL=%q err=%v", config.Server.PublicURL, err)
	}
	for _, value := range []string{"", "app.example.com", "ftp://app.example.com", "https://app.example.com/app", "https://app.example.com?x=1", "https://user@app.example.com"} {
		config := validTestConfig()
		config.Server.PublicURL = value
		config.normalize()
		if err := config.validate(); err == nil {
			t.Fatalf("部署地址 %q 通过了校验", value)
		}
	}
}

// TestRegistrationOpenEnvironment 验证自托管注册开关默认关闭，并可由环境变量开启。
func TestRegistrationOpenEnvironment(t *testing.T) {
	clearServerEnvironment(t)
	t.Setenv("PUBLIC_URL", "https://app.example.com")
	t.Setenv("POSTGRES_HOST", "127.0.0.1")
	t.Setenv("POSTGRES_PORT", "5432")
	t.Setenv("POSTGRES_USER", "app")
	t.Setenv("POSTGRES_PASSWORD", "secret")
	t.Setenv("POSTGRES_DB", "app")
	t.Setenv("POSTGRES_SSLMODE", "disable")
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("NATS_NAMESPACE", "app")
	config, err := Load("")
	if err != nil || config.Deployment.RegistrationOpen {
		t.Fatalf("默认注册开关 = %v, err = %v", config.Deployment.RegistrationOpen, err)
	}
	t.Setenv("REGISTRATION_OPEN", "true")
	config, err = Load("")
	if err != nil || !config.Deployment.RegistrationOpen {
		t.Fatalf("环境变量开启后注册开关 = %v, err = %v", config.Deployment.RegistrationOpen, err)
	}
}

// TestDeploymentNameEnvironment 验证部署名称可由环境变量设置，超长时拒绝启动。
func TestDeploymentNameEnvironment(t *testing.T) {
	clearServerEnvironment(t)
	t.Setenv("PUBLIC_URL", "https://app.example.com")
	t.Setenv("POSTGRES_HOST", "127.0.0.1")
	t.Setenv("POSTGRES_PORT", "5432")
	t.Setenv("POSTGRES_USER", "app")
	t.Setenv("POSTGRES_PASSWORD", "secret")
	t.Setenv("POSTGRES_DB", "app")
	t.Setenv("POSTGRES_SSLMODE", "disable")
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("NATS_NAMESPACE", "app")
	t.Setenv("DEPLOYMENT_NAME", "演示客服")
	config, err := Load("")
	if err != nil || config.Deployment.Name != "演示客服" {
		t.Fatalf("部署名称 = %q, err = %v", config.Deployment.Name, err)
	}
	t.Setenv("DEPLOYMENT_NAME", strings.Repeat("名", deploymentNameMaxLength+1))
	if _, err := Load(""); err == nil {
		t.Fatal("超长的部署名称应被拒绝")
	}
}

// TestBrandingOverride 验证品牌覆盖的环境变量与校验：产品名称覆盖所有语言，对象名和图标路径必须有效。
func TestBrandingOverride(t *testing.T) {
	clearServerEnvironment(t)
	t.Setenv("BRAND_NAME", " Acme Desk ")
	t.Setenv("BRAND_SDK_NAME", "AcmeDesk")
	t.Setenv("PUBLIC_URL", "https://app.example.com")
	t.Setenv("POSTGRES_HOST", "127.0.0.1")
	t.Setenv("POSTGRES_USER", "app")
	t.Setenv("POSTGRES_PASSWORD", "secret")
	t.Setenv("POSTGRES_DB", "app")
	t.Setenv("POSTGRES_PORT", "5432")
	t.Setenv("POSTGRES_SSLMODE", "disable")
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("NATS_NAMESPACE", "app")
	config, err := Load("")
	if err != nil {
		t.Fatalf("加载品牌覆盖配置失败: %v", err)
	}
	if config.Branding.Names["en-US"] != "Acme Desk" || config.Branding.Names["zh-CN"] != "Acme Desk" || config.Branding.SDKName != "AcmeDesk" {
		t.Fatalf("品牌覆盖 = %#v", config.Branding)
	}

	invalid := map[string]func(*Config){
		"对象名含连字符":  func(c *Config) { c.Branding.SDKName = "Acme-Desk" },
		"名称为空":     func(c *Config) { c.Branding.Names = map[string]string{"en-US": ""} },
		"语言标签无效":   func(c *Config) { c.Branding.Names = map[string]string{"not a tag": "Acme"} },
		"图标不是 PNG": func(c *Config) { c.Branding.IconPath = "/etc/icon.svg" },
		"图标不存在":    func(c *Config) { c.Branding.IconPath = filepath.Join(t.TempDir(), "missing.png") },
	}
	for name, mutate := range invalid {
		config := validTestConfig()
		mutate(&config)
		if err := config.validate(); err == nil {
			t.Errorf("%s: 应拒绝品牌覆盖", name)
		}
	}
}
