package fleet

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// otlpAckRoundTripper rejects HTTP responses that report success but are not
// an OTLP acknowledgement.
//
// # The bug this exists to prevent
//
// The OTLP/HTTP exporters in the OTel SDK treat any 2xx as a successful
// export. That is a reasonable reading of the spec and a dangerous one in
// practice, because a static-site origin will happily return 2xx for a path
// it has never heard of. Fleet's dashboard host is CloudFront in front of an
// S3 SPA bucket: it answers *every* path, including POST /otlp/v1/traces,
// with 200 and the contents of index.html. Telemetry pointed there did not
// fail — it reported success, forever, while every span was discarded.
//
// A wrong endpoint is a bug you fix once. A success path that cannot tell the
// difference between "the collector accepted this" and "a web server handed
// me a web page" is a bug that hides the next wrong endpoint too. This
// round-tripper closes that: on a 2xx, the response must actually look like
// an OTLP response, or the export becomes an error.
//
// # What counts as an acknowledgement
//
// OTLP/HTTP (spec §"otlp/http response") says a success response carries the
// serialized ExportServiceResponse in the same encoding as the request —
// application/x-protobuf for the binary encoding, application/json for the
// JSON one. Anything else on a 2xx (text/html above all) means the request
// reached something that is not an OTLP receiver.
//
// A 2xx with no Content-Type *and* no body is allowed through: some minimal
// collectors reply with a bare 200, and that shape is at least not a web page.
//
// Non-2xx responses pass through untouched. The SDK exporter already handles
// those correctly — it knows 429/503 are retryable and 4xx are not — and
// wrapping them here would only degrade its backoff behaviour.
type otlpAckRoundTripper struct {
	inner http.RoundTripper
}

// NewOTLPAckRoundTripper wraps inner so that a 2xx response which is not an
// OTLP acknowledgement is surfaced as a transport error instead of a
// successful export. When inner is nil, http.DefaultTransport is used.
//
// Compose it outside the auth transport:
//
//	NewOTLPAckRoundTripper(NewTokenRoundTripper(bearer, nil))
func NewOTLPAckRoundTripper(inner http.RoundTripper) http.RoundTripper {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &otlpAckRoundTripper{inner: inner}
}

// otlpAckMediaTypes is the set of media types an OTLP receiver may use for a
// successful ExportServiceResponse.
var otlpAckMediaTypes = map[string]bool{
	"application/x-protobuf": true,
	"application/protobuf":   true,
	"application/json":       true,
}

// ErrNotOTLPAck is returned when a 2xx response is not an OTLP
// acknowledgement. Callers can test for it with errors.Is.
var ErrNotOTLPAck = errNotOTLPAck{}

type errNotOTLPAck struct{}

func (errNotOTLPAck) Error() string {
	return "fleet/otlp: response was successful but is not an OTLP acknowledgement"
}

func (t *otlpAckRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Only 2xx is at risk of being a false success. Let the SDK exporter apply
	// its own status handling (retry/backoff on 429 + 5xx) to everything else.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}

	ct := resp.Header.Get("Content-Type")
	mediaType := ""
	if ct != "" {
		// Strip any ";charset=..." parameters before comparing.
		if mt, _, mErr := mime.ParseMediaType(ct); mErr == nil {
			mediaType = strings.ToLower(mt)
		} else {
			mediaType = strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
		}
	}

	if otlpAckMediaTypes[mediaType] {
		return resp, nil
	}

	// No Content-Type at all: allowed only if the body is genuinely empty.
	// Read it (a real ack body is tiny) and put it back for the caller.
	if mediaType == "" {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if readErr == nil && len(bytes.TrimSpace(body)) == 0 {
			resp.Body = io.NopCloser(bytes.NewReader(body))
			return resp, nil
		}
		return nil, fmt.Errorf(
			"%w: %s %s returned %d with no Content-Type and a non-empty body (%s) — "+
				"this endpoint is not an OTLP receiver",
			ErrNotOTLPAck, req.Method, req.URL.Redacted(), resp.StatusCode, preview(body),
		)
	}

	// A typed 2xx that is not an OTLP media type. text/html here is the
	// CloudFront/S3 SPA fall-through — the exact shape that made telemetry
	// silently disappear.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	hint := ""
	if strings.HasPrefix(mediaType, "text/html") {
		hint = " — this looks like a static-site origin answering every path " +
			"(SPA fall-through), not an OTLP receiver; check that the OTLP " +
			"endpoint points at the API host from /config.json, not the dashboard host"
	}
	return nil, fmt.Errorf(
		"%w: %s %s returned %d with Content-Type %q (%s)%s",
		ErrNotOTLPAck, req.Method, req.URL.Redacted(), resp.StatusCode, ct, preview(body), hint,
	)
}

// preview renders a short, safe excerpt of a response body for diagnostics.
// Bounded hard: this is for identifying *what kind of thing* answered, not for
// capturing content.
func preview(b []byte) string {
	const max = 64
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "empty body"
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return "body starts: " + s
}
