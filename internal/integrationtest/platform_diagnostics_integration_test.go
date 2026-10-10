//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"

	"uuid"

	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/clusterbus"
	"github.com/stretchr/testify/require"
)

// TestPlatformDiagnostics 验证诊断信息只对平台管理员开放，包含部署配置、各服务端进程登记时的启动配置、数据库版本、以工作区编号标识的失败任务和平台供应商，且不含密码与密钥。
func TestPlatformDiagnostics(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	monitor := &fakeTaskMonitor{}
	backend := direct.New(db, direct.DeploymentConfig{Deployment: servertest.NewDeployment(t, db), InstanceID: "exporter", TaskMonitor: monitor}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	service := backend
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}
	installed, err := service.InstallWorkspace(ctx, meta, appservice.InstallWorkspaceInput{
		PublicURL: servertest.PublicURL, WorkspaceName: "诊断信息", DisplayName: "管理员", Email: servertest.UniqueEmail("diagnostics"),
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	admin := installed.Auth
	require.NoError(t, err)
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: domain.LocaleChineseSimplified}
	workspaces, err := backend.ListWorkspaces(ctx, adminMeta)
	require.NoError(t, err)
	require.Len(t, workspaces.Items, 1)
	owner := servertest.ResolveMemberSession(t, db, workspaces.Items[0].ID, admin.Token).Identity
	adminMeta.WorkspaceID = owner.Workspace.ID
	memberEmail := servertest.UniqueEmail("diagnostics-member")
	_, err = newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner, memberSpec{DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID})
	require.NoError(t, err)
	member := servertest.LoginMember(t, db, owner.Workspace.ID, memberEmail, "password123")
	memberMeta := appservice.RequestMeta{Token: member.Token, WorkspaceID: owner.Workspace.ID, Locale: domain.LocaleChineseSimplified}

	// 诊断信息只对平台管理员开放。
	_, err = backend.GetPlatformDiagnostics(ctx, memberMeta)
	servertest.RequireLocalizedError(t, err, i18n.ErrorPlatformAdminRequired)

	// 部署配置的对象存储与邮件发送带有密钥和密码。
	_, err = db.NewRaw(`UPDATE platforms SET s3_enabled = true, s3_endpoint = 'https://s3.example.test', s3_bucket = 'files',
		s3_access_key_id = 'access-key-id', s3_secret_access_key = 'secret-access-key'`).Exec(ctx)
	require.NoError(t, err)
	_, err = backend.UpdatePlatformEmail(ctx, adminMeta, appservice.PlatformEmailSettings{
		Host: "smtp.example.test", Port: 587, Username: "smtp-user", Password: "smtp-password", Security: "starttls", FromAddress: "support@example.test",
	})
	require.NoError(t, err)
	// 两台服务器登记各自的启动配置，配置中带有密码和地址凭据。
	config := serverconfig.Config{
		Server:   serverconfig.ServerConfig{Host: "0.0.0.0", Port: 8080},
		Database: serverconfig.DatabaseConfig{Host: "db", Port: 5432, User: "app", Password: "db-password", Name: "app", SSLMode: "disable"},
		NATS:     serverconfig.NATSConfig{URL: "nats://nats-user:nats-password@nats:4222", Namespace: "app"},
		Data:     serverconfig.DataConfig{Directory: "/var/lib/app"},
	}
	first, second := uuid.NewV7().String(), uuid.NewV7().String()
	for _, report := range []serverinstanceaction.InstanceReport{
		{ID: first, Hostname: "host-a", Version: "v1.0.0", BusDriver: clusterbus.DriverNATS, BusConnected: true, Config: config.Diagnostics("/opt/app/clients")},
		{ID: second, Hostname: "host-b", Version: "v1.0.0", BusDriver: clusterbus.DriverPostgres, BusConnected: true},
	} {
		require.NoError(t, serverinstanceaction.ReportInstance(ctx, db, report))
	}
	_, err = backend.CreateAIProvider(ctx, adminMeta, appservice.AIProviderInput{
		Brand: domain.AIProviderBrandOpenRouter, Name: "诊断来源", CredentialType: domain.AIProviderCredentialTypeAPIKey,
		APIKey: "provider-api-key", APIURL: "https://provider.example.test/v1",
		Models: []appservice.AIProviderModel{{Identifier: "chat", Name: "对话", Type: domain.AIModelTypeChat, InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 8192, MaxOutputTokens: 100}},
	})
	require.NoError(t, err)
	// 心跳刷新不改写登记时的配置。
	require.NoError(t, serverinstanceaction.ReportInstance(ctx, db, serverinstanceaction.InstanceReport{ID: first, Hostname: "host-a", Version: "v1.0.0"}))
	// 一个工作区任务失败，错误信息完整保留。
	failure := "upstream returned 502"
	monitor.failed = []servertask.FailedRun{{
		ID: uuid.NewV7().String(), WorkspaceID: &owner.Workspace.ID, ActionName: "knowledge.reindex", QueueName: servertask.QueueKnowledge,
		Attempt: 3, LastError: failure, FailedAt: time.Now(),
	}}

	diagnostics, err := backend.GetPlatformDiagnostics(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, "exporter", diagnostics.ExportedBy)
	require.False(t, diagnostics.GeneratedAt.IsZero(), "diagnostics generated at")
	require.Equal(t, 1, diagnostics.Overview.WorkspaceCount)
	require.Equal(t, 2, diagnostics.Overview.AccountCount)
	require.False(t, diagnostics.InstalledAt.IsZero(), "diagnostics installed at")
	require.Len(t, diagnostics.License.ServerID, 36)
	require.Equal(t, domain.LicenseStatusNone, diagnostics.License.Status)
	// 开启对象存储时由执行导出的服务器检查存储桶，无法访问时给出原因。
	require.True(t, diagnostics.ObjectStorage.Enabled)
	require.NotEmpty(t, diagnostics.ObjectStorage.Error)
	require.NotEmpty(t, diagnostics.Database.Version)
	require.NotZero(t, diagnostics.Database.Migration)
	require.Len(t, diagnostics.Runtime.Servers, 2)
	configs, drivers := map[string]appservice.PlatformServerConfig{}, map[string]string{}
	for _, server := range diagnostics.Runtime.Servers {
		configs[server.ID] = server.Config
		drivers[server.ID] = server.BusDriver
	}
	require.Equal(t, map[string]string{first: "nats", second: "postgres"}, drivers)
	firstConfig := configs[first]
	require.Equal(t, "nats://nats:4222", firstConfig.NATS.URL)
	require.Equal(t, "app", firstConfig.Database.User)
	require.Equal(t, filepath.Join("/var/lib/app", "files"), firstConfig.LocalDirectory)
	require.Equal(t, "0.0.0.0:8080", firstConfig.Listen)
	require.Empty(t, configs[second].Listen)
	deployment := diagnostics.Deployment
	require.Equal(t, servertest.PublicURL, deployment.PublicURL)
	require.True(t, deployment.Storage.Enabled)
	require.Equal(t, "files", deployment.Storage.Bucket)
	require.True(t, deployment.Email.Enabled)
	require.Equal(t, "smtp.example.test", deployment.Email.Host)
	require.Len(t, diagnostics.FailedTasks, 1)
	task := diagnostics.FailedTasks[0]
	require.Equal(t, &owner.Workspace.ID, task.WorkspaceID)
	require.Equal(t, failure, task.Error)
	require.Equal(t, "knowledge.reindex", task.Action)

	// 导出内容不含密码、密钥、地址凭据和工作区名称。
	encoded, err := json.Marshal(diagnostics)
	require.NoError(t, err)
	for _, secret := range []string{"db-password", "nats-user", "nats-password", "access-key-id", "secret-access-key", "smtp-user", "smtp-password", "provider-api-key"} {
		require.NotContains(t, string(encoded), secret, "diagnostics contains %q", secret)
	}
	require.NotContains(t, string(encoded), "诊断信息", "diagnostics contains the workspace name")
}
