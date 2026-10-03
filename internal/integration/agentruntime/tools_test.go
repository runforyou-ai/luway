package agentruntime

import (
	"context"
	"slices"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

// TestAssembleToolsByScene 验证 Runtime 基础工具只在内部场景注册，客服场景只保留业务工具和终止工具。
func TestAssembleToolsByScene(t *testing.T) {
	echoTool, err := newEchoTool()
	if err != nil {
		t.Fatal(err)
	}
	runtime := &EinoRuntime{tools: []tool.BaseTool{echoTool}}
	history := func(context.Context, string) (CustomerHistoryResult, error) { return CustomerHistoryResult{}, nil }
	for _, scenario := range []struct {
		scene Scene
		want  []string
	}{
		{SceneCustomer, []string{"search_customer_history", "ask_customer", "handoff_to_human", "resolve_conversation"}},
		{SceneAgentChat, []string{"echo", "search_customer_history"}},
		{SceneGroup, []string{"echo", "search_customer_history"}},
	} {
		t.Run(string(scenario.scene), func(t *testing.T) {
			var terminal *terminalTools
			if scenario.scene == SceneCustomer {
				terminal = newTerminalTools(nil)
			}
			tools, release, err := runtime.assembleTools(context.Background(), RunRequest{Assignment: Assignment{Scene: scenario.scene}, CustomerHistorySearch: history}, terminal, computerToolset{})
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			names := make([]string, 0, len(tools))
			for _, registered := range tools {
				info, err := registered.Info(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				names = append(names, info.Name)
			}
			if !slices.Equal(names, scenario.want) {
				t.Fatalf("工具 = %v，期望 %v", names, scenario.want)
			}
		})
	}
}
