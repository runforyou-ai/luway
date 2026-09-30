package domain

import (
	"path/filepath"
	"strings"
)

// KnowledgeDocumentSourceKind 表示知识文档的内容来源。
type KnowledgeDocumentSourceKind string

const (
	KnowledgeDocumentSourceFile KnowledgeDocumentSourceKind = "file"
	KnowledgeDocumentSourceText KnowledgeDocumentSourceKind = "text"
	KnowledgeDocumentSourceWeb  KnowledgeDocumentSourceKind = "web"
)

// KnowledgeDocumentTitleMaxLength 是在线文档与网页文档名称允许的最大字符数。
const KnowledgeDocumentTitleMaxLength = 120

// KnowledgeDocumentMarkdownContentType 是在线文档与网页文档正文的内容类型。
const KnowledgeDocumentMarkdownContentType = "text/markdown"

// KnowledgeDocumentFormat 表示知识文档支持的文件扩展名。
type KnowledgeDocumentFormat string

const (
	KnowledgeDocumentTXT      KnowledgeDocumentFormat = ".txt"
	KnowledgeDocumentMD       KnowledgeDocumentFormat = ".md"
	KnowledgeDocumentMarkdown KnowledgeDocumentFormat = ".markdown"
	KnowledgeDocumentHTML     KnowledgeDocumentFormat = ".html"
	KnowledgeDocumentHTM      KnowledgeDocumentFormat = ".htm"
	KnowledgeDocumentPDF      KnowledgeDocumentFormat = ".pdf"
	KnowledgeDocumentDOCX     KnowledgeDocumentFormat = ".docx"
	KnowledgeDocumentPPTX     KnowledgeDocumentFormat = ".pptx"
	KnowledgeDocumentXLSX     KnowledgeDocumentFormat = ".xlsx"
	KnowledgeDocumentCSV      KnowledgeDocumentFormat = ".csv"
	KnowledgeDocumentJSON     KnowledgeDocumentFormat = ".json"
)

// KnowledgeDocumentContentType 按扩展名返回知识文档的规范内容类型。
func KnowledgeDocumentContentType(name string) string {
	switch KnowledgeDocumentFormat(strings.ToLower(filepath.Ext(name))) {
	case KnowledgeDocumentTXT:
		return "text/plain"
	case KnowledgeDocumentMD, KnowledgeDocumentMarkdown:
		return "text/markdown"
	case KnowledgeDocumentHTML, KnowledgeDocumentHTM:
		return "text/html"
	case KnowledgeDocumentPDF:
		return "application/pdf"
	case KnowledgeDocumentDOCX:
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case KnowledgeDocumentPPTX:
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case KnowledgeDocumentXLSX:
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case KnowledgeDocumentCSV:
		return "text/csv"
	case KnowledgeDocumentJSON:
		return "application/json"
	default:
		return ""
	}
}
