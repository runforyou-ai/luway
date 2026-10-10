//go:build server

package direct

import (
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// knowledgeIndexPresentation 把来源索引阶段映射为展示状态，并本地化失败原因。
func knowledgeIndexPresentation(meta appservice.RequestMeta, stage domain.KnowledgeIndexStatus, failureCode string) (appservice.KnowledgeIndexStatus, string) {
	message := ""
	if failureCode != "" {
		key := i18n.ErrorKnowledgeProcessingFailed
		switch failureCode {
		case "file_read_failed":
			key = i18n.ErrorKnowledgeOriginalReadFailed
		case "empty_content":
			key = i18n.ErrorKnowledgeContentEmpty
		case "parse_failed", "unsupported_file":
			key = i18n.ErrorKnowledgeParseFailed
		case "file_too_large":
			key = i18n.FieldKnowledgeDocumentTooLarge
		case "content_too_large":
			key = i18n.ErrorKnowledgeContentTooLarge
		case "url_unreachable", "url_invalid":
			key = i18n.ErrorKnowledgePageUnreachable
		case "url_content_unsupported":
			key = i18n.ErrorKnowledgePageUnsupported
		case "url_content_too_large":
			key = i18n.ErrorKnowledgePageTooLarge
		case "embedding_model_unavailable":
			key = i18n.ErrorKnowledgeEmbeddingUnavailable
		case "embedding_failed":
			key = i18n.ErrorKnowledgeEmbeddingFailed
		case "embedding_dimension_mismatch":
			key = i18n.ErrorKnowledgeEmbeddingDimension
		}
		message, _ = i18n.Localize(string(meta.Locale), key)
	}
	status := appservice.KnowledgeIndexRunning
	switch stage {
	case domain.KnowledgeIndexInitial:
		status = appservice.KnowledgeIndexInitial
	case domain.KnowledgeIndexQueued:
		status = appservice.KnowledgeIndexQueued
	case domain.KnowledgeIndexSucceeded:
		status = appservice.KnowledgeIndexSucceeded
	case domain.KnowledgeIndexFailed:
		status = appservice.KnowledgeIndexFailed
	case domain.KnowledgeIndexCancelled:
		status = appservice.KnowledgeIndexCancelled
	}
	return status, message
}
