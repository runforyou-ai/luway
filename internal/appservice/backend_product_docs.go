package appservice

import "context"

// ProductDocsBackend 定义应用内帮助读取产品文档的业务调用。
type ProductDocsBackend interface {
	// GetProductDocPage 返回当前平台可见的产品文档页面正文，供应用内帮助显示。
	//appservice:route GET /product-docs/page auth=public
	GetProductDocPage(context.Context, RequestMeta, ProductDocPageInput) (ProductDocPage, error)
}
