//go:build server

package modelcall

import (
	"context"

	"github.com/runforyou-ai/einorun/provider/embedding"
	"github.com/runforyou-ai/einorun/provider/rerank"
	"github.com/runforyou-ai/einorun/provider/vendor"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	"github.com/runforyou-ai/support/arr"
)

// Embed 记录一次调用并按本次调用的来源尝试顺序为输入文本生成指定维度的向量。
func (i *Invoker) Embed(ctx context.Context, scope Scope, target *aimodel.Model, dimension int, inputs []string) ([][]float32, error) {
	var vectors [][]float32
	size := arr.SumBy(inputs, func(input string) int { return len(input) })
	estimate := Usage{InputTokens: estimateTokens(size, 0)}
	err := i.run(ctx, scope, target, estimate, func(ctx context.Context, route aimodel.Route) (Usage, error) {
		baseURL, err := CompatibleBaseURL(route.Brand, route.APIURL)
		if err != nil {
			return Usage{}, &EmbeddingError{Code: "embedding_model_unavailable"}
		}
		result, err := i.upstreams.Embedder.Embed(ctx, embedding.Endpoint{BaseURL: baseURL, APIKey: route.APIKey}, route.Identifier, dimension, inputs)
		if err != nil {
			return Usage{}, embeddingFailure(ctx, route.Identifier, err)
		}
		vectors = result.Vectors
		return Usage{InputTokens: int64(result.InputTokens)}, nil
	})
	return vectors, err
}

// Rerank 记录一次调用并按本次调用的来源尝试顺序为候选文本打分，返回候选下标与相关性得分。
func (i *Invoker) Rerank(ctx context.Context, scope Scope, target *aimodel.Model, query string, documents []string, topN int) ([]rerank.Score, error) {
	var scores []rerank.Score
	size := len(query) + arr.SumBy(documents, func(document string) int { return len(document) })
	estimate := Usage{InputTokens: estimateTokens(size, 0)}
	err := i.run(ctx, scope, target, estimate, func(ctx context.Context, route aimodel.Route) (Usage, error) {
		baseURL, err := CompatibleBaseURL(route.Brand, route.APIURL)
		if err != nil {
			return Usage{}, &RerankError{Code: "rerank_model_unavailable"}
		}
		// 重排接口格式取自供应商品牌的预设。
		preset, _ := vendor.Of(VendorBrand(route.Brand))
		result, err := i.upstreams.Reranker.Rerank(ctx, rerank.Endpoint{Protocol: preset.Rerank, BaseURL: baseURL, APIKey: route.APIKey}, route.Identifier, query, documents, topN)
		if err != nil {
			return Usage{}, rerankFailure(ctx, route.Identifier, err)
		}
		scores = result.Scores
		return Usage{InputTokens: int64(result.InputTokens)}, nil
	})
	return scores, err
}

// Decide 记录一次调用并按本次调用的来源尝试顺序把状态和一组判断题提交给判断模型，返回按题目键索引的结果；判断接口不返回 Token 用量。
func (i *Invoker) Decide(ctx context.Context, scope Scope, target *aimodel.Model, state any, questions map[string]decision.Question) (map[string]decision.Answer, error) {
	var answers map[string]decision.Answer
	err := i.run(ctx, scope, target, Usage{}, func(ctx context.Context, route aimodel.Route) (Usage, error) {
		var err error
		answers, err = i.upstreams.Decider.Decide(ctx, decision.Credential{BaseURL: route.APIURL, APIKey: route.APIKey}, route.Identifier, state, questions)
		return Usage{}, err
	})
	return answers, err
}
