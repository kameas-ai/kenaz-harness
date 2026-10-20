package llm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"
)

// LogSafeEndpoint renders a provider endpoint URL for an error string, a
// log line or an event payload as scheme, host and path only.
//
// A user can configure https://<token>@host/v1, http://user:key@host/v1 or
// https://host/v1?api_key=K as a provider endpoint; none of those secrets
// may reach an error that the registry hands to emitter.Error (persisted
// audit/events). Userinfo — password AND a bare username, which is how a
// token-in-URL is usually written — is masked, the query and fragment are
// dropped entirely, and so are path ";params" (matrix parameters, where
// some gateways carry a key). An opaque URL (scheme:SECRET@x) keeps only
// its scheme. An unparseable value renders as a fixed placeholder rather
// than verbatim, for the same reason.
//
// The path itself is KEPT: it is what makes the endpoint recognisable in
// an error ("/openai/v1" vs "/v1"), and a secret embedded as a plain path
// segment cannot be told apart from a deployment or model name. Exact
// path-borne secrets are the caller's to scrub (see ScrubSecrets).
func LogSafeEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable>"
	}
	if u.Opaque != "" {
		return u.Scheme + ":xxxxx"
	}
	if u.User != nil {
		u.User = url.User("xxxxx")
	}
	if strings.Contains(u.Path, ";") {
		segs := strings.Split(u.Path, "/")
		for i, seg := range segs {
			if j := strings.IndexByte(seg, ';'); j >= 0 {
				segs[i] = seg[:j]
			}
		}
		u.Path = strings.Join(segs, "/")
	}
	u.RawPath = ""
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// TransportFailureReason maps a transport-level failure to a fixed
// vocabulary (timeout / canceled / dns / connect_refused /
// connection_reset / tls / eof / connect_failed / network). It never
// returns err.Error(), so it is safe for any sink.
func TransportFailureReason(err error) string {
	var dnsErr *net.DNSError
	var opErr *net.OpError
	var certErr *tls.CertificateVerificationError
	var hostErr x509.HostnameError
	var unknownCA x509.UnknownAuthorityError
	var invalidCert x509.CertificateInvalidError
	var recErr tls.RecordHeaderError
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &dnsErr):
		return "dns"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connect_refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset"
	case errors.As(err, &certErr), errors.As(err, &hostErr), errors.As(err, &unknownCA),
		errors.As(err, &invalidCert), errors.As(err, &recErr):
		return "tls"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "eof"
	case errors.As(err, &opErr):
		return "connect_failed"
	default:
		return "network"
	}
}

// safeURLError replaces a *url.Error whose message would embed the raw
// request URL. Error() is "<Op> <LogSafeEndpoint(URL)>: <reason>"; the
// wrapped url.Error's own text is never used. Unwrap returns the
// url.Error's inner error, so errors.Is(err, context.DeadlineExceeded),
// errors.As(err, &*net.OpError) and friends keep working, and Timeout()
// is preserved so a net.Error check sees the same verdict.
type safeURLError struct {
	op      string
	url     string
	reason  string
	timeout bool
	err     error
}

func (e *safeURLError) Error() string {
	return e.op + " " + e.url + ": " + e.reason
}

func (e *safeURLError) Unwrap() error   { return e.err }
func (e *safeURLError) Timeout() bool   { return e.timeout }
func (e *safeURLError) Temporary() bool { return e.timeout }

// RedactURLError returns err with the request URL made log-safe when err
// is a *url.Error (what http.Client.Do and http.NewRequest return); any
// other error is returned unchanged. Call it on the immediate return of
// Do / NewRequest, before wrapping.
func RedactURLError(err error) error {
	var ue *url.Error
	if err == nil || !errors.As(err, &ue) || ue != err { //nolint:errorlint // identity: only the direct url.Error is rebuilt
		return err
	}
	reason := TransportFailureReason(ue.Err)
	if ue.Op == "parse" {
		reason = "invalid URL"
	}
	return &safeURLError{
		op:      ue.Op,
		url:     LogSafeEndpoint(ue.URL),
		reason:  reason,
		timeout: ue.Timeout(),
		err:     ue.Err,
	}
}

// NewTransportError builds the *ErrTransient an adapter returns for an
// http.Client.Do failure, with the URL redacted (see RedactURLError).
// prefix is prepended to the message verbatim (e.g. "gemini: http: ").
func NewTransportError(prefix string, err error) *ErrTransient {
	red := RedactURLError(err)
	msg := ""
	if red != nil {
		msg = red.Error()
	}
	return &ErrTransient{Status: 0, Message: prefix + msg, Cause: red}
}

// minScrubLen is the floor for exact-value scrubbing: shorter values
// would shred ordinary words out of a provider message.
const minScrubLen = 4

// minUserinfoScrubLen is the floor for scrubbing a URL's userinfo parts.
// A short or common username ("user", "admin") is not a secret, and
// replacing every occurrence of it would wreck the diagnostic text; a
// token written as userinfo is long.
const minUserinfoScrubLen = 8

// ScrubSecrets replaces every occurrence of each secret (of length >= 4)
// in msg with "[redacted]", then applies RedactCredentials. Used for
// provider-echoed text where the exact credential (possibly sent under a
// custom header name RedactCredentials cannot recognise) is known — the
// exact-value pass removes it whatever header carried it.
func ScrubSecrets(msg string, secrets ...string) string {
	for _, s := range secrets {
		if len(s) >= minScrubLen {
			msg = strings.ReplaceAll(msg, s, "[redacted]")
		}
	}
	return RedactCredentials(msg)
}

// EndpointSecrets returns the secret-bearing parts of an endpoint URL —
// userinfo username and password (each only when >= 8 bytes) and every
// query value — for ScrubSecrets. Each is returned in BOTH its decoded
// and its raw (percent-encoded) form, since a server may echo either
// (e.g. sig=abc%2Bdef12 comes back as-is or as abc+def12).
func EndpointSecrets(raw string) []string {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	var out []string
	add := func(vals ...string) {
		for _, v := range vals {
			if v != "" {
				out = append(out, v)
			}
		}
	}
	if u.User != nil {
		// Raw userinfo as written: between "//" and the last '@' of the
		// authority.
		var rawUser, rawPass string
		if i := strings.Index(raw, "//"); i >= 0 {
			auth := raw[i+2:]
			if j := strings.IndexAny(auth, "/?#"); j >= 0 {
				auth = auth[:j]
			}
			if k := strings.LastIndexByte(auth, '@'); k >= 0 {
				rawUser, rawPass, _ = strings.Cut(auth[:k], ":")
			}
		}
		user := u.User.Username()
		pass, _ := u.User.Password()
		for _, v := range []string{user, rawUser, pass, rawPass} {
			if len(v) >= minUserinfoScrubLen {
				add(v)
			}
		}
	}
	for _, kv := range strings.Split(u.RawQuery, "&") {
		_, rawVal, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		add(rawVal)
		if dec, err := url.QueryUnescape(rawVal); err == nil && dec != rawVal {
			add(dec)
		}
	}
	return out
}

// RequestSecrets is EndpointSecrets plus the request's credential bytes:
// the exact secret material of one provider request.
func RequestSecrets(endpoint string, cred []byte) []string {
	out := EndpointSecrets(endpoint)
	if len(cred) > 0 {
		out = append(out, string(cred))
	}
	return out
}
