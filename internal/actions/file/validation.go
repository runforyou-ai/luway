//go:build server

// Package file 实现企业文件领域的应用操作。
package file

import (
	"mime"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
)

const (
	// maxImageByteSize 是图片文件的最大字节数。
	maxImageByteSize int64 = 5 * 1024 * 1024
	// maxKnowledgeDocumentByteSize 是知识文档原件的最大字节数。
	maxKnowledgeDocumentByteSize int64 = 20 * 1024 * 1024
	// maxFileNameLength 是文件名的最大字符数。
	maxFileNameLength = 255
)

// ValidationCode 标识文件字段校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationFileNameRequired   ValidationCode = "FILE_NAME_REQUIRED"
	ValidationContentTypeInvalid ValidationCode = "CONTENT_TYPE_INVALID"
	ValidationByteSizeInvalid    ValidationCode = "BYTE_SIZE_INVALID"
	ValidationDocumentTooLarge   ValidationCode = "DOCUMENT_TOO_LARGE"
	ValidationPurposeInvalid     ValidationCode = "PURPOSE_INVALID"
)

// ValidationError 表示文件字段校验失败。
type ValidationError = common.FieldError

var imageFileExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// UploadInput 定义待上传文件的客户端元数据。
type UploadInput struct {
	Purpose     domain.FilePurpose
	FileName    string
	ContentType string
	ByteSize    int64
}

// NormalizeUploadInput 规范化并校验待上传文件元数据。
func NormalizeUploadInput(input UploadInput) (UploadInput, map[string]ValidationCode) {
	switch input.Purpose {
	case domain.FilePurposeKnowledgeDocument:
		normalized, fields := normalizeFileInput(input, domain.FilePurposeKnowledgeDocument)
		normalized.ContentType = domain.KnowledgeDocumentContentType(normalized.FileName)
		if normalized.ContentType == "" {
			fields["contentType"] = ValidationContentTypeInvalid
		}
		return normalized, fields
	case domain.FilePurposeMessageAttachment:
		return normalizeFileInput(input, domain.FilePurposeMessageAttachment)
	case domain.FilePurposeUserAvatar:
		return normalizeFileInput(input, domain.FilePurposeUserAvatar)
	case domain.FilePurposeGroupImage:
		return normalizeFileInput(input, domain.FilePurposeGroupImage)
	case domain.FilePurposeAgentAvatar:
		return normalizeFileInput(input, domain.FilePurposeAgentAvatar)
	default:
		normalized, fields := normalizeFileInput(input, domain.FilePurposeUserAvatar)
		fields["purpose"] = ValidationPurposeInvalid
		return normalized, fields
	}
}

// normalizeFileInput 按指定的单一用途规范化文件元数据。
func normalizeFileInput(input UploadInput, purpose domain.FilePurpose) (UploadInput, map[string]ValidationCode) {
	input.FileName = strings.TrimSpace(filepath.Base(strings.ReplaceAll(input.FileName, "\\", "/")))
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(input.ContentType))
	if err == nil {
		input.ContentType = strings.ToLower(mediaType)
	} else {
		input.ContentType = "application/octet-stream"
	}
	fields := make(map[string]ValidationCode)
	if input.FileName == "" || input.FileName == "." || utf8.RuneCountInString(input.FileName) > maxFileNameLength {
		fields["fileName"] = ValidationFileNameRequired
	}
	if input.Purpose != purpose {
		fields["purpose"] = ValidationPurposeInvalid
	}
	if _, exists := imageFileExtensions[input.ContentType]; !exists && purpose != domain.FilePurposeMessageAttachment && purpose != domain.FilePurposeKnowledgeDocument {
		fields["contentType"] = ValidationContentTypeInvalid
	}
	switch {
	case input.ByteSize < 0:
		fields["byteSize"] = ValidationByteSizeInvalid
	case purpose == domain.FilePurposeKnowledgeDocument && input.ByteSize > maxKnowledgeDocumentByteSize:
		fields["byteSize"] = ValidationDocumentTooLarge
	case purpose != domain.FilePurposeMessageAttachment && purpose != domain.FilePurposeKnowledgeDocument &&
		(input.ByteSize == 0 || input.ByteSize > maxImageByteSize):
		fields["byteSize"] = ValidationByteSizeInvalid
	}
	return input, fields
}
