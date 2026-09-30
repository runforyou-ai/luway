//go:build server

// Package documentconvert 在进程内把知识库原件转换为 Markdown 正文。
package documentconvert

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/runforyou-ai/cervi/internal/domain"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// pdfWorkers 与知识库任务并发数一致，每个 PDF 转换独占一个 PDFium 实例。
const pdfWorkers = 2

// Error 定义原件转换的语言无关失败原因码。
type Error struct {
	Code string `json:"code"`
}

// Error 返回语言无关的失败原因。
func (e *Error) Error() string { return "document convert: " + e.Code }

// Converter 按扩展名把原件转换为 Markdown 正文，PDF 由内嵌的 PDFium WebAssembly 解析。
type Converter struct {
	lock   sync.Mutex
	pdfium pdfium.Pool
}

// NewConverter 创建原件转换器，PDFium 实例池在首次转换 PDF 时加载。
func NewConverter() *Converter {
	return &Converter{}
}

// pdfPool 返回 PDFium 实例池，尚未加载或上次加载失败时重新加载。
func (c *Converter) pdfPool() (pdfium.Pool, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.pdfium == nil {
		pool, err := webassembly.Init(webassembly.Config{MaxTotal: pdfWorkers})
		if err != nil {
			return nil, err
		}
		c.pdfium = pool
	}
	return c.pdfium, nil
}

// Convert 读取原件并按扩展名返回 Markdown 正文，未知格式返回 unsupported_file，解析失败返回 parse_failed，PDFium 加载失败原样返回。
func (c *Converter) Convert(ctx context.Context, name string, source io.Reader) (string, error) {
	data, err := io.ReadAll(source)
	if err != nil {
		return "", err
	}
	var markdown string
	switch domain.KnowledgeDocumentFormat(strings.ToLower(filepath.Ext(name))) {
	case domain.KnowledgeDocumentTXT, domain.KnowledgeDocumentMD, domain.KnowledgeDocumentMarkdown, domain.KnowledgeDocumentJSON:
		markdown = decodeText(data)
	case domain.KnowledgeDocumentHTML, domain.KnowledgeDocumentHTM:
		markdown, err = convertHTML(ctx, data)
	case domain.KnowledgeDocumentCSV:
		markdown, err = convertCSV(data)
	case domain.KnowledgeDocumentXLSX:
		markdown, err = convertXLSX(data)
	case domain.KnowledgeDocumentDOCX:
		markdown, err = convertDOCX(data)
	case domain.KnowledgeDocumentPPTX:
		markdown, err = convertPPTX(data)
	case domain.KnowledgeDocumentPDF:
		pool, poolErr := c.pdfPool()
		if poolErr != nil {
			return "", poolErr
		}
		markdown, err = convertPDF(ctx, pool, data)
	default:
		return "", &Error{Code: "unsupported_file"}
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		slog.Warn("原件转换失败", "name", name, "error", err)
		return "", &Error{Code: "parse_failed"}
	}
	return strings.TrimSpace(markdown), nil
}

// decodeText 按 BOM 解码 UTF-8 与 UTF-16 文本，无 BOM 且不是合法 UTF-8 时按 GB18030 解码。
func decodeText(data []byte) string {
	var fallback encoding.Encoding = encoding.Nop
	if !utf8.Valid(data) {
		fallback = simplifiedchinese.GB18030
	}
	decoded, _, err := transform.Bytes(unicode.BOMOverride(fallback.NewDecoder()), data)
	if err != nil {
		return string(bytes.ToValidUTF8(data, []byte("�")))
	}
	return string(decoded)
}
