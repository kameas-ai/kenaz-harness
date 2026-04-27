package telemetry

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// OTLPLogConfig mirrors OTLPSpanConfig for the log pipeline.
type OTLPLogConfig struct {
	Endpoint string
	Headers  map[string]string
	Insecure bool
}

// NewOTLPLogExporter constructs an OTLP-HTTP log exporter targeting
// cfg.Endpoint. Empty Endpoint returns (nil, nil). Path defaults to
// /v1/logs.
func NewOTLPLogExporter(ctx context.Context, cfg OTLPLogConfig) (sdklog.Exporter, error) {
	if cfg.Endpoint == "" {
		return nil, nil
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("telemetry: NewOTLPLogExporter: endpoint scheme must be http or https")
	}
	if scheme == "http" && !cfg.Insecure {
		return nil, errors.New("telemetry: NewOTLPLogExporter: http:// endpoint requires Insecure=true")
	}
	opts := []otlploghttp.Option{
		otlploghttp.WithEndpointURL(cfg.Endpoint),
	}
	if cfg.Insecure {
		opts = append(opts, otlploghttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlploghttp.WithHeaders(cfg.Headers))
	}
	if u.Path == "" {
		opts = append(opts, otlploghttp.WithURLPath("/v1/logs"))
	}
	exp, err := otlploghttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return exp, nil
}
