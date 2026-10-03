//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/i18n"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"uuid"
)

// TestPlatformDiagnostics 验证诊断信息只对平台管理员开放，包含各服务端进程登记时的配置、数据库版本、以工作区编号标识的失败任务和平台供应商，且不含密码与密钥。
func TestPlatformDiagnostics(t *testing.T) {
	t.Parallel()
	db := openEmptyDatabase(t)
	backend := direct.New(db, direct.DeploymentConfig{PublicURL: testPublicURL, InstanceID: "exporter"}, nil, serverfilecontent.S3Config{}, nil, nil, newTestTasks(db), nil, nil, nil)
	service := appservice.New(backend)
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: appservice.LocaleChineseSimplified}
	admin, err := service.InstallWorkspace(ctx, meta, appservice.InstallWorkspaceInput{
		WorkspaceName: "诊断信息", WorkspaceSlug: "diagnostics", DisplayName: "管理员", Email: uniqueEmail("diagnostics"),
		Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: appservice.LocaleChineseSimplified}
	workspaces, err := backend.ListWorkspaces(ctx, adminMeta)
	if err != nil || len(workspaces.Items) != 1 {
		t.Fatalf("workspaces = %#v, err = %v", workspaces, err)
	}
	owner := resolveMemberSession(t, db, workspaces.Items[0].ID, admin.Token).Identity
	adminMeta.WorkspaceID = owner.Organization.ID
	memberEmail := uniqueEmail("diagnostics-member")
	if _, err := newTestMemberCreator(db, newTestTasks(db)).Execute(ctx, owner, memberSpec{DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID}); err != nil {
		t.Fatal(err)
	}
	member := loginMember(t, db, owner.Organization.ID, memberEmail, "password123")
	memberMeta := appservice.RequestMeta{Token: member.Token, WorkspaceID: owner.Organization.ID, Locale: appservice.LocaleChineseSimplified}

	// 诊断信息只对平台管理员开放。
	_, err = backend.GetPlatformDiagnostics(ctx, memberMeta)
	requireLocalizedError(t, err, i18n.ErrorPlatformAdminRequired)

	// 两台服务器登记各自的配置，配置中带有密码、密钥和地址凭据。
	config := serverconfig.Config{
		Server:   serverconfig.ServerConfig{PublicURL: "https://app.example.test", Host: "0.0.0.0", Port: 8080},
		Database: serverconfig.DatabaseConfig{Host: "db", Port: 5432, User: "app", Password: "db-password", Name: "app", SSLMode: "disable"},
		NATS:     serverconfig.NATSConfig{URL: "nats://nats-user:nats-password@nats:4222", Namespace: "app"},
		Storage: serverconfig.StorageConfig{S3: serverconfig.S3Config{
			Enabled: true, Endpoint: "https://s3.example.test", Bucket: "files", AccessKeyID: "access-key-id", SecretAccessKey: "secret-access-key",
		}},
		Email: serverconfig.EmailConfig{SMTP: serverconfig.SMTPConfig{Host: "smtp.example.test", Port: 587, Username: "smtp-user", Password: "smtp-password"}},
	}
	first, second := uuid.NewV7().String(), uuid.NewV7().String()
	for _, report := range []platformaction.InstanceReport{
		{ID: first, Hostname: "host-a", Version: "v1.0.0", TasksNATSConnected: true, RealtimeNATSConnected: true, Config: config.Diagnostics()},
		{ID: second, Hostname: "host-b", Version: "v1.0.0", TasksNATSConnected: true, RealtimeNATSConnected: true},
	} {
		if err := platformaction.ReportInstance(ctx, db, report); err != nil {
			t.Fatal(err)
		}
	}
	provider, err := backend.CreatePlatformAIProvider(ctx, adminMeta, appservice.PlatformAIProviderInput{
		Brand: appservice.AIProviderBrandOpenRouter, Name: "诊断来源", CredentialType: appservice.AIProviderCredentialTypeAPIKey,
		APIKey: "provider-api-key", APIURL: "https://provider.example.test/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 心跳刷新不改写登记时的配置。
	if err := platformaction.ReportInstance(ctx, db, platformaction.InstanceReport{ID: first, Hostname: "host-a", Version: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	// 一个工作区任务失败，错误信息完整保留。
	failedAt := time.Now()
	failure := "upstream returned 502"
	if _, err := db.NewInsert().Model(&servermodels.TaskRun{
		ID: uuid.NewV7().String(), OrganizationID: &owner.Organization.ID, ActionName: "knowledge.reindex", QueueName: "knowledge",
		Payload: json.RawMessage(`{}`), TriggerType: "business", Status: "failed", Attempt: 3, MaxAttempts: 3,
		AvailableAt: failedAt, LastError: &failure, FailedAt: &failedAt, CreatedAt: failedAt, UpdatedAt: failedAt,
	}).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	diagnostics, err := backend.GetPlatformDiagnostics(ctx, adminMeta)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.ExportedBy != "exporter" || diagnostics.GeneratedAt.IsZero() || diagnostics.Overview.WorkspaceCount != 1 || diagnostics.Overview.AccountCount != 2 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if diagnostics.Database.Version == "" || diagnostics.Database.Migration == 0 {
		t.Fatalf("database = %#v", diagnostics.Database)
	}
	if len(diagnostics.Runtime.Servers) != 2 {
		t.Fatalf("servers = %#v", diagnostics.Runtime.Servers)
	}
	configs := map[string]appservice.PlatformServerConfig{}
	for _, server := range diagnostics.Runtime.Servers {
		configs[server.ID] = server.Config
	}
	if got := configs[first]; got.PublicURL != "https://app.example.test" || got.NATS.URL != "nats://nats:4222" || !got.Storage.S3Enabled ||
		got.Storage.Bucket != "files" || got.Database.User != "app" || !got.SMTP.Enabled || got.Listen != "0.0.0.0:8080" {
		t.Fatalf("first server config = %#v", got)
	}
	if got := configs[second]; got.PublicURL != "" || got.Storage.S3Enabled {
		t.Fatalf("second server config = %#v", got)
	}
	if len(diagnostics.FailedTasks) != 1 {
		t.Fatalf("failed tasks = %#v", diagnostics.FailedTasks)
	}
	if task := diagnostics.FailedTasks[0]; task.WorkspaceID == nil || *task.WorkspaceID != owner.Organization.ID || task.Error != failure || task.Action != "knowledge.reindex" {
		t.Fatalf("failed task = %#v", task)
	}

	if len(diagnostics.AIProviders) != 1 || diagnostics.AIProviders[0].ID != provider.ID || diagnostics.AIProviders[0].Name != "诊断来源" {
		t.Fatalf("AI providers = %#v", diagnostics.AIProviders)
	}

	// 导出内容不含密码、密钥、地址凭据和工作区名称。
	encoded, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"db-password", "nats-user", "nats-password", "access-key-id", "secret-access-key", "smtp-user", "smtp-password", "provider-api-key"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("diagnostics contains %q", secret)
		}
	}
	if strings.Contains(string(encoded), "诊断信息") {
		t.Fatal("diagnostics contains the workspace name")
	}
}
