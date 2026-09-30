package webfetch

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// helpPage 是带导航、侧栏和页脚的帮助中心页面样本。
const helpPage = `<!DOCTYPE html><html><head><title>退款政策</title></head><body>
<nav><a href="/">首页</a><a href="/pricing">定价</a><a href="/docs">文档</a></nav>
<aside><h3>相关文章</h3><ul><li><a href="/a">如何开票</a></li><li><a href="/b">如何改签</a></li></ul></aside>
<main><article>
<h1>退款政策</h1>
<p>企业客户在订阅生效后的七个自然日内可以申请全额退款，款项按原支付渠道退回，到账时间取决于支付机构的处理周期。</p>
<p>超过七个自然日后，按剩余未使用的服务月份折算退款金额，已使用月份不予退还，折算结果保留到分。</p>
<h2>退款时限</h2>
<table><thead><tr><th>支付方式</th><th>到账时间</th></tr></thead><tbody><tr><td>对公转账</td><td>五个工作日</td></tr><tr><td>信用卡</td><td>十个工作日</td></tr></tbody></table>
<h2>接口调用</h2>
<pre><code>POST /v1/refunds
{"orderId": "2048"}</code></pre>
</article></main>
<footer><p>版权所有 示例公司，保留一切权利。</p></footer>
</body></html>`

// TestExtractArticle 验证正文提取去掉页面结构并保留标题、表格与代码块。
func TestExtractArticle(t *testing.T) {
	address, _ := url.Parse("https://example.com/help/refund")
	body, title := extractArticle([]byte(helpPage), address)
	content := string(body)
	if title != "退款政策" {
		t.Fatalf("title=%q", title)
	}
	for _, keep := range []string{"七个自然日", "退款时限", "<table", "对公转账", "<pre", "/v1/refunds"} {
		if !strings.Contains(content, keep) {
			t.Fatalf("content missing %q: %s", keep, content)
		}
	}
	for _, drop := range []string{"定价", "如何开票", "版权所有"} {
		if strings.Contains(content, drop) {
			t.Fatalf("content keeps %q: %s", drop, content)
		}
	}
}

// TestExtractArticleWithoutContent 验证提取不到正文时返回整页，前端渲染的空壳页据此按空正文失败。
func TestExtractArticleWithoutContent(t *testing.T) {
	address, _ := url.Parse("https://example.com/")
	page := `<!DOCTYPE html><html><body><div id="app"></div><script src="/app.js"></script></body></html>`
	if content, _ := extractArticle([]byte(page), address); string(content) != page {
		t.Fatalf("content=%s", content)
	}
}

// TestDecodeUTF8 验证响应头、页面声明与 BOM 判定的编码都转成 UTF-8。
func TestDecodeUTF8(t *testing.T) {
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("<html><body><p>退款说明</p></body></html>"))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if decoded := string(decodeUTF8(gbk, "text/html; charset=gbk")); !strings.Contains(decoded, "退款说明") {
		t.Fatalf("header charset decoded=%q", decoded)
	}
	declared, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(`<html><head><meta charset="gbk"></head><body><p>退款说明</p></body></html>`))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if decoded := string(decodeUTF8(declared, "text/html")); !strings.Contains(decoded, "退款说明") {
		t.Fatalf("meta charset decoded=%q", decoded)
	}
	if decoded := string(decodeUTF8([]byte("退款说明"), "text/plain; charset=utf-8")); decoded != "退款说明" {
		t.Fatalf("utf8 decoded=%q", decoded)
	}
}
