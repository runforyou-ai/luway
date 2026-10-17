//go:build server

package integrationtest

import (
	"context"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/stretchr/testify/require"
)

// platformSiteInputFrom 把当前站点设置转为保存输入，供测试在修改个别字段后整页保存。
func platformSiteInputFrom(site appservice.PlatformSite) appservice.PlatformSiteInput {
	return appservice.PlatformSiteInput{
		Name:                   site.Name,
		TimeZone:               site.TimeZone,
		RegistrationPolicy:     site.RegistrationPolicy,
		WorkspaceCreationQuota: site.WorkspaceCreationQuota,
		PublicURL:              site.PublicURL,
		Certificate:            site.Certificate,
		Branding:               site.Branding,
	}
}

// loadPlatformSiteInput 读取当前站点设置并转为保存输入。
func loadPlatformSiteInput(t *testing.T, service interface {
	GetPlatformSite(context.Context, appservice.RequestMeta) (appservice.PlatformSite, error)
}, ctx context.Context, meta appservice.RequestMeta) appservice.PlatformSiteInput {
	t.Helper()
	site, err := service.GetPlatformSite(ctx, meta)
	require.NoError(t, err)
	return platformSiteInputFrom(site)
}
