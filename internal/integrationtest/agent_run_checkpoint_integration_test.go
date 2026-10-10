//go:build server

package integrationtest

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/schema"
	openaischema "github.com/cloudwego/eino/schema/openai"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// checkpointItemID 模拟模型组件以具名字符串类型写入的条目编号。
type checkpointItemID string

// checkpointMetaExtension 模拟模型组件写入附加信息的输出元数据。
type checkpointMetaExtension struct {
	FinishReason string
}

// testAgentRunCheckpoint 验证挂起的运行以带版本号的 JSON 保存模型上下文，恢复后推理签名、推理原文分段与附加信息按类型回到模型输入，
// 以及恢复状态版本未知时运行以明确的错误失败。
func testAgentRunCheckpoint(t *testing.T, f *personalAgentFixture) {
	operations := computeraction.NewOperationsAction(f.db, newTestToolDecisions(f.db, f.tasks))
	// suspendRead 发起一次读取文件的运行，带推理内容块调用工具后因电脑未及时上报而挂起。
	suspendRead := func() servermodels.AgentRun {
		f.online()
		run := f.sendAndLoadRun(f.personalAgentChat(), "读一下会议纪要")
		f.model.use("read_file", `{"file_path":"notes.txt"}`)
		reasoning := schema.NewContentBlock(&schema.Reasoning{
			Text: "先读文件", Signature: "sig-1",
			OpenAIExtension: &openaischema.ReasoningExtension{Content: []*openaischema.ReasoningContent{{Text: "原文分段"}}},
		})
		reasoning.Extra = map[string]any{
			"thought_signature": []byte{1, 2, 3},
			"item_id":           checkpointItemID("rs_1"),
			"cached":            true,
			"meta":              &checkpointMetaExtension{FinishReason: "tool_calls"},
		}
		f.model.useReasoning(reasoning)
		f.run(run.ID)
		f.assertStatus(run.ID, domain.AgentRunStatusWaiting)
		return run
	}
	// complete 由执行器领取并上报读取结果，运行改回排队。
	complete := func(run servermodels.AgentRun) {
		claimed, err := operations.Claim(f.ctx, f.computerIdentity(), computeraction.ClaimInput{Limit: 4})
		require.NoError(t, err)
		require.Len(t, claimed.Operations, 1)
		require.NoError(t, operations.Complete(f.ctx, f.computerIdentity(), claimed.Operations[0].ID, domain.ComputerOutcome{Output: "     1\t会议纪要", Path: "/Users/test/notes.txt", Hash: "h1"}))
		f.assertStatus(run.ID, domain.AgentRunStatusQueued)
	}

	t.Run("挂起后按新格式恢复", func(t *testing.T) {
		run := suspendRead()
		var state []byte
		require.NoError(t, f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Column("state").Where("id = ?", run.ID).Scan(f.ctx, &state))
		var saved struct {
			Version  int `json:"version"`
			Messages []struct {
				Role   string `json:"role"`
				Blocks []struct {
					Type      string                     `json:"type"`
					Signature string                     `json:"signature"`
					Extra     map[string]json.RawMessage `json:"extra"`
				} `json:"blocks"`
			} `json:"messages"`
		}
		require.NoError(t, json.Unmarshal(state, &saved), "state=%s", state)
		require.Equal(t, 1, saved.Version)
		var savedReasoning bool
		for _, message := range saved.Messages {
			for _, block := range message.Blocks {
				if block.Type == "reasoning" {
					savedReasoning = true
					require.Equal(t, "sig-1", block.Signature)
					require.Contains(t, block.Extra, "thought_signature")
					require.NotContains(t, block.Extra, "meta", "output metadata saved")
				}
			}
		}
		require.True(t, savedReasoning, "state=%s", state)

		complete(run)
		f.run(run.ID)
		f.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		f.assertReply(run.ID, "结果：     1\t会议纪要")
		// 恢复后的模型输入带上挂起前的推理内容块、工具调用与送达的工具结果。
		var restored *schema.ContentBlock
		var callID string
		var resultCallID string
		for _, message := range f.model.lastInput() {
			for _, block := range message.ContentBlocks {
				switch block.Type {
				case schema.ContentBlockTypeReasoning:
					restored = block
				case schema.ContentBlockTypeFunctionToolCall:
					callID = block.FunctionToolCall.CallID
				case schema.ContentBlockTypeFunctionToolResult:
					resultCallID = block.FunctionToolResult.CallID
				}
			}
		}
		require.NotNil(t, restored, "restored reasoning")
		require.Equal(t, "先读文件", restored.Reasoning.Text)
		require.Equal(t, "sig-1", restored.Reasoning.Signature)
		require.NotNil(t, restored.Reasoning.OpenAIExtension)
		require.Len(t, restored.Reasoning.OpenAIExtension.Content, 1)
		require.Equal(t, "原文分段", restored.Reasoning.OpenAIExtension.Content[0].Text)
		require.Equal(t, map[string]any{"thought_signature": []byte{1, 2, 3}, "item_id": "rs_1", "cached": true}, restored.Extra)
		require.NotEmpty(t, callID)
		require.Equal(t, callID, resultCallID)
	})

	t.Run("恢复状态版本未知时运行失败", func(t *testing.T) {
		run := suspendRead()
		_, err := f.db.NewUpdate().Model((*servermodels.AgentRun)(nil)).Set("state = ?", []byte(`{"version":999,"messages":[]}`)).Where("id = ?", run.ID).Exec(f.ctx)
		require.NoError(t, err)
		complete(run)
		err = f.runner.Execute(f.ctx, agentrunaction.RunInput{RunID: run.ID})
		require.ErrorContains(t, err, "checkpoint version 999")
		f.assertStatus(run.ID, domain.AgentRunStatusFailed)
		var lastError *string
		require.NoError(t, f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Column("last_error").Where("id = ?", run.ID).Scan(f.ctx, &lastError))
		require.NotNil(t, lastError)
		require.Contains(t, *lastError, "checkpoint version 999")
	})
}
