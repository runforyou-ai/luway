//go:build server

package integrationtest

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/runforyou-ai/luway/docs"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/productdocs"
	"github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"
)

// TestPublicProductDocs 验证文档组成经真实数据库后端与 HTTP 提供同一组双语页面。
func TestPublicProductDocs(t *testing.T) {
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	db := store.DB()
	site, err := productdocs.Load(docs.Content)
	require.NoError(t, err)
	backend := direct.New(db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, db), ProductDocs: site}, nil, nil, nil, servertest.NewTasks(), nil, nil, nil)
	member := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "文档", DisplayName: "文档管理员", Email: servertest.UniqueEmail("docs"), Password: "password123"})
	meta := appservice.RequestMeta{Token: member.Token, WorkspaceID: member.Identity.Workspace.ID}
	_, err = backend.LoadIdentity(context.Background(), meta)
	require.NoError(t, err)
	sources := []fs.FS{docs.Content}
	requireProductDocs(t, site, backend, meta, sources, "../../frontend/src/lib/product-docs.ts")
	// 重复来源与 index 别名冲突必须拒绝。
	_, err = productdocs.Load(docs.Content, docs.Content)
	require.ErrorContains(t, err, "duplicate docs page:")
	_, err = productdocs.Load(docs.Content, fstest.MapFS{"zh-cn/guide.md": {Data: []byte("---\ntitle: 重复\norder: 0\n---\n正文")}})
	require.ErrorContains(t, err, "duplicate docs page: zh-cn/guide")
	service, err := productdocs.NewService(site)
	require.NoError(t, err)
	for _, locale := range productdocs.Locales {
		for _, slug := range []string{"missing-page", "guide/missing-page"} {
			recorder := httptest.NewRecorder()
			http.StripPrefix("/docs", service).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, productdocs.PagePath(locale, slug), nil))
			require.Equal(t, http.StatusNotFound, recorder.Code, "%s/%s", locale, slug)
			_, err := backend.GetProductDocPage(context.Background(), meta, appservice.ProductDocPageInput{Locale: locale, Path: slug})
			var appErr *appservice.Error
			require.ErrorAs(t, err, &appErr, "%s/%s", locale, slug)
			require.Equal(t, http.StatusNotFound, appErr.HTTPStatus())
			_, found := site.Page(locale, slug)
			require.False(t, found)
		}
	}

}

// requireProductDocs 核对每个来源的双语路径、导航、帮助登记与页面的 HTTP 和 Backend 输出。
func requireProductDocs(t *testing.T, site *productdocs.Site, backend appservice.Backend, meta appservice.RequestMeta, sources []fs.FS, helpPath string) {
	t.Helper()
	service, err := productdocs.NewService(site)
	require.NoError(t, err)
	handler := http.StripPrefix("/docs", service)
	paths := map[string]map[string]bool{}
	for _, locale := range productdocs.Locales {
		paths[locale] = map[string]bool{}
		for _, source := range sources {
			require.NoError(t, fs.WalkDir(source, locale, func(name string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil || entry.IsDir() || path.Ext(name) != ".md" {
					return walkErr
				}
				slug := strings.TrimSuffix(strings.TrimPrefix(name, locale+"/"), ".md")
				if slug == "index" {
					slug = ""
				}
				slug = strings.TrimSuffix(slug, "/index")
				paths[locale][slug] = true
				page, found := site.Page(locale, slug)
				require.True(t, found, name)
				requireDocPage(t, site, backend, meta, handler, page)
				return nil
			}))
		}
		navigation := map[string]bool{"": true}
		for _, section := range site.Navigation(locale) {
			if section.Index != nil {
				navigation[section.Index.Slug] = true
			}
			for _, group := range section.Groups {
				for _, page := range group.Pages {
					navigation[page.Slug] = true
				}
			}
		}
		require.Equal(t, paths[locale], navigation, "navigation %s", locale)
	}
	require.Equal(t, paths["zh-cn"], paths["en"])
	// 登记对象中的每个帮助路径必须在两种语言中存在。
	help, err := os.ReadFile(helpPath)
	require.NoError(t, err)
	_, registry, found := strings.Cut(string(help), "export const productDocsPages = {")
	require.True(t, found)
	registry, _, found = strings.Cut(registry, "} as const")
	require.True(t, found)
	entries := regexp.MustCompile(`:\s*"([^"]*)"`).FindAllStringSubmatch(registry, -1)
	require.NotEmpty(t, entries)
	for _, entry := range entries {
		for _, locale := range productdocs.Locales {
			require.True(t, paths[locale][entry[1]], "%s/%s", locale, entry[1])
		}
	}
}

// requireDocPage 验证页面正文、搜索、语言切换和站内链接锚点。
func requireDocPage(t *testing.T, site *productdocs.Site, backend appservice.Backend, meta appservice.RequestMeta, handler http.Handler, page *productdocs.Page) {
	t.Helper()
	fragment, err := backend.GetProductDocPage(context.Background(), meta, appservice.ProductDocPageInput{Locale: page.Locale, Path: page.Slug})
	require.NoError(t, err, page.Path())
	require.Equal(t, page.Path(), fragment.Path)
	require.Equal(t, productdocs.WithProduct(page.Locale, page.Title), fragment.Title)
	require.NotContains(t, fragment.HTML, "{{product}}")
	require.NotContains(t, fragment.HTML, "{{slug}}")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, page.Path(), nil))
	require.Equal(t, http.StatusOK, response.Code, page.Path())
	require.Contains(t, response.Body.String(), fragment.HTML)
	for _, locale := range productdocs.Locales {
		require.Contains(t, response.Body.String(), `<option value="`+productdocs.PagePath(locale, page.Slug)+`"`)
	}
	results := site.Search(page.Locale, fragment.Title)
	found := false
	for _, result := range results {
		found = found || result.Page.Slug == page.Slug
	}
	require.True(t, found, "search %s", page.Path())
	search := httptest.NewRecorder()
	handler.ServeHTTP(search, httptest.NewRequest(http.MethodGet, productdocs.PagePath(page.Locale, "search")+"?q="+url.QueryEscape(fragment.Title), nil))
	require.Equal(t, http.StatusOK, search.Code)
	require.Contains(t, search.Body.String(), `class="docs-search-results"`)
	require.Contains(t, strings.SplitN(search.Body.String(), `class="docs-search-results"`, 2)[1], `href="`+page.Path())
	for _, href := range page.Links {
		link, err := url.Parse(href)
		require.NoError(t, err, "%s: %s", page.Path(), href)
		if link.IsAbs() || link.Host != "" {
			continue
		}
		target := page
		if link.Path != "" {
			require.True(t, strings.HasPrefix(link.Path, "/docs/"), "%s: %s", page.Path(), href)
			locale, slug, _ := strings.Cut(strings.TrimPrefix(link.Path, "/docs/"), "/")
			var exists bool
			target, exists = site.Page(locale, strings.TrimSuffix(slug, "/"))
			require.True(t, exists, "%s: %s", page.Path(), href)
		}
		if link.Fragment != "" {
			anchorExists := false
			for _, heading := range target.Headings {
				anchorExists = anchorExists || heading.ID == link.Fragment
			}
			require.True(t, anchorExists, "%s: %s", page.Path(), href)
		}
	}
}
