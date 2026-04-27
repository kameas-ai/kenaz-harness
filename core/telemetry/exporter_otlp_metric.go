package telemetry

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// OTLPMetricConfig mirrors OTLPSpanConfig for the metric pipeline.
type OTLPMetricConfig struct {
	Endpoint string
	Headers  map[string]string
	Insecure bool
}

// NewOTLPMetricExporter constructs an OTLP-HTTP metric exporter
// targeting cfg.Endpoint. Empty Endpoint returns (nil, nil) so the
// composite reader can skip the OTLP side. Path defaults to /v1/metrics.
func NewOTLPMetricExporter(ctx context.Context, cfg OTLPMetricConfig) (sdkmetric.Exporter, error) {
	if cfg.Endpoint == "" {
		return nil, nil
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("telemetry: NewOTLPMetricExporter: endpoint scheme must be http or https")
	}
	if scheme == "http" && !cfg.Insecure {
		return nil, errors.New("telemetry: NewOTLPMetricExporter: http:// endpoint requires Insecure=true")
	}
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(cfg.Endpoint),
	}
	if cfg.Insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(cfg.Headers))
	}
	if u.Path == "" {
		opts = append(opts, otlpmetrichttp.WithURLPath("/v1/metrics"))
	}
	exp, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return exp, nil
}
