//go:build server

package knowledgebase

import (
	"strings"
	"testing"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// TestNormalizeInput 验证知识库字段会被规范化并校验长度。
func TestNormalizeInput(t *testing.T) {
	length, overlap := 512, 50
	input, fields := normalizeInput(Input{Name: "  产品知识  ", Category: domain.KnowledgeBaseCategoryStandard, Description: "  产品资料  ", EmbeddingProviderID: "01991b28-5721-7000-8000-000000000001", EmbeddingModelIdentifier: "embedding", EmbeddingDimension: 1024, ChunkLength: &length, ChunkOverlap: &overlap, RetrievalCount: 3, RerankProviderID: "01991b28-5721-7000-8000-000000000001", RerankModelIdentifier: "rerank"})
	if len(fields) != 0 || input.Name != "产品知识" || input.Description != "产品资料" {
		t.Fatalf("input = %#v, fields = %#v", input, fields)
	}
	_, fields = normalizeInput(Input{
		Name:        strings.Repeat("知", domain.KnowledgeBaseNameMaxLength+1),
		Category:    domain.KnowledgeBaseCategoryStandard,
		Description: strings.Repeat("识", domain.KnowledgeBaseDescriptionMaxLength+1),
	})
	if fields["name"] != ValidationNameTooLong || fields["description"] != ValidationDescriptionTooLong {
		t.Fatalf("fields = %#v", fields)
	}
	_, fields = normalizeInput(Input{Name: "   ", Category: domain.KnowledgeBaseCategoryStandard})
	if fields["name"] != ValidationNameRequired {
		t.Fatalf("fields = %#v", fields)
	}
	_, fields = normalizeInput(Input{Name: "产品知识", Category: "external"})
	if fields["category"] != ValidationCategoryInvalid {
		t.Fatalf("fields = %#v", fields)
	}
}

// TestKnowledgeSettingsBounds 验证必填、边界值、小于下限及超过上限的数值配置。
func TestKnowledgeSettingsBounds(t *testing.T) {
	length, overlap := 512, 50
	valid := Input{Name: "知识库", Category: domain.KnowledgeBaseCategoryStandard, EmbeddingProviderID: "01991b28-5721-7000-8000-000000000001", EmbeddingModelIdentifier: "embedding", EmbeddingDimension: 1024, ChunkLength: &length, ChunkOverlap: &overlap, RetrievalCount: 3, RetrievalScoreThreshold: 0.7, RerankProviderID: "01991b28-5721-7000-8000-000000000001", RerankModelIdentifier: "rerank"}
	for _, test := range []struct {
		length, overlap, count int
		field                  string
	}{
		{256, 0, 1, ""}, {2048, 200, 20, ""}, {255, 50, 3, "chunkLength"}, {2049, 50, 3, "chunkLength"},
		{512, -1, 3, "chunkOverlap"}, {512, 201, 3, "chunkOverlap"}, {512, 50, 0, "retrievalCount"}, {512, 50, 21, "retrievalCount"},
	} {
		input := valid
		input.ChunkLength, input.ChunkOverlap, input.RetrievalCount = &test.length, &test.overlap, test.count
		_, fields := normalizeInput(input)
		if test.field == "" && len(fields) != 0 || test.field != "" && fields[test.field] == "" {
			t.Fatalf("test=%+v fields=%+v", test, fields)
		}
	}
	for _, test := range []struct {
		threshold float64
		valid     bool
	}{{0, true}, {1, true}, {-0.01, false}, {1.01, false}} {
		input := valid
		input.RetrievalScoreThreshold = test.threshold
		if _, fields := normalizeInput(input); (fields["retrievalScoreThreshold"] == "") != test.valid {
			t.Fatalf("threshold=%v fields=%+v", test.threshold, fields)
		}
	}
	input := valid
	input.ChunkLength, input.ChunkOverlap, input.EmbeddingDimension = nil, nil, 0
	_, fields := normalizeInput(input)
	if fields["chunkLength"] == "" || fields["chunkOverlap"] == "" || fields["embeddingDimension"] == "" {
		t.Fatalf("required fields=%+v", fields)
	}
	input = valid
	input.EmbeddingDimension = 1000
	if _, fields := normalizeInput(input); fields["embeddingDimension"] != ValidationEmbeddingDimensionInvalid {
		t.Fatalf("dimension fields=%+v", fields)
	}
	input = valid
	input.RerankModelIdentifier = ""
	_, fields = normalizeInput(input)
	if fields["rerankModelIdentifier"] != ValidationRerankModelInvalid {
		t.Fatalf("rerank fields=%+v", fields)
	}
	input = valid
	input.Category = domain.KnowledgeBaseCategoryQA
	normalized, fields := normalizeInput(input)
	if len(fields) != 0 || normalized.ChunkLength != nil || normalized.ChunkOverlap != nil {
		t.Fatalf("QA=%+v fields=%+v", normalized, fields)
	}
}
