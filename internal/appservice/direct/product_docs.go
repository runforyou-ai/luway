//go:build server

package direct

import (
	"context"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/productdocs"
)

// productDocsOps 持有当前平台的产品文档。
type productDocsOps struct {
	site *productdocs.Site
}

// GetProductDocPage 返回当前平台可见的文档页面正文，产品名称已按部署品牌替换。
func (o *directOperations) GetProductDocPage(_ context.Context, meta appservice.RequestMeta, input appservice.ProductDocPageInput) (appservice.ProductDocPage, error) {
	fragment, ok := o.productDocsOps.site.Fragment(input.Locale, input.Path)
	if !ok {
		return appservice.ProductDocPage{}, appservice.NotFoundError(meta, i18n.ErrorProductDocNotFound)
	}
	return appservice.ProductDocPage{Title: fragment.Title, HTML: fragment.HTML, Path: fragment.Path}, nil
}
