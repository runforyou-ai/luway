//go:build server

package modelcall

import (
	"context"
	"errors"
	"log/slog"

	"github.com/runforyou-ai/einorun/provider/apierr"
	"github.com/runforyou-ai/einorun/provider/embedding"
	"github.com/runforyou-ai/einorun/provider/vendor"
	"github.com/runforyou-ai/luway/internal/domain"
)

// EmbeddingError 定义向量生成的语言无关失败原因码。
type EmbeddingError struct {
	Code string
}

// Error 返回语言无关的失败原因。
func (e *EmbeddingError) Error() string { return "embedding: " + e.Code }

// RerankError 定义重排的语言无关失败原因码。
type RerankError struct {
	Code string
}

// Error 返回语言无关的失败原因。
func (e *RerankError) Error() string { return "rerank: " + e.Code }

// VendorBrand 返回供应商品牌在模型服务库中的品牌，两者取值一致。
func VendorBrand(brand domain.AIProviderBrand) vendor.Brand {
	return vendor.Brand(brand)
}

// CompatibleBaseURL 按品牌预设把供应商地址规范为 OpenAI 兼容入口。
func CompatibleBaseURL(brand domain.AIProviderBrand, value string) (string, error) {
	preset, known := vendor.Of(VendorBrand(brand))
	if !known {
		return "", errors.New("unknown model provider brand " + string(brand))
	}
	return preset.CompatibleURL(value)
}

// embeddingFailure 把向量接口的失败转为失败原因码：配置无法使用时为模型不可用，维度不符时为维度不匹配，其余为生成失败；调用方取消时原样返回。
func embeddingFailure(ctx context.Context, model string, err error) error {
	var classified *EmbeddingError
	switch {
	case errors.As(err, &classified), errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, embedding.ErrDimensionMismatch):
		return &EmbeddingError{Code: "embedding_dimension_mismatch"}
	}
	if failure, ok := apierr.As(err); ok && failure.Kind == apierr.InvalidConfig {
		return &EmbeddingError{Code: "embedding_model_unavailable"}
	}
	slog.WarnContext(ctx, "向量生成请求失败", "model", model, "error", err)
	return &EmbeddingError{Code: "embedding_failed"}
}

// rerankFailure 把重排接口的失败转为失败原因码：超时为重排超时，配置、凭据与连接失败为模型不可用，其余为重排失败；调用方取消时原样返回。
func rerankFailure(ctx context.Context, model string, err error) error {
	var classified *RerankError
	if errors.As(err, &classified) || errors.Is(err, context.Canceled) {
		return err
	}
	slog.WarnContext(ctx, "重排请求失败", "model", model, "error", err)
	failure, ok := apierr.As(err)
	if !ok {
		return &RerankError{Code: "rerank_failed"}
	}
	switch {
	case failure.Kind == apierr.Timeout:
		return &RerankError{Code: "rerank_timeout"}
	case failure.Status == 0 && failure.Stage == apierr.StageConnect,
		failure.Kind == apierr.Unauthorized, failure.Kind == apierr.Forbidden, failure.Kind == apierr.InvalidConfig:
		return &RerankError{Code: "rerank_model_unavailable"}
	}
	return &RerankError{Code: "rerank_failed"}
}
