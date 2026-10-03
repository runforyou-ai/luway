//go:build server

package agent

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestNormalizeExecutionInputNormalizesValues 验证平台托管执行配置字段会被规范化。
func TestNormalizeExecutionInputNormalizesValues(t *testing.T) {
	input, err := normalizeExecutionInput(ExecutionInput{
		Mode: domain.AgentExecutionModeManaged,
		Managed: &ManagedExecutionInput{
			ModelID:           " 019C7F37-8C0B-7EF0-8ECA-CB672194D28D ",
			SystemInstruction: " 负责回答产品问题。 ",
			KnowledgeBaseIDs:  []string{" 019C7F37-8C0B-7EF0-8ECA-CB672194D28D ", "019c7f37-8c0b-7ef0-8eca-cb672194d28d"},
		},
	})
	if err != nil {
		t.Fatalf("normalizeExecutionInput() error = %v", err)
	}
	if !slices.Equal(input.Managed.KnowledgeBaseIDs, []string{"019c7f37-8c0b-7ef0-8eca-cb672194d28d"}) || input.Managed.ModelID != "019c7f37-8c0b-7ef0-8eca-cb672194d28d" || input.Managed.SystemInstruction != "负责回答产品问题。" {
		t.Fatalf("normalizeExecutionInput() = %#v", input)
	}
}

// TestNormalizeExecutionInputRejectsInvalidEnvelope 验证无效执行模式和缺失配置会被拒绝。
func TestNormalizeExecutionInputRejectsInvalidEnvelope(t *testing.T) {
	fields := executionValidationFields(t, ExecutionInput{})
	if fields["execution"] != ValidationExecutionInvalid {
		t.Fatalf("normalizeExecutionInput() fields = %#v", fields)
	}
}

// TestNormalizeExecutionInputRejectsRequiredFields 验证无效模型和知识库会被拒绝，企业指令允许为空。
func TestNormalizeExecutionInputRejectsRequiredFields(t *testing.T) {
	fields := executionValidationFields(t, ExecutionInput{
		Mode:    domain.AgentExecutionModeManaged,
		Managed: &ManagedExecutionInput{ModelID: "invalid", KnowledgeBaseIDs: []string{"invalid"}},
	})
	if fields["knowledgeBaseIds"] != ValidationKnowledgeBaseInvalid || fields["modelId"] != ValidationModelInvalid || fields["systemInstruction"] != "" {
		t.Fatalf("normalizeExecutionInput() fields = %#v", fields)
	}
}

// TestNormalizeExecutionInputRejectsLongInstruction 验证企业指令使用字符数限制。
func TestNormalizeExecutionInputRejectsLongInstruction(t *testing.T) {
	fields := executionValidationFields(t, ExecutionInput{
		Mode: domain.AgentExecutionModeManaged,
		Managed: &ManagedExecutionInput{
			ModelID:           "019c7f37-8c0b-7ef0-8eca-cb672194d28d",
			SystemInstruction: strings.Repeat("鹿", maxSystemInstructionLength+1),
		},
	})
	if fields["systemInstruction"] != ValidationSystemInstructionTooLong {
		t.Fatalf("normalizeExecutionInput() fields = %#v", fields)
	}
}

// TestDecodeRevisionExecutionReadsManagedV1 验证平台托管第一版配置快照可以完整解码。
func TestDecodeRevisionExecutionReadsManagedV1(t *testing.T) {
	execution, err := decodeRevisionExecution(servermodels.AgentRevision{
		ID:            "revision-1",
		ExecutionMode: string(domain.AgentExecutionModeManaged),
		ModelID:       "019c7f37-8c0b-7ef0-8eca-cb672194d28d",
		SchemaVersion: executionSchemaVersion,
		Configuration: []byte(`{"systemInstruction":"回答产品问题。","knowledgeBaseIds":["019c7f37-8c0b-7ef0-8eca-cb672194d28d"],"mcpServerIds":["019c7f37-8c0b-7ef0-8eca-cb672194d28d"]}`),
	})
	if err != nil {
		t.Fatalf("decodeRevisionExecution() error = %v", err)
	}
	if !slices.Equal(execution.MCPServerIDs, []string{"019c7f37-8c0b-7ef0-8eca-cb672194d28d"}) || execution.RevisionID != "revision-1" || execution.Mode != domain.AgentExecutionModeManaged || execution.Managed == nil || execution.Managed.Model.ID != "019c7f37-8c0b-7ef0-8eca-cb672194d28d" || execution.Managed.SystemInstruction != "回答产品问题。" || !slices.Equal(execution.Managed.KnowledgeBaseIDs, []string{"019c7f37-8c0b-7ef0-8eca-cb672194d28d"}) {
		t.Fatalf("decodeRevisionExecution() = %#v", execution)
	}
}

// TestDecodeRevisionExecutionRejectsUnknownMode 验证未知执行模式会明确失败。
func TestDecodeRevisionExecutionRejectsUnknownMode(t *testing.T) {
	_, err := decodeRevisionExecution(servermodels.AgentRevision{
		ExecutionMode: "connected",
		SchemaVersion: executionSchemaVersion,
		Configuration: []byte(`{}`),
	})
	if err == nil {
		t.Fatal("decodeRevisionExecution() error = nil")
	}
}

// TestDecodeRevisionExecutionRejectsUnknownSchema 验证未知配置结构版本会明确失败。
func TestDecodeRevisionExecutionRejectsUnknownSchema(t *testing.T) {
	_, err := decodeRevisionExecution(servermodels.AgentRevision{
		ExecutionMode: string(domain.AgentExecutionModeManaged),
		SchemaVersion: executionSchemaVersion + 1,
		Configuration: []byte(`{}`),
	})
	if err == nil {
		t.Fatal("decodeRevisionExecution() error = nil")
	}
}

// TestDecodeRevisionExecutionRejectsUnknownFields 验证配置快照包含未知字段时会明确失败。
func TestDecodeRevisionExecutionRejectsUnknownFields(t *testing.T) {
	_, err := decodeRevisionExecution(servermodels.AgentRevision{
		ExecutionMode: string(domain.AgentExecutionModeManaged),
		SchemaVersion: executionSchemaVersion,
		ModelID:       "019c7f37-8c0b-7ef0-8eca-cb672194d28d",
		Configuration: []byte(`{"systemInstruction":"回答产品问题。","unknown":true}`),
	})
	if err == nil {
		t.Fatal("decodeRevisionExecution() error = nil")
	}
}

// TestNormalizeExecutionInputRequiresManaged 验证执行方式只接受托管执行。
func TestNormalizeExecutionInputRequiresManaged(t *testing.T) {
	if fields := executionValidationFields(t, ExecutionInput{Mode: "local_agent"}); fields["execution"] != ValidationExecutionInvalid {
		t.Fatalf("normalizeExecutionInput() fields = %#v", fields)
	}
}

// executionValidationFields 返回执行配置字段校验结果。
func executionValidationFields(t *testing.T, input ExecutionInput) map[string]common.FieldCode {
	t.Helper()
	_, err := normalizeExecutionInput(input)
	var fieldError *common.FieldError
	if !errors.As(err, &fieldError) {
		t.Fatalf("normalizeExecutionInput() error = %v", err)
	}
	return fieldError.Fields
}
