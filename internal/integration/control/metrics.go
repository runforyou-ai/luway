package control

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/runforyou-ai/luway/internal/common/license"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

// metricsInterval 是运行指标的采集与上报间隔，control 按该频率判断服务器是否在线。
const metricsInterval = time.Minute

// Gauge 定义一个按次采集的整数指标。
type Gauge struct {
	Name        string
	Description string
}

// Collect 返回各指标的当前值，按指标名索引；返回 nil 时本次不上报。
type Collect func(context.Context) (map[string]int64, error)

// Metrics 按固定间隔采集并经 OTLP/HTTP 向 control 上报运行指标。
type Metrics struct {
	provider *sdkmetric.MeterProvider
}

// StartMetrics 注册指标并开始每分钟采集上报。
func (c *Client) StartMetrics(ctx context.Context, gauges []Gauge, collect Collect) (*Metrics, error) {
	exporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(c.baseURL+"/otlp/v1/metrics"),
		otlpmetrichttp.WithHTTPClient(c.http),
	)
	if err != nil {
		return nil, fmt.Errorf("create metrics exporter: %w", err)
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(resource.NewSchemaless(
			attribute.String("service.name", license.ProductID),
			attribute.String("service.version", c.version),
		)),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(nonEmptyExporter{exporter}, sdkmetric.WithInterval(metricsInterval))),
	)
	meter := provider.Meter(license.ProductID)
	instruments := make(map[string]metric.Int64ObservableGauge, len(gauges))
	observables := make([]metric.Observable, 0, len(gauges))
	for _, gauge := range gauges {
		instrument, err := meter.Int64ObservableGauge(gauge.Name, metric.WithDescription(gauge.Description))
		if err != nil {
			return nil, fmt.Errorf("create metric %s: %w", gauge.Name, err)
		}
		instruments[gauge.Name] = instrument
		observables = append(observables, instrument)
	}
	if _, err := meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		values, err := collect(ctx)
		if err != nil {
			return err
		}
		for name, value := range values {
			if instrument, ok := instruments[name]; ok {
				observer.ObserveInt64(instrument, value)
			}
		}
		return nil
	}, observables...); err != nil {
		return nil, fmt.Errorf("register metrics callback: %w", err)
	}
	return &Metrics{provider: provider}, nil
}

// Shutdown 上报剩余指标后停止采集。
func (m *Metrics) Shutdown(ctx context.Context) error {
	return m.provider.Shutdown(ctx)
}

// nonEmptyExporter 只上报含指标的采集结果，上报失败记录警告日志。
type nonEmptyExporter struct {
	sdkmetric.Exporter
}

// Export 采集结果没有任何指标时直接返回，其余交给 OTLP 导出器；本次上报失败时记录日志，下个周期重新上报。
func (e nonEmptyExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	for _, scope := range metrics.ScopeMetrics {
		if len(scope.Metrics) > 0 {
			if err := e.Exporter.Export(ctx, metrics); err != nil {
				slog.Warn("上报运行指标失败", "error", err)
			}
			return nil
		}
	}
	return nil
}
