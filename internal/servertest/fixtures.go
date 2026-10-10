//go:build server

package servertest

import (
	"context"
	"crypto/ed25519"
	"strings"
	"testing"
	"time"
	"uuid"

	jwt "github.com/golang-jwt/jwt/v5"
	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	"github.com/runforyou-ai/luway/internal/common/license"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// OpenEmptyDatabase 在测试库所在实例上新建并迁移一个空数据库，测试结束后删除；用于依赖平台尚无账号的首次安装场景。
func OpenEmptyDatabase(t *testing.T, migrations serverstorage.Migrations) *bun.DB {
	t.Helper()
	ctx := context.Background()
	config := DatabaseConfig(t)
	base, err := serverstorage.Connect(ctx, config, migrations, "", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = base.Close() })
	name := config.Name + "_" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")[:16]
	_, err = base.DB().ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	config.Name = name
	store, err := serverstorage.Open(ctx, config, migrations)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close()
		_, _ = base.DB().ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return store.DB()
}

// NewDeployment 登记部署版本后返回已读取的部署状态；平台尚未首次安装时部署配置为空，首次安装后由安装操作刷新。
func NewDeployment(t testing.TB, db *bun.DB) *deploymentaction.DeploymentState {
	t.Helper()
	ctx := context.Background()
	_, err := db.NewInsert().Model(&servermodels.DeploymentVersion{Version: buildinfo.Version}).On("CONFLICT DO NOTHING").Exec(ctx)
	require.NoError(t, err)
	deployment := deploymentaction.NewDeploymentState(db)
	require.NoError(t, deployment.Reload(ctx))
	return deployment
}

// PublicURL 是集成测试使用的部署地址，服务端生成的对外链接以它为根地址。
const PublicURL = "https://app.example.test"

// LicenseKID 是测试签名公钥的编号。
const LicenseKID = "k-test"

// SignLicense 用测试私钥为服务器签发授权码。
func SignLicense(t *testing.T, key ed25519.PrivateKey, serverID string, issuedAt, expiresAt time.Time, capabilities map[string]any) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": license.Issuer, "aud": license.ProductID, "sub": serverID,
		"iat": issuedAt.Unix(), "exp": expiresAt.Unix(),
		"license_id": uuid.NewV7().String(), "customer": "测试客户", "capabilities": capabilities,
	})
	token.Header["typ"] = "license+jwt"
	token.Header["kid"] = LicenseKID
	code, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return code
}
