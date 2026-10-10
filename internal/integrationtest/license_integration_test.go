//go:build server

package integrationtest

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"uuid"

	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/control"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// TestLicenseActivation 验证授权码验签与服务器标识校验、按签发时间替换、能力开放与到期后回到免费取值；测试设置进程级品牌覆盖来源，不与其他测试并行。
func TestLicenseActivation(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	// 当前品牌以本测试的部署状态为覆盖来源。
	deployment := servertest.NewDeployment(t, db)
	brand.UseOverride(deployment.BrandOverride)
	t.Cleanup(func() { brand.UseOverride(nil) })
	backend := direct.New(db, direct.DeploymentConfig{Deployment: deployment, LicenseKeys: license.Keys{servertest.LicenseKID: publicKey}},
		nil, nil, nil, testEnqueuer, nil, nil, nil)
	service := backend
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}

	installed, err := service.InstallWorkspace(ctx, meta, appservice.InstallWorkspaceInput{
		PublicURL: servertest.PublicURL, WorkspaceName: "授权测试", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	admin := installed.Auth
	require.NoError(t, err)
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: domain.LocaleChineseSimplified}
	current, err := service.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, domain.LicenseStatusNone, current.Status)
	require.Nil(t, current.ExpiresAt)
	require.Equal(t, 1, current.Capabilities.WorkspaceLimit)

	now := time.Now()
	unlimited := map[string]any{license.CapabilityWorkspaceLimit: 0, license.CapabilityCustomBranding: true}
	// 格式错误、他人签名、其他服务器与已过期的授权码都被拒绝。
	_, otherKey, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	rejected := []struct {
		code string
		key  i18n.Key
	}{
		{"not-a-license", i18n.ErrorLicenseInvalid},
		{servertest.SignLicense(t, otherKey, current.ServerID, now, now.AddDate(1, 0, 0), unlimited), i18n.ErrorLicenseInvalid},
		{servertest.SignLicense(t, privateKey, uuid.NewV7().String(), now, now.AddDate(1, 0, 0), unlimited), i18n.ErrorLicenseServerMismatch},
		{servertest.SignLicense(t, privateKey, current.ServerID, now.AddDate(-1, 0, 0), now.Add(-time.Minute), unlimited), i18n.ErrorLicenseExpired},
	}
	for _, item := range rejected {
		_, err := backend.ActivateLicense(ctx, adminMeta, appservice.ActivateLicenseInput{LicenseCode: item.code})
		servertest.RequireErrorMessage(t, err, item.key)
	}

	// 有效授权开放多工作区与自定义品牌，重复提交同一授权码原样返回。
	code := servertest.SignLicense(t, privateKey, current.ServerID, now.Add(-time.Hour), now.AddDate(1, 0, 0), unlimited)
	activated, err := service.ActivateLicense(ctx, adminMeta, appservice.ActivateLicenseInput{LicenseCode: "\n" + code + " "})
	require.NoError(t, err)
	require.Equal(t, domain.LicenseStatusActive, activated.Status)
	require.Equal(t, "测试客户", activated.Customer)
	require.NotNil(t, activated.ExpiresAt)
	require.Equal(t, 0, activated.Capabilities.WorkspaceLimit)
	require.True(t, activated.Capabilities.CustomBranding)
	require.True(t, brand.OverrideActive(), "授予自定义品牌后部署品牌配置应生效")
	again, err := backend.ActivateLicense(ctx, adminMeta, appservice.ActivateLicenseInput{LicenseCode: code})
	require.NoError(t, err)
	require.Equal(t, activated.LicenseID, again.LicenseID)
	// 创建和改名按去掉首尾空白后的名称检查部署内唯一性。
	_, err = backend.CreateWorkspace(ctx, adminMeta, appservice.WorkspaceInput{Name: " " + installed.Workspace.Name + " "})
	servertest.RequireFieldError(t, err, "name", i18n.FieldWorkspaceNameDuplicate)
	second, err := backend.CreateWorkspace(ctx, adminMeta, appservice.WorkspaceInput{Name: " Team "})
	require.NoError(t, err)
	require.Equal(t, "Team", second.Name)
	require.NotEqual(t, installed.Workspace.ID, second.ID)
	require.NotEqual(t, installed.Workspace.Slug, second.Slug)
	require.True(t, domain.WorkspaceSlugValid(second.Slug), "slug = %q", second.Slug)
	for _, name := range []string{"Team", "team", " TEAM "} {
		_, err = backend.CreateWorkspace(ctx, adminMeta, appservice.WorkspaceInput{Name: name})
		servertest.RequireFieldError(t, err, "name", i18n.FieldWorkspaceNameDuplicate)
	}
	secondMeta := adminMeta
	secondMeta.WorkspaceID = second.ID
	for _, name := range []string{"TEAM", "更名工作区"} {
		renamed, err := backend.UpdateWorkspace(ctx, secondMeta, appservice.WorkspaceSettingsInput{Name: name})
		require.NoError(t, err, "rename %q", name)
		require.Equal(t, name, renamed.Name)
		require.Equal(t, second.Slug, renamed.Slug)
	}
	_, err = backend.UpdateWorkspace(ctx, secondMeta, appservice.WorkspaceSettingsInput{Name: " " + installed.Workspace.Name + " "})
	servertest.RequireFieldError(t, err, "name", i18n.FieldWorkspaceNameDuplicate)
	firstMeta := adminMeta
	firstMeta.WorkspaceID = installed.Workspace.ID
	// 两个工作区并发改为同一个名称时，只有一个写入成功。
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, meta := range []appservice.RequestMeta{firstMeta, secondMeta} {
		go func() {
			<-start
			_, err := backend.UpdateWorkspace(ctx, meta, appservice.WorkspaceSettingsInput{Name: "并发重命名"})
			results <- err
		}()
	}
	close(start)
	succeeded := 0
	for range 2 {
		if err := <-results; err == nil {
			succeeded++
		} else {
			servertest.RequireFieldError(t, err, "name", i18n.FieldWorkspaceNameDuplicate)
		}
	}
	require.Equal(t, 1, succeeded, "concurrent renames succeeded")
	listed, err := backend.ListWorkspaces(ctx, adminMeta)
	require.NoError(t, err)
	require.Len(t, listed.Items, 2)
	require.NotEqual(t, listed.Items[0].Name, listed.Items[1].Name)
	require.NotEqual(t, listed.Items[0].Slug, listed.Items[1].Slug)

	// 签发时间不晚于当前授权的授权码被拒绝，更晚的授权码替换当前授权。
	older := servertest.SignLicense(t, privateKey, current.ServerID, now.Add(-2*time.Hour), now.AddDate(2, 0, 0), unlimited)
	_, err = backend.ActivateLicense(ctx, adminMeta, appservice.ActivateLicenseInput{LicenseCode: older})
	servertest.RequireErrorMessage(t, err, i18n.ErrorLicenseSuperseded)
	newer := servertest.SignLicense(t, privateKey, current.ServerID, now, now.AddDate(2, 0, 0), map[string]any{license.CapabilityWorkspaceLimit: 2})
	replaced, err := backend.ActivateLicense(ctx, adminMeta, appservice.ActivateLicenseInput{LicenseCode: newer})
	require.NoError(t, err)
	require.NotEqual(t, activated.LicenseID, replaced.LicenseID)
	require.Equal(t, 2, replaced.Capabilities.WorkspaceLimit)
	require.False(t, replaced.Capabilities.CustomBranding)
	require.False(t, brand.OverrideActive(), "新授权未授予自定义品牌时部署品牌配置不应生效")
	_, err = backend.CreateWorkspace(ctx, adminMeta, appservice.WorkspaceInput{Name: "第三工作区"})
	servertest.RequireErrorKind(t, err, appservice.ErrorKindConflict)

	// 授权到期后保留授权记录，平台按免费取值运行。
	_, err = db.ExecContext(ctx, "UPDATE licenses SET expires_at = now() - interval '1 minute'")
	require.NoError(t, err)
	expired, err := backend.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, domain.LicenseStatusExpired, expired.Status)
	require.Equal(t, replaced.LicenseID, expired.LicenseID)
	require.Equal(t, 1, expired.Capabilities.WorkspaceLimit)
}

// fakeControl 是记录请求并按预设返回授权码的 control 测试替身。
type fakeControl struct {
	mu          sync.Mutex
	serverID    string
	registered  int
	licenseCode string
	issue       func() string
	// metrics 是每次收到的运行指标上报，按指标名索引。
	metrics []map[string]int64
}

// metricReports 返回已收到的运行指标上报。
func (f *fakeControl) metricReports() []map[string]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.metrics)
}

// ServeHTTP 校验签名请求头与服务器身份后，按路径模拟登记、激活、拉取授权和接收运行指标。
func (f *fakeControl) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if request.Header.Get("Signature") == "" || request.Header.Get("Content-Digest") == "" ||
		!strings.Contains(request.Header.Get("Signature-Input"), `keyid="`+f.serverID+`"`) {
		writeProblem(writer, http.StatusUnauthorized, "invalid_signature")
		return
	}
	raw, _ := io.ReadAll(request.Body)
	var body struct {
		Product        string `json:"product"`
		PublicKey      string `json:"public_key"`
		ActivationCode string `json:"activation_code"`
	}
	_ = json.Unmarshal(raw, &body)
	switch request.URL.Path {
	case "/otlp/v1/metrics":
		var export collectormetrics.ExportMetricsServiceRequest
		if err := proto.Unmarshal(raw, &export); err != nil {
			writeProblem(writer, http.StatusBadRequest, "validation_failed")
			return
		}
		values := map[string]int64{}
		for _, resource := range export.ResourceMetrics {
			for _, scope := range resource.ScopeMetrics {
				for _, metric := range scope.Metrics {
					for _, point := range metric.GetGauge().GetDataPoints() {
						values[metric.Name] = point.GetAsInt()
					}
				}
			}
		}
		f.metrics = append(f.metrics, values)
		writer.Header().Set("Content-Type", "application/x-protobuf")
		writer.WriteHeader(http.StatusOK)
	case "/api/v1/servers":
		if body.Product != license.ProductID || body.PublicKey == "" {
			writeProblem(writer, http.StatusBadRequest, "validation_failed")
			return
		}
		f.registered++
		writer.WriteHeader(http.StatusNoContent)
	case "/api/v1/activations":
		switch body.ActivationCode {
		case "USED-USED-USED-USED":
			writeProblem(writer, http.StatusConflict, "server_mismatch")
		case "GOOD-GOOD-GOOD-GOOD":
			f.licenseCode = f.issue()
			_ = json.NewEncoder(writer).Encode(map[string]string{"license_code": f.licenseCode})
		default:
			writeProblem(writer, http.StatusNotFound, "activation_code_invalid")
		}
	case "/api/v1/license":
		if f.licenseCode == "" {
			writeProblem(writer, http.StatusNotFound, "not_found")
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"license_code": f.licenseCode})
	default:
		writeProblem(writer, http.StatusNotFound, "not_found")
	}
}

// writeProblem 写入 RFC 9457 错误响应。
func writeProblem(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"type": "about:blank", "title": code, "status": status, "code": code, "retryable": false})
}

// TestLicenseOnline 验证经 control 在线激活、后台同步续期、错误映射、上报开关与服务器标识重置。
func TestLicenseOnline(t *testing.T) {
	t.Parallel()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	keys := license.Keys{servertest.LicenseKID: publicKey}
	fake := &fakeControl{}
	server := httptest.NewServer(fake)
	defer server.Close()
	client := control.New(server.URL, "test", func(ctx context.Context) (control.Identity, error) {
		return licenseaction.ControlIdentity(ctx, db)
	})
	deployment := servertest.NewDeployment(t, db)
	backend := direct.New(db, direct.DeploymentConfig{Deployment: deployment, LicenseKeys: keys, Control: client},
		nil, nil, nil, testEnqueuer, nil, nil, nil)
	service := backend
	online := licenseaction.NewOnlineLicenseAction(db, keys, client, deployment)
	ctx := context.Background()

	// 未安装时后台同步直接跳过。
	require.NoError(t, online.SyncTask(ctx, licenseaction.SyncLicenseInput{}), "sync before install")
	// 未安装时上报开关为关闭。
	require.False(t, deployment.TelemetryEnabled())
	installed, err := service.InstallWorkspace(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}, appservice.InstallWorkspaceInput{
		PublicURL: servertest.PublicURL, WorkspaceName: "在线授权", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	admin := installed.Auth
	require.NoError(t, err)
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: domain.LocaleChineseSimplified}
	current, err := service.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.NotEmpty(t, current.ServerID)
	require.Equal(t, domain.LicenseStatusNone, current.Status)
	serverID := current.ServerID
	now := time.Now()
	fake.mu.Lock()
	fake.serverID = serverID
	fake.issue = func() string {
		return servertest.SignLicense(t, privateKey, serverID, now.Add(-time.Hour), now.AddDate(1, 0, 0), map[string]any{license.CapabilityWorkspaceLimit: 0})
	}
	fake.mu.Unlock()

	// control 中尚无授权时：手动同步提示先激活，后台同步保持现状。
	current, err = service.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.Nil(t, current.Sync.SyncedAt)
	require.Nil(t, current.Sync.FailedAt)
	_, err = backend.SyncLicense(ctx, adminMeta)
	servertest.RequireErrorMessage(t, err, i18n.ErrorLicenseNotIssued)
	require.NoError(t, online.SyncTask(ctx, licenseaction.SyncLicenseInput{}), "sync without license")
	// control 正常应答没有授权时记为同步成功。
	current, err = service.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.NotNil(t, current.Sync.SyncedAt)
	require.Nil(t, current.Sync.FailedAt)
	require.Empty(t, current.Sync.Error)
	syncedAt := *current.Sync.SyncedAt

	// 激活码无效或已用于其他服务器时返回对应错误，有效激活码激活授权。
	_, err = backend.ActivateLicenseOnline(ctx, adminMeta, appservice.ActivateLicenseOnlineInput{ActivationCode: "BAD"})
	servertest.RequireErrorMessage(t, err, i18n.ErrorActivationCodeInvalid)
	_, err = backend.ActivateLicenseOnline(ctx, adminMeta, appservice.ActivateLicenseOnlineInput{ActivationCode: "USED-USED-USED-USED"})
	servertest.RequireErrorMessage(t, err, i18n.ErrorActivationServerMismatch)
	activated, err := service.ActivateLicenseOnline(ctx, adminMeta, appservice.ActivateLicenseOnlineInput{ActivationCode: " GOOD-GOOD-GOOD-GOOD\n"})
	require.NoError(t, err)
	require.Equal(t, domain.LicenseStatusActive, activated.Status)
	require.Equal(t, serverID, activated.ServerID)
	require.Equal(t, 0, activated.Capabilities.WorkspaceLimit)
	current, err = service.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, domain.LicenseStatusActive, current.Status)
	require.Equal(t, serverID, current.ServerID)
	require.NotNil(t, current.ExpiresAt)
	require.True(t, current.ExpiresAt.Equal(*activated.ExpiresAt), "license expires at = %v, want %v", current.ExpiresAt, activated.ExpiresAt)

	// control 续期后后台同步替换为签发更晚的授权码；control 返回较早的授权码时保留本地授权。
	fake.mu.Lock()
	renewed := servertest.SignLicense(t, privateKey, serverID, now, now.AddDate(2, 0, 0), map[string]any{license.CapabilityWorkspaceLimit: 0})
	fake.licenseCode = renewed
	fake.mu.Unlock()
	require.NoError(t, online.SyncTask(ctx, licenseaction.SyncLicenseInput{}), "sync renewed")
	synced, err := service.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.NotNil(t, synced.ExpiresAt)
	require.True(t, synced.ExpiresAt.After(activated.ExpiresAt.AddDate(0, 6, 0)), "synced expires at = %v", synced.ExpiresAt)
	fake.mu.Lock()
	fake.licenseCode = servertest.SignLicense(t, privateKey, serverID, now.Add(-2*time.Hour), now.AddDate(3, 0, 0), map[string]any{})
	registered := fake.registered
	fake.mu.Unlock()
	kept, err := service.SyncLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.True(t, kept.ExpiresAt.Equal(*synced.ExpiresAt), "kept expires at = %v, want %v", kept.ExpiresAt, synced.ExpiresAt)
	require.Equal(t, serverID, kept.ServerID)
	fake.mu.Lock()
	require.Equal(t, registered+1, fake.registered)
	fake.mu.Unlock()

	// control 不再有授权或只返回已到期的授权码时，手动同步保留本地有效授权；查不到授权时记录起始时间，再次查到授权码时清空。
	for _, code := range []string{"", "", servertest.SignLicense(t, privateKey, serverID, now.AddDate(-2, 0, 0), now.Add(-time.Hour), map[string]any{})} {
		fake.mu.Lock()
		fake.licenseCode = code
		fake.mu.Unlock()
		local, err := service.SyncLicense(ctx, adminMeta)
		require.NoError(t, err, "sync for code %q", code)
		require.Equal(t, domain.LicenseStatusActive, local.Status, "code %q", code)
		require.True(t, local.ExpiresAt.Equal(*synced.ExpiresAt), "local expires at = %v for code %q", local.ExpiresAt, code)
		require.Equal(t, code == "", local.ControlMissingAt != nil, "control missing at = %v for code %q", local.ControlMissingAt, code)
	}

	// 后台同步查不到授权时保留首次查不到的时间。
	fake.mu.Lock()
	fake.licenseCode = ""
	fake.mu.Unlock()
	require.NoError(t, online.SyncTask(ctx, licenseaction.SyncLicenseInput{}), "sync missing")
	missing, err := service.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.NotNil(t, missing.ControlMissingAt)
	require.NoError(t, online.SyncTask(ctx, licenseaction.SyncLicenseInput{}), "sync missing again")
	stillMissing, err := service.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.NotNil(t, stillMissing.ControlMissingAt)
	require.True(t, stillMissing.ControlMissingAt.Equal(*missing.ControlMissingAt), "still missing at = %v, want %v", stillMissing.ControlMissingAt, missing.ControlMissingAt)

	// 离线替换授权码时清空查不到授权的记录。
	replaced, err := service.ActivateLicense(ctx, adminMeta, appservice.ActivateLicenseInput{
		LicenseCode: servertest.SignLicense(t, privateKey, serverID, now.Add(time.Minute), now.AddDate(2, 0, 0), map[string]any{license.CapabilityWorkspaceLimit: 0}),
	})
	require.NoError(t, err)
	require.Nil(t, replaced.ControlMissingAt)

	// 在线激活取得不晚于当前授权的授权码时同样清空查不到授权的记录。
	require.NoError(t, online.SyncTask(ctx, licenseaction.SyncLicenseInput{}), "sync missing before activation")
	reactivated, err := service.ActivateLicenseOnline(ctx, adminMeta, appservice.ActivateLicenseOnlineInput{ActivationCode: "GOOD-GOOD-GOOD-GOOD"})
	require.NoError(t, err)
	require.Nil(t, reactivated.ControlMissingAt)
	require.True(t, reactivated.ExpiresAt.Equal(*replaced.ExpiresAt), "reactivated expires at = %v, want %v", reactivated.ExpiresAt, replaced.ExpiresAt)

	// 本地授权已到期且 control 查不到授权时，手动同步返回本地授权并带上查不到授权的记录。
	_, err = db.NewUpdate().Table("licenses").Set("expires_at = now() - interval '1 hour'").Where("server_id = ?", serverID).Exec(ctx)
	require.NoError(t, err)
	fake.mu.Lock()
	fake.licenseCode = ""
	fake.mu.Unlock()
	expiredMissing, err := service.SyncLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, domain.LicenseStatusExpired, expiredMissing.Status)
	require.NotNil(t, expiredMissing.ControlMissingAt)

	// 首次安装后默认开启上报：运行指标上报一次整个部署的计数；关闭上报后不再上报运行指标，本实例的部署状态立即关闭错误上报，其他实例刷新后同样关闭。
	require.True(t, deployment.TelemetryEnabled())
	reportMetrics := licenseaction.NewReportMetricsAction(db, client)
	require.NoError(t, reportMetrics.Execute(ctx, struct{}{}))
	reports := fake.metricReports()
	require.Len(t, reports, 1)
	require.Len(t, reports[0], 5)
	require.Equal(t, int64(1), reports[0]["platform.accounts"])
	require.Equal(t, int64(1), reports[0]["platform.workspaces"])
	require.Equal(t, int64(1), reports[0]["platform.members"])
	other := servertest.NewDeployment(t, db)
	require.True(t, other.TelemetryEnabled())
	basics, err := service.GetPlatformDeployment(ctx, adminMeta)
	require.NoError(t, err)
	settings, err := service.UpdatePlatformDeploymentBasics(ctx, adminMeta, appservice.PlatformDeploymentBasicsInput{
		Name: basics.Name, TimeZone: basics.TimeZone, TelemetryEnabled: false,
	})
	require.NoError(t, err)
	require.False(t, settings.TelemetryEnabled)
	require.False(t, deployment.TelemetryEnabled())
	require.NoError(t, reportMetrics.Execute(ctx, struct{}{}), "metrics after disable")
	require.Len(t, fake.metricReports(), 1)
	require.True(t, other.TelemetryEnabled(), "other instance before refresh")
	require.NoError(t, other.Reload(ctx), "other refresh")
	require.False(t, other.TelemetryEnabled())

	// control 不可用时提示改用离线授权。
	server.Close()
	_, err = backend.SyncLicense(ctx, adminMeta)
	servertest.RequireErrorMessage(t, err, i18n.ErrorControlUnavailable)
	// 同步失败记录失败时间与原因，保留最近一次成功的时间。
	current, err = service.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.NotNil(t, current.Sync.FailedAt)
	require.NotEmpty(t, current.Sync.Error)
	require.NotNil(t, current.Sync.SyncedAt)
	require.False(t, current.Sync.SyncedAt.Before(syncedAt), "synced at = %v, want not before %v", current.Sync.SyncedAt, syncedAt)

	// 重置服务器标识生成新的服务器标识并删除本地授权。
	resetID, err := licenseaction.ResetServerID(ctx, db)
	require.NoError(t, err)
	require.NotEmpty(t, resetID)
	require.NotEqual(t, serverID, resetID)
	reset, err := service.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, resetID, reset.ServerID)
	require.Equal(t, domain.LicenseStatusNone, reset.Status)
	current, err = service.GetLicense(ctx, adminMeta)
	require.NoError(t, err)
	require.Nil(t, current.Sync.SyncedAt)
	require.Nil(t, current.Sync.FailedAt)
	require.Empty(t, current.Sync.Error)
}
