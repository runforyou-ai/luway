package agentruntime

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	"github.com/runforyou-ai/luway/pkg/webfetch"
)

// TestResolveAssignmentWebTools 验证联网搜索与网页读取只在内部场景进入工具清单与指令，客服场景不提供。
func TestResolveAssignmentWebTools(t *testing.T) {
	capabilities := Capabilities{Knowledge: true, WebSearch: true, WebFetch: true}
	internal := ResolveAssignment(AssignmentFacts{Scene: SceneContext{Scene: SceneAgentChat}}, capabilities)
	if strings.Join(internal.Tools, ",") != "search_knowledge,web_search,web_fetch,TaskCreate,TaskGet,TaskUpdate,TaskList,agent" {
		t.Fatalf("内部场景工具清单 = %v", internal.Tools)
	}
	if !strings.Contains(internal.Instruction, "- web_search：") || !strings.Contains(internal.Instruction, "- web_fetch：") ||
		!strings.Contains(internal.Instruction, webSourceGuidance) {
		t.Fatalf("内部场景指令 = %q", internal.Instruction)
	}
	customer := ResolveAssignment(customerFacts(), capabilities)
	if slices.Contains(customer.Tools, WebSearchToolName) || slices.Contains(customer.Tools, WebFetchToolName) ||
		strings.Contains(customer.Instruction, "web_search") || strings.Contains(customer.Instruction, webSourceGuidance) {
		t.Fatalf("客服场景有效配置 = %+v", customer)
	}
	// 企业未启用联网搜索时只提供网页读取。
	fetchOnly := ResolveAssignment(AssignmentFacts{Scene: SceneContext{Scene: SceneGroup}}, Capabilities{WebFetch: true})
	if strings.Join(fetchOnly.Tools, ",") != "web_fetch,TaskCreate,TaskGet,TaskUpdate,TaskList,agent" || strings.Contains(fetchOnly.Instruction, "- web_search：") {
		t.Fatalf("仅网页读取的有效配置 = %+v", fetchOnly)
	}
}

// TestWebTools 验证联网搜索与网页读取工具把模型参数交给执行侧并返回 JSON 结果。
func TestWebTools(t *testing.T) {
	var searched websearch.Request
	search := func(_ context.Context, request websearch.Request) (websearch.Result, error) {
		searched = request
		return websearch.Result{Items: []websearch.Item{{Title: "退款政策", URL: "https://example.com/refund"}}}, nil
	}
	fetch := func(_ context.Context, address string) (webfetch.Document, error) {
		return webfetch.Document{URL: address, Content: "# 退款政策"}, nil
	}
	runtime := &EinoRuntime{}
	tools, release, err := runtime.assembleTools(context.Background(), RunRequest{
		Assignment: Assignment{Scene: SceneAgentChat}, WebSearch: search, WebFetch: fetch,
	}, nil, computerToolset{})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	outputs := make(map[string]string)
	for _, registered := range tools {
		info, err := registered.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		arguments := map[string]string{WebSearchToolName: `{"query":"退款","count":3,"recency":"week"}`, WebFetchToolName: `{"url":"https://example.com/refund"}`}[info.Name]
		output, err := registered.(tool.InvokableTool).InvokableRun(context.Background(), arguments)
		if err != nil {
			t.Fatalf("%s: %v", info.Name, err)
		}
		outputs[info.Name] = output
	}
	if searched != (websearch.Request{Query: "退款", Count: 3, Recency: websearch.RecencyWeek}) {
		t.Fatalf("搜索参数 = %+v", searched)
	}
	var result websearch.Result
	if err := json.Unmarshal([]byte(outputs[WebSearchToolName]), &result); err != nil || len(result.Items) != 1 {
		t.Fatalf("搜索结果 = %s", outputs[WebSearchToolName])
	}
	if !strings.Contains(outputs[WebFetchToolName], "# 退款政策") {
		t.Fatalf("网页读取结果 = %s", outputs[WebFetchToolName])
	}
}
