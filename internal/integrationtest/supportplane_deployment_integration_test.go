//go:build server

package integrationtest

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	"github.com/runforyou-ai/luway/internal/common/diagnostic"
	licensecode "github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/common/locallog"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/supportplane"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSupportPlaneDeployment 验证首次登记只发送一次、在线激活保存授权、诊断包经签名上传。
func TestSupportPlaneDeployment(t *testing.T) {
	ctx := context.Background()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	servertest.EnsurePlatform(t, db)
	identity, err := licenseaction.ControlIdentity(ctx, db)
	require.NoError(t, err)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Now()
	code := servertest.SignLicense(t, private, identity.DeploymentID, now.Add(-time.Minute), now.Add(time.Hour))
	requests := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.URL.Path
		assert.NotEmpty(t, request.Header.Get("Signature"))
		assert.Contains(t, request.Header.Get("Signature-Input"), identity.DeploymentID)
		switch request.URL.Path {
		case "/api/support/v1/deployments":
			writer.WriteHeader(http.StatusNoContent)
		case "/api/support/v1/activations":
			assert.NoError(t, json.NewEncoder(writer).Encode(map[string]string{"license_code": code}))
		case "/api/support/v1/diagnostics":
			content, err := io.ReadAll(request.Body)
			assert.NoError(t, err)
			archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
			if assert.NoError(t, err) {
				assert.NotEmpty(t, archive.File)
			}
			assert.Empty(t, request.Header.Get("X-Support-Session"))
			writer.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected support plane request: %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := supportplane.New(server.URL, "test", func(context.Context) (supportplane.Identity, error) { return identity, nil })
	action := licenseaction.NewOnlineLicenseAction(db, licensecode.Keys{servertest.LicenseKID: public}, client, servertest.NewDeployment(t, db))
	require.NoError(t, action.Register(ctx, licenseaction.RegisterDeploymentInput{}))
	require.NoError(t, action.Register(ctx, licenseaction.RegisterDeploymentInput{}))
	current, err := action.Activate(ctx, nil, "ACTIVATION")
	require.NoError(t, err)
	require.Equal(t, domain.LicenseStatusActive, current.Status)
	require.True(t, current.Licensed())
	bundle, err := diagnostic.Build(t.TempDir(), map[string]string{"version": "test"})
	require.NoError(t, err)
	require.NoError(t, client.UploadDiagnostic(ctx, bundle))
	require.Equal(t, []string{"/api/support/v1/deployments", "/api/support/v1/activations", "/api/support/v1/diagnostics"}, []string{<-requests, <-requests, <-requests})
	require.Empty(t, requests)
}

// TestServerDiagnosticLogs 验证 JSON 日志保留请求编号并进入诊断包，过期日志被清理。
func TestServerDiagnosticLogs(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(directory, "logs"), 0700))
	expired := filepath.Join(directory, "logs", "server-"+time.Now().UTC().AddDate(0, 0, -7).Format(time.DateOnly)+"-old.jsonl")
	require.NoError(t, os.WriteFile(expired, []byte("expired"), 0600))
	previous := slog.Default()
	previousOutput := log.Writer()
	defer func() { slog.SetDefault(previous); log.SetOutput(previousOutput) }()
	logger, err := locallog.SetupServer(directory, "process", "host", "test", slog.LevelDebug)
	require.NoError(t, err)
	slog.InfoContext(logscope.WithTrace(context.Background(), "0123456789abcdef0123456789abcdef"), "请求完成", "duration_ms", 42)
	require.NoError(t, logger.Close())
	_, err = os.Stat(expired)
	require.True(t, os.IsNotExist(err))
	content, err := diagnostic.Build(directory, map[string]string{"version": "test"})
	require.NoError(t, err)
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	require.NoError(t, err)
	require.Len(t, archive.File, 2)
	reader, err := archive.File[1].Open()
	require.NoError(t, err)
	defer reader.Close()
	var record map[string]any
	require.NoError(t, json.NewDecoder(reader).Decode(&record))
	require.Equal(t, "0123456789abcdef0123456789abcdef", record["trace_id"])
	require.Equal(t, float64(42), record["duration_ms"])
}

// TestOfflineDeploymentLicense 验证离线授权的部署绑定、期限与替换顺序，并验证重置身份清空授权。
func TestOfflineDeploymentLicense(t *testing.T) {
	ctx := context.Background()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	servertest.EnsurePlatform(t, db)
	identity, err := licenseaction.ControlIdentity(ctx, db)
	require.NoError(t, err)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	action := licenseaction.NewActivateLicenseAction(db, licensecode.Keys{servertest.LicenseKID: public}, servertest.NewDeployment(t, db))
	now := time.Now()
	for _, test := range []struct {
		deployment      string
		issued, expires time.Time
		want            error
	}{
		{"00000000-0000-4000-8000-000000000001", now.Add(-time.Minute), now.Add(time.Hour), licenseaction.ErrLicenseDeploymentMismatch},
		{identity.DeploymentID, now.Add(-time.Hour), now.Add(-time.Minute), licenseaction.ErrLicenseExpired},
		{identity.DeploymentID, now.Add(-time.Minute), now.Add(time.Hour), nil},
		{identity.DeploymentID, now.Add(-time.Hour), now.Add(time.Hour), licenseaction.ErrLicenseSuperseded},
	} {
		code := servertest.SignLicense(t, private, test.deployment, test.issued, test.expires)
		_, err := action.Execute(ctx, nil, code)
		require.ErrorIs(t, err, test.want)
	}
	deploymentID, err := licenseaction.ResetDeploymentID(ctx, db)
	require.NoError(t, err)
	require.NotEqual(t, identity.DeploymentID, deploymentID)
	current, err := licenseaction.NewLicenseQuery(db).Execute(ctx)
	require.NoError(t, err)
	require.Equal(t, domain.LicenseStatusNone, current.Status)
}
