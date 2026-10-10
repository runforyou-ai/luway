//go:build server

// Package documentconvert 把知识库原件转换为 Markdown 正文，转换由 mdchunk 完成。
package documentconvert

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/mdchunk/convert"
	"github.com/runforyou-ai/mdchunk/convert/all"
	"github.com/runforyou-ai/mdchunk/convert/csv"
	"github.com/runforyou-ai/mdchunk/convert/pdf"
	"github.com/runforyou-ai/mdchunk/convert/pptx"
	"github.com/runforyou-ai/mdchunk/convert/text"
	"github.com/runforyou-ai/mdchunk/convert/xlsx"
	"github.com/runforyou-ai/support/set"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// 原件读取上限与知识文档上传上限一致，Office 原件解压与 HTML 表格展开的总量上限、输出正文上限均为 64 MiB，PDF 转换并发与知识库任务并发一致。
const (
	maxSourceBytes   = 20 << 20
	maxExpandedBytes = 64 << 20
	maxOutputBytes   = 64 << 20
	pdfWorkers       = 2
)

// supported 是知识库接受的原件格式，按 mdchunk 规范化后的格式登记。
var supported = set.CollectBy([]domain.KnowledgeDocumentFormat{
	domain.KnowledgeDocumentTXT, domain.KnowledgeDocumentMD, domain.KnowledgeDocumentMarkdown,
	domain.KnowledgeDocumentHTML, domain.KnowledgeDocumentHTM, domain.KnowledgeDocumentPDF,
	domain.KnowledgeDocumentDOCX, domain.KnowledgeDocumentPPTX, domain.KnowledgeDocumentXLSX,
	domain.KnowledgeDocumentCSV, domain.KnowledgeDocumentJSON,
}, func(format domain.KnowledgeDocumentFormat) convert.Format { return convert.ParseFormat(string(format)) })

// Error 定义原件转换的语言无关失败原因码。
type Error struct {
	Code string `json:"code"`
}

// Error 返回语言无关的失败原因。
func (e *Error) Error() string { return "document convert: " + e.Code }

// Converter 按扩展名把原件转换为 Markdown 正文：JSON 按纯文本输出，CSV 容忍不规范引号，隐藏工作表与隐藏幻灯片一并读取，无法识别编码的文本按 GB18030 解码。
type Converter struct {
	registry *all.Registry
}

// NewConverter 创建原件转换器，PDFium 实例池在首次转换 PDF 时加载。
func NewConverter() *Converter {
	limits := convert.Limits{MaxBytes: maxSourceBytes, MaxExpandedBytes: maxExpandedBytes, MaxOutputBytes: maxOutputBytes}
	registry := all.New(all.Options{
		Limits:   limits,
		Fallback: simplifiedchinese.GB18030,
		CSV:      csv.Options{LazyQuotes: true},
		PPTX:     pptx.Options{IncludeHidden: true},
		XLSX:     xlsx.Options{IncludeHidden: true},
		PDF:      pdf.Options{Workers: pdfWorkers},
	})
	// JSON 按纯文本输出，覆盖 all.New 默认的代码围栏注册。
	registry.Register(text.New(text.Options{Limits: limits, Fallback: simplifiedchinese.GB18030}), convert.JSON)
	return &Converter{registry: registry}
}

// Convert 读取原件并按扩展名返回 Markdown 正文，文本编码依次按 BOM、charset 与文档内声明确定，都没有时按 UTF-8 或 GB18030 解码。知识库不支持的格式与旧版二进制 Office 文档返回 unsupported_file，原件超过大小上限返回 file_too_large，解压、展开、输出、PDF 页数或单页字符超过上限返回 content_too_large，加密或损坏返回 parse_failed；取消与运行环境错误原样返回。
func (c *Converter) Convert(ctx context.Context, name, charset string, source io.Reader) (string, error) {
	format, ok := convert.FormatOf(name)
	if !ok || !supported.Has(format) {
		return "", &Error{Code: "unsupported_file"}
	}
	doc, err := c.registry.Convert(ctx, format, convert.Input{Reader: source, Name: name, Charset: charset})
	var limit *convert.LimitError
	switch {
	case err == nil:
		return strings.TrimSpace(doc.Markdown), nil
	case ctx.Err() != nil:
		return "", ctx.Err()
	case errors.As(err, &limit) && limit.Limit == convert.LimitSource:
		return "", &Error{Code: "file_too_large"}
	case errors.As(err, &limit):
		return "", &Error{Code: "content_too_large"}
	case errors.Is(err, convert.ErrUnsupported):
		return "", &Error{Code: "unsupported_file"}
	case errors.Is(err, convert.ErrCorrupt), errors.Is(err, convert.ErrEncrypted):
		slog.WarnContext(ctx, "原件转换失败", "name", name, "error", err)
		return "", &Error{Code: "parse_failed"}
	}
	return "", err
}
