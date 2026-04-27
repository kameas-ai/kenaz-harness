package telemetry

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// OTLPSpanConfig drives NewOTLPSpanExporter. Endpoint is the absolute
// URL the SDK will POST OTLP/HTTP protobuf payloads to (the SDK
// appends nothing to it; we wire WithEndpointURL so callers can use
// either http:// or https:// schemes). Headers are passed through
// otlptracehttp.WithHeaders for auth/proxy plumbing. Insecure must be
// true when Endpoint uses the http:// scheme — keeping the flag
// explicit avoids accidentally posting traces in cleartext to a
// misconfigured endpoint.
type OTLPSpanConfig struct {
	Endpoint string
	Headers  map[string]string
	Insecure bool
}

// NewOTLPSpanExporter constructs an OTLP-HTTP span exporter targeting
// cfg.Endpoint. Empty Endpoint returns (nil, nil) so the composite
// fan-out can skip the OTLP side without branching at the call site
// (NFR-005: zero outbound work when no endpoint is configured).
//
// The standard /v1/traces path is used unless cfg.Endpoint already
// carries a path. http:// schemes require cfg.Insecure = true; the
// constructor errors out otherwise so a typo in the Settings UI can't
// silently degrade transport security.
func NewOTLPSpanExporter(ctx context.Context, cfg OTLPSpanConfig) (sdktrace.SpanExporter, error) {
	if cfg.Endpoint == "" {
		return nil, nil
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errors.New("telemetry: NewOTLPSpanExporter: endpoint scheme must be http or https")
	}
	if scheme == "http" && !cfg.Insecure {
		return nil, errors.New("telemetry: NewOTLPSpanExporter: http:// endpoint requires Insecure=true")
	}

	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(cfg.Endpoint),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
	}
	if u.Path == "" {
		opts = append(opts, otlptracehttp.WithURLPath("/v1/traces"))
	}
	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return exp, nil
}
