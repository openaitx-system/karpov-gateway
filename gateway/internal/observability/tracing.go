package observability

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// TracerProviderConfig 控制 OTEL TracerProvider 的初始化。
type TracerProviderConfig struct {
	ServiceName    string
	ServiceVersion string
	Exporter       trace.SpanExporter // 注入自定义 exporter；nil 时使用 noop
	SampleRatio    float64            // 0..1；0=不采样、1=全采样
}

// SetupTracerProvider 装配并设置全局 OTEL TracerProvider。
//
// 调用方应在 main 启动早期调用一次；返回的 shutdown 应在退出时执行（flush + close）。
//
// 不强制要求 Exporter：在测试 / 单元集成里把 Exporter 留 nil 即可启用 noop tracer，
// 业务代码可以始终用 otel.Tracer(...) 而无需特判。
func SetupTracerProvider(cfg TracerProviderConfig) (shutdown func(context.Context) error, err error) {
	if cfg.ServiceName == "" {
		return nil, errors.New("observability: empty service name")
	}
	if cfg.Exporter == nil {
		// 设置 noop tracer，后续 otel.Tracer(...).Start(...) 都返回 no-op span
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(_ context.Context) error { return nil }, nil
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
	))
	if err != nil {
		return nil, err
	}
	ratio := cfg.SampleRatio
	if ratio <= 0 {
		ratio = 0.05 // 默认 5% 采样
	}
	if ratio > 1 {
		ratio = 1
	}
	tp := trace.NewTracerProvider(
		trace.WithBatcher(cfg.Exporter),
		trace.WithResource(res),
		trace.WithSampler(trace.ParentBased(trace.TraceIDRatioBased(ratio))),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// SpanWithAttrs 是 otel span 起手 helper：自动加 service/method 标签。
//
// 用法：
//
//	ctx, span := observability.SpanWithAttrs(ctx, "music", "GetSong",
//	    attribute.String("provider", "qqmusic"))
//	defer span.End()
func SpanWithAttrs(ctx context.Context, service, method string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	tracer := otel.Tracer(service)
	allAttrs := append([]attribute.KeyValue{
		attribute.String("service.method", method),
	}, attrs...)
	return tracer.Start(ctx, method, oteltrace.WithAttributes(allAttrs...))
}
