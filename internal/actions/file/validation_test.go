//go:build server

package file

import (
	"testing"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// TestNormalizeUploadInput 验证图片上传元数据规范化和限制。
func TestNormalizeUploadInput(t *testing.T) {
	normalized, fields := NormalizeUploadInput(UploadInput{
		Purpose: domain.FilePurposeUserAvatar, FileName: `C:\fakepath\avatar.png`, ContentType: "image/png; charset=binary", ByteSize: 1024,
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want empty", fields)
	}
	if normalized.FileName != "avatar.png" || normalized.ContentType != "image/png" {
		t.Fatalf("normalized input = %#v", normalized)
	}
	if key := storageKey("org", "file", normalized.FileName, normalized.ContentType); key != "organizations/org/files/file.png" {
		t.Fatalf("storage key = %q", key)
	}
	// 扩展名优先取原始文件名的单段扩展名，不合规时按内容类型补全，最后回落为 .bin。
	for _, item := range []struct{ name, contentType, want string }{
		{"memo.md", "text/markdown", "organizations/org/files/file.md"},
		{"Report.PDF", "application/pdf", "organizations/org/files/file.pdf"},
		{"archive.tar.gz", "application/gzip", "organizations/org/files/file.gz"},
		{"photo", "image/jpeg", "organizations/org/files/file.jpg"},
		{"note.a b", "image/webp", "organizations/org/files/file.webp"},
		{"README", "text/plain", "organizations/org/files/file.txt"},
		{"方案", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "organizations/org/files/file.docx"},
		{"报表", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "organizations/org/files/file.xlsx"},
		{"合同", "application/pdf", "organizations/org/files/file.pdf"},
		{"blob", "application/x-unknown", "organizations/org/files/file.bin"},
		{"data.verylongextensionname", "application/octet-stream", "organizations/org/files/file.bin"},
	} {
		if key := storageKey("org", "file", item.name, item.contentType); key != item.want {
			t.Fatalf("storage key for %q = %q, want %q", item.name, key, item.want)
		}
	}
	_, fields = NormalizeUploadInput(UploadInput{Purpose: domain.FilePurposeGroupImage, FileName: "group.webp", ContentType: "image/webp", ByteSize: 2048})
	if len(fields) != 0 {
		t.Fatalf("group image fields = %#v, want empty", fields)
	}
	_, fields = NormalizeUploadInput(UploadInput{Purpose: domain.FilePurposeAgentAvatar, FileName: "agent.png", ContentType: "image/png", ByteSize: 2048})
	if len(fields) != 0 {
		t.Fatalf("agent avatar fields = %#v, want empty", fields)
	}

	_, fields = NormalizeUploadInput(UploadInput{Purpose: domain.FilePurposeUserAvatar, FileName: "avatar.svg", ContentType: "image/svg+xml", ByteSize: maxImageByteSize + 1})
	if fields["contentType"] != ValidationContentTypeInvalid || fields["byteSize"] != ValidationByteSizeInvalid {
		t.Fatalf("invalid fields = %#v", fields)
	}
	_, fields = NormalizeUploadInput(UploadInput{Purpose: domain.FilePurposeContactAvatar, FileName: "avatar.jpg", ContentType: "image/jpeg", ByteSize: 3})
	if fields["purpose"] != ValidationPurposeInvalid {
		t.Fatalf("client contact avatar fields = %#v", fields)
	}
	_, fields = normalizeFileInput(UploadInput{Purpose: domain.FilePurposeContactAvatar, FileName: "avatar.jpg", ContentType: "image/jpeg", ByteSize: 3}, domain.FilePurposeContactAvatar)
	if len(fields) != 0 {
		t.Fatalf("imported contact avatar fields = %#v", fields)
	}
}

// TestKnowledgeDocumentUploadFormats 验证允许格式、内容类型归一化和文档大小上限。
func TestKnowledgeDocumentUploadFormats(t *testing.T) {
	for _, name := range []string{"a.txt", "a.md", "a.markdown", "a.htm", "a.html", "a.PDF", "a.docx", "a.pptx", "a.xlsx", "a.csv", "a.json"} {
		input, fields := NormalizeUploadInput(UploadInput{Purpose: domain.FilePurposeKnowledgeDocument, FileName: name, ContentType: "application/octet-stream", ByteSize: maxKnowledgeDocumentByteSize})
		if len(fields) != 0 || input.ContentType == "" || input.ContentType == "application/octet-stream" {
			t.Fatalf("%s: %+v %+v", name, input, fields)
		}
	}
	_, fields := NormalizeUploadInput(UploadInput{Purpose: domain.FilePurposeKnowledgeDocument, FileName: "a.pdf", ByteSize: maxImageByteSize + 1})
	if len(fields) != 0 {
		t.Fatalf("5MB+1 的文档 fields = %#v", fields)
	}
	_, fields = NormalizeUploadInput(UploadInput{Purpose: domain.FilePurposeKnowledgeDocument, FileName: "a.pdf", ByteSize: maxKnowledgeDocumentByteSize + 1})
	if fields["byteSize"] != ValidationDocumentTooLarge {
		t.Fatalf("超限文档 fields = %#v", fields)
	}
	_, fields = NormalizeUploadInput(UploadInput{Purpose: domain.FilePurposeMessageAttachment, FileName: "a.bin", ByteSize: maxKnowledgeDocumentByteSize + 1})
	if len(fields) != 0 {
		t.Fatalf("消息附件不限制大小，fields = %#v", fields)
	}
	for _, name := range []string{"a.doc", "a.xls", "a.ppt", "a.exe", "a.pdf.exe", "a"} {
		_, fields = NormalizeUploadInput(UploadInput{Purpose: domain.FilePurposeKnowledgeDocument, FileName: name, ByteSize: 12})
		if fields["contentType"] != ValidationContentTypeInvalid {
			t.Fatalf("accepted %s", name)
		}
	}
}
