//go:build server

package modelcall

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
	"github.com/runforyou-ai/luway/pkg/embedding"
	"github.com/runforyou-ai/luway/pkg/rerank"
)

// Embed 记录一次调用并按来源顺序为输入文本生成指定维度的向量。
func (i *Invoker) Embed(ctx context.Context, scope Scope, target *aimodel.Model, dimension int, inputs []string) ([][]float32, error) {
	var vectors [][]float32
	var size int
	for _, input := range inputs {
		size += len(input)
	}
	estimate := Usage{InputTokens: estimateTokens(size, 0)}
	err := i.run(ctx, scope, target, estimate, func(ctx context.Context, route aimodel.Route) (Usage, error) {
		baseURL, err := modelprovider.CompatibleBaseURL(string(route.Brand), route.APIURL)
		if err != nil {
			return Usage{}, &embedding.Error{Code: "embedding_model_unavailable"}
		}
		result, err := i.upstreams.Embedder.Embed(ctx, embedding.Credential{BaseURL: baseURL, APIKey: route.APIKey}, route.Identifier, dimension, inputs)
		if err != nil {
			return Usage{}, err
		}
		vectors = result.Vectors
		return Usage{InputTokens: int64(result.InputTokens)}, nil
	})
	return vectors, err
}

// Rerank 记录一次调用并按来源顺序为候选文本打分，返回候选下标与相关性得分。
func (i *Invoker) Rerank(ctx context.Context, scope Scope, target *aimodel.Model, query string, documents []string, topN int) ([]rerank.Score, error) {
	var scores []rerank.Score
	size := len(query)
	for _, document := range documents {
		size += len(document)
	}
	estimate := Usage{InputTokens: estimateTokens(size, 0)}
	err := i.run(ctx, scope, target, estimate, func(ctx context.Context, route aimodel.Route) (Usage, error) {
		baseURL, err := modelprovider.CompatibleBaseURL(string(route.Brand), route.APIURL)
		if err != nil {
			return Usage{}, &rerank.Error{Code: "rerank_model_unavailable"}
		}
		// 阿里云百炼使用 DashScope 原生重排接口，其余供应商使用兼容接口。
		protocol := rerank.ProtocolCompatible
		if route.Brand == domain.AIProviderBrandAlibaba {
			protocol = rerank.ProtocolDashScope
		}
		result, err := i.upstreams.Reranker.Rerank(ctx, rerank.Credential{Protocol: protocol, BaseURL: baseURL, APIKey: route.APIKey}, route.Identifier, query, documents, topN)
		if err != nil {
			return Usage{}, err
		}
		scores = result.Scores
		return Usage{InputTokens: int64(result.InputTokens)}, nil
	})
	return scores, err
}

// Decide 记录一次调用并按来源顺序把状态和一组判断题提交给判断模型，返回按题目键索引的结果；判断接口不返回 Token 用量，平台模型只按次计费。
func (i *Invoker) Decide(ctx context.Context, scope Scope, target *aimodel.Model, state any, questions map[string]decision.Question) (map[string]decision.Answer, error) {
	var answers map[string]decision.Answer
	err := i.run(ctx, scope, target, Usage{}, func(ctx context.Context, route aimodel.Route) (Usage, error) {
		var err error
		answers, err = i.upstreams.Decider.Decide(ctx, decision.Credential{BaseURL: route.APIURL, APIKey: route.APIKey}, route.Identifier, state, questions)
		return Usage{}, err
	})
	return answers, err
}
