package control

import (
	"context"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/common/license"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

// Gauge 是一个整数运行指标在采集时刻的值。
type Gauge struct {
	Name        string
	Description string
	Value       int64
}

// ExportMetrics 经 OTLP/HTTP 向 control 上报一次运行指标，at 是采集时刻；control 按部署每分钟一次的上报判断服务器是否在线。
func (c *Client) ExportMetrics(ctx context.Context, at time.Time, gauges []Gauge) error {
	exporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(c.baseURL+"/otlp/v1/metrics"),
		otlpmetrichttp.WithHTTPClient(c.http),
	)
	if err != nil {
		return fmt.Errorf("create metrics exporter: %w", err)
	}
	defer exporter.Shutdown(context.WithoutCancel(ctx))
	metrics := make([]metricdata.Metrics, 0, len(gauges))
	for _, gauge := range gauges {
		metrics = append(metrics, metricdata.Metrics{
			Name: gauge.Name, Description: gauge.Description,
			Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{Time: at, Value: gauge.Value}}},
		})
	}
	if err := exporter.Export(ctx, &metricdata.ResourceMetrics{
		Resource: resource.NewSchemaless(
			attribute.String("service.name", license.ProductID),
			attribute.String("service.version", c.version),
		),
		ScopeMetrics: []metricdata.ScopeMetrics{{Scope: instrumentation.Scope{Name: license.ProductID}, Metrics: metrics}},
	}); err != nil {
		return fmt.Errorf("export metrics: %w", err)
	}
	return nil
}
