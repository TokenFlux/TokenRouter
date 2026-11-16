package timing

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
)

// http.go records external HTTP durations and tracks pending response headers.
// External dependency timing ends when Do or RoundTrip returns a response or
// error, leaving body reads to the caller. A context override selects a sanitized
// dependency label; ordered hostname checks otherwise choose a fixed label so
// the raw URL never becomes a metric name.
//
// Request liveness remains active even when dependency metrics are disabled.
// Each Do call publishes its header wait before notifying downstream output,
// then releases that wait on every return. Header arrival renews activity before
// release, giving body consumption its own idle allowance after a long transport
// wait. Downstream heartbeat commitment cannot end this upstream wait by itself.

// dependencyModuleKey identifies the optional package-private HTTP dependency label override.
type dependencyModuleKey struct{}

// timingRoundTripper records a transport call's elapsed interval through response return.
type timingRoundTripper struct {
	// base retains the caller's transport and response-body ownership rules;
	// this wrapper delegates each request without closing that transport's resources.
	base http.RoundTripper
}

// WithDependencyModule attaches a sanitized dependency label to ctx, using
// background for nil. Blank sanitized input leaves the context unchanged,
// including any inherited override; one existing dep_ prefix is removed
// before storage.
func WithDependencyModule(ctx context.Context, module string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	module = strings.TrimPrefix(normalizeMetricName(module), dependencyPrefix)
	if module == "" {
		return ctx
	}
	return context.WithValue(ctx, dependencyModuleKey{}, module)
}

// WrapRoundTripper records active requests around base.RoundTrip and returns
// the wrapper. Nil base selects http.DefaultTransport; an existing
// timingRoundTripper is reused, while callers retain ownership of response
// bodies and transport lifecycle.
func WrapRoundTripper(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*timingRoundTripper); ok {
		return base
	}
	return &timingRoundTripper{base: base}
}

// InstrumentClient returns a shallow client copy with its transport wrapped
// for request timing. Nil input uses an empty client; the copied client
// retains shared transport resources, while its configuration can be set
// separately by the caller.
func InstrumentClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	copyClient.Transport = WrapRoundTripper(copyClient.Transport)
	return &copyClient
}

// Do records one active dependency interval around client.Do, including
// failure and redirects, using http.DefaultClient for nil. Body consumption
// remains caller-owned; using an already instrumented transport can record
// additional counts inside the same observed call. Request stages retain the
// actual header-wait lifetime independently of downstream HTTP commitment.
//
// Each dispatch publishes its wait before notifying the downstream observer.
// Every return releases that wait, including transport failure; received headers
// renew activity before release so a long header wait cannot immediately consume
// the subsequent body's inactivity budget. Dependency timing may be disabled
// without disabling either of these request-liveness observations.
func Do(client *http.Client, req *http.Request) (response *http.Response, err error) {
	if client == nil {
		client = http.DefaultClient
	}
	if req != nil {
		if s := StagesFromContext(req.Context()); s != nil {
			if attempt := telemetry.RequestAttemptFromContext(req.Context()); attempt.Number > 0 && req.ContentLength >= 0 {
				s.SetAttemptRequestBytes(int64(attempt.Number), req.ContentLength)
			}
			// Header waiting follows every upstream exchange even when dependency
			// timing is disabled, so metrics settings cannot disable downstream liveness.
			s.upstreamHeaderWaits.Add(1)
			// Publish header arrival before ending the wait. A downstream timer
			// observing no pending dispatch then sees the body's fresh idle baseline.
			defer func() {
				if response != nil {
					s.ObserveUpstreamActivity(time.Now())
				}
				s.upstreamHeaderWaits.Add(-1)
			}()
			s.ObserveUpstreamActivity(time.Now())
			if fn := s.listener(); fn != nil {
				fn(req.Context())
			}
		}
	}
	if req == nil || !Active(req.Context()) {
		return client.Do(req)
	}
	startedAt := time.Now()

	response, err = client.Do(req)
	RecordDependency(req.Context(), dependencyModule(req), startedAt, time.Now())
	return response, err
}

// RoundTrip delegates req to the underlying transport and records its
// completed interval when timing is active, including errors. The returned
// body remains unread by this wrapper and retains the base transport's
// cleanup requirements.
func (t *timingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || !Active(req.Context()) {
		return t.base.RoundTrip(req)
	}
	startedAt := time.Now()
	response, err := t.base.RoundTrip(req)
	RecordDependency(req.Context(), dependencyModule(req), startedAt, time.Now())
	return response, err
}

// dependencyModule returns a context override or a fixed label chosen by
// ordered hostname substring checks. Missing URL information and unmatched
// hosts select http; matches are classification hints rather than validation
// of a service's ownership.
func dependencyModule(req *http.Request) string {
	if req != nil {
		if module, ok := req.Context().Value(dependencyModuleKey{}).(string); ok && module != "" {
			return module
		}
	}
	if req == nil || req.URL == nil {
		return "http"
	}
	// Check Google-hosted Gemini and Antigravity names before the broader Google
	// substring, so those dependency observations retain their more specific platform label.
	host := strings.ToLower(req.URL.Hostname())
	switch {
	case strings.Contains(host, "github"):
		return "github"
	case strings.Contains(host, "openai"):
		return "openai"
	case strings.Contains(host, "anthropic"):
		return "anthropic"
	case strings.Contains(host, "generativelanguage") || strings.Contains(host, "gemini"):
		return "gemini"
	case strings.Contains(host, "cloudcode") || strings.Contains(host, "antigravity"):
		return "antigravity"
	case strings.Contains(host, "googleapis") || strings.Contains(host, "google"):
		return "google"
	case strings.Contains(host, "amazonaws") || strings.Contains(host, "cloudflarestorage") || strings.Contains(host, "s3"):
		return "s3"
	case strings.Contains(host, "stripe") || strings.Contains(host, "airwallex") || strings.Contains(host, "alipay") || strings.Contains(host, "wechatpay") || strings.Contains(host, "paypal"):
		return "payment"
	default:
		return "http"
	}
}
