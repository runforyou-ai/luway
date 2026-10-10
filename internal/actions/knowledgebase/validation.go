//go:build server

package knowledgebase

import (
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/str"
)

const (
	ValidationEmbeddingModelInvalid common.FieldCode = "KNOWLEDGE_BASE_EMBEDDING_MODEL_INVALID"
	ValidationChunkLengthInvalid    common.FieldCode = "KNOWLEDGE_BASE_CHUNK_LENGTH_INVALID"
	ValidationChunkOverlapInvalid   common.FieldCode = "KNOWLEDGE_BASE_CHUNK_OVERLAP_INVALID"
	ValidationRerankModelInvalid    common.FieldCode = "KNOWLEDGE_BASE_RERANK_MODEL_INVALID"

	ValidationDocumentURLInvalid common.FieldCode = "KNOWLEDGE_DOCUMENT_URL_INVALID"

	ValidationQAContentInvalid common.FieldCode = "KNOWLEDGE_QA_CONTENT_INVALID"
	ValidationNameDuplicate    common.FieldCode = "KNOWLEDGE_BASE_NAME_DUPLICATE"
)

// normalizeInput 规范化知识库字段，并按类别校验分段长度与重叠。
func normalizeInput(input Input) (Input, map[string]common.FieldCode) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.EmbeddingModelID, _ = str.NormalizeUUID(input.EmbeddingModelID)
	input.RerankModelID, _ = str.NormalizeUUID(input.RerankModelID)
	fields := make(map[string]common.FieldCode)
	if input.Category == domain.KnowledgeBaseCategoryQA {
		input.ChunkLength, input.ChunkOverlap = nil, nil
	} else {
		if input.ChunkLength == nil || *input.ChunkLength < 256 || *input.ChunkLength > 2048 {
			fields["chunkLength"] = ValidationChunkLengthInvalid
		}
		if input.ChunkOverlap == nil || *input.ChunkOverlap < 0 || *input.ChunkOverlap > 200 {
			fields["chunkOverlap"] = ValidationChunkOverlapInvalid
		}
	}
	return input, fields
}
