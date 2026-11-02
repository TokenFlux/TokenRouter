package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/imroc/req/v3"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
	servertiming "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// pool.go constructs and caches HTTP clients with optional host validation and
// request timing.
// Shared HTTP clients cache supplied construction options, including proxy
// text and timing or connection settings, and callers keep the published
// client immutable. Optional host validation checks DNS before sending a
// request and caches successful host checks for thirty seconds. Direct clients
// with validation also dial only addresses checked at connection time, so a
// changed DNS answer cannot redirect them; a proxy still resolves its own target.
//
// The validation cache is independent of connection reuse. Concurrent misses
// can validate the same host more than once, and lazy deletion removes
// expired or unusable entries on a later read. All clients are wrapped for
// request-context dependency timing before publication. Idle-close requests
// can reach req clients through their concrete transport, while the ordinary
// timing wrapper has no CloseIdleConnections forwarding method.

// Shared-client defaults bound retained idle connections, direct network/TLS
// phase durations, and the short lifetime of successful DNS host validation.
const (
	defaultMaxIdleConns        = 100              // Maximum idle connections across hosts
	defaultMaxIdleConnsPerHost = 10               // Maximum idle connections per host
	defaultIdleConnTimeout     = 90 * time.Second // Idle connection lifetime for this shared transport
	defaultDialTimeout         = 5 * time.Second  // TCP dial timeout for the default transport dialer
	defaultTLSHandshakeTimeout = 5 * time.Second  // TLS handshake timeout
	validatedHostTTL           = 30 * time.Second // Lifetime of cached DNS validation results
)

var (
	// sharedClients caches constructed HTTP clients so requests with matching supplied
	// options can share one transport and its existing connection pool.
	sharedClients sync.Map

	// validateResolvedIP uses production DNS checks; serial tests replace it
	// while no concurrent request may read this package-level function value.
	validateResolvedIP = ValidateResolvedIP
)

// Options configures shared HTTP client construction and host-validation behavior.
type Options struct {
	ProxyURL              string        // HTTP, HTTPS, SOCKS5, or SOCKS5H proxy URL
	Timeout               time.Duration // Overall request timeout
	ResponseHeaderTimeout time.Duration // Timeout waiting for response headers
	// InsecureSkipVerify requests disabled certificate verification and is
	// rejected by buildTransport. Callers install trusted certificates while
	// keeping upstream verification enabled.
	InsecureSkipVerify bool // Rejected when true; TLS certificate verification is required
	// ValidateResolvedIP enables a pre-request DNS check when AllowPrivateHosts
	// is false. Successful checks are cached by host; direct connections are
	// separately checked and pinned when dialed, while proxied targets are not.
	ValidateResolvedIP bool // Check resolved addresses for nonpublic IPs before requests
	// AllowPrivateHosts suppresses this transport's DNS validation when true.
	// Target-format, literal-address, and allowlist policy remain with the
	// egress caller before transport execution.
	AllowPrivateHosts bool // Bypass resolved-IP validation when private hosts are allowed

	// MaxIdleConns and MaxIdleConnsPerHost select shared transport defaults when
	// nonpositive; MaxConnsPerHost passes through to net/http, where zero is unlimited
	// and negative values also select that library behavior.
	MaxIdleConns        int // Maximum total idle connections; default 100
	MaxIdleConnsPerHost int // Maximum idle connections per host; default 10
	MaxConnsPerHost     int // Maximum connections per host; zero means unlimited
}

// validatedTransport caches successful host checks before delegating outbound HTTP requests.
type validatedTransport struct {
	// base supplies request execution and response cleanup after the validation
	// lookup. Its direct dialer checks addresses again; a proxy resolves by itself.
	base           http.RoundTripper
	validatedHosts sync.Map // host to successful-check expiry time
	// now samples validation-cache time before DNS checks; production uses time.Now
	// and serial tests inject a clock before issuing requests through this wrapper.
	now func() time.Time
}

// GetClient returns a cached HTTP client keyed by supplied options. Callers
// keep the client and its shared transport configuration immutable;
// construction errors such as invalid proxy input or disabled certificate
// verification must be handled before sending requests. If a key contains an
// unexpected value type, it stays stored and this call returns its new candidate.
func GetClient(opts Options) (*http.Client, error) {
	key := buildClientKey(opts)
	if cached, ok := sharedClients.Load(key); ok {
		if client, ok := cached.(*http.Client); ok {
			return client, nil
		}
	}

	client, err := buildClient(opts)
	if err != nil {
		return nil, err
	}

	// Publish the constructed candidate before reuse. A competing *http.Client
	// is returned on type match; an unexpected stored type remains and leaves
	// this candidate uncached.
	actual, _ := sharedClients.LoadOrStore(key, client)
	if c, ok := actual.(*http.Client); ok {
		return c, nil
	}
	return client, nil
}

// buildClient constructs the transport, optionally adding DNS host validation
// and a pinning direct dialer when enabled and private hosts are disallowed. It then wraps
// request-context timing and sets the supplied overall timeout; callers
// retain client and response-body cleanup responsibility.
func buildClient(opts Options) (*http.Client, error) {
	transport, err := buildTransport(opts)
	if err != nil {
		return nil, err
	}

	var rt http.RoundTripper = transport
	if opts.ValidateResolvedIP && !opts.AllowPrivateHosts {
		if strings.TrimSpace(opts.ProxyURL) == "" {
			// The validated host cache outlives DNS answers, so only a check made
			// while dialing can stop a host that later resolves to a private address.
			transport.DialContext = publicTargetDialContext(transport.DialContext)
		}
		rt = newValidatedTransport(transport)
	}
	rt = servertiming.WrapRoundTripper(rt)
	return &http.Client{
		Transport: rt,
		Timeout:   opts.Timeout,
	}, nil
}

// buildTransport creates a certificate-verifying HTTP transport and
// configures its selected proxy. Nonpositive idle limits use defaults, while
// connection and header settings pass through. InsecureSkipVerify is
// rejected; direct routing retains the five-second network and TLS phase
// limits.
func buildTransport(opts Options) (*http.Transport, error) {
	// Use defaults for nonpositive idle connection limits.
	maxIdleConns := opts.MaxIdleConns
	if maxIdleConns <= 0 {
		maxIdleConns = defaultMaxIdleConns
	}
	maxIdleConnsPerHost := opts.MaxIdleConnsPerHost
	if maxIdleConnsPerHost <= 0 {
		maxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: defaultDialTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		MaxConnsPerHost:       opts.MaxConnsPerHost, // Zero means unlimited
		IdleConnTimeout:       defaultIdleConnTimeout,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
	}

	if opts.InsecureSkipVerify {
		// Certificate verification authenticates the selected upstream. Reject a request
		// to disable it before the transport can be published to shared callers.
		return nil, fmt.Errorf("insecure_skip_verify is not allowed; install a trusted certificate instead")
	}

	_, parsed, err := proxy.Parse(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	if parsed == nil {
		return transport, nil
	}

	if err := proxy.ConfigureTransportProxy(transport, parsed); err != nil {
		return nil, err
	}

	return transport, nil
}

// buildClientKey encodes trimmed supplied proxy text and every construction
// option into the shared-client identity. Raw nonpositive limits remain in
// the key even when construction selects equal defaults, so effective
// equivalence does not merge entries.
func buildClientKey(opts Options) string {
	return fmt.Sprintf("%s|%s|%s|%t|%t|%t|%d|%d|%d",
		strings.TrimSpace(opts.ProxyURL),
		opts.Timeout.String(),
		opts.ResponseHeaderTimeout.String(),
		opts.InsecureSkipVerify,
		opts.ValidateResolvedIP,
		opts.AllowPrivateHosts,
		opts.MaxIdleConns,
		opts.MaxIdleConnsPerHost,
		opts.MaxConnsPerHost,
	)
}

// newValidatedTransport wraps base with an empty successful-host validation
// cache and wall clock. Callers provide a usable base before sending
// requests; tests can replace the clock and package validator serially.
func newValidatedTransport(base http.RoundTripper) *validatedTransport {
	return &validatedTransport{
		base: base,
		now:  time.Now,
	}
}

// isValidatedHost reports whether host has a stored expiry strictly after
// now. Nil receivers and misses return false; unusable values and expired
// entries are deleted, allowing a subsequent request to perform validation
// again.
func (t *validatedTransport) isValidatedHost(host string, now time.Time) bool {
	if t == nil {
		return false
	}
	raw, ok := t.validatedHosts.Load(host)
	if !ok {
		return false
	}
	expireAt, ok := raw.(time.Time)
	if !ok {
		// Treat unexpected cache data as absent so the next request can obtain
		// a real DNS validation decision before its base transport sends the request.
		t.validatedHosts.Delete(host)
		return false
	}
	if now.Before(expireAt) {
		return true
	}
	// Expiry removes this remembered validation decision; connection reuse remains
	// independent, and the next request must perform its own host validation lookup.
	t.validatedHosts.Delete(host)
	return false
}

// RoundTrip validates a nonempty request hostname unless its cached check
// remains valid, then delegates to base. The validator uses its own bounded
// background lookup, and success caches from the sampled prelookup time; this
// early check rejects a request before any connection is reused. Construct a non-nil wrapper before
// requests, and retain the base's body cleanup obligations.
func (t *validatedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req != nil && req.URL != nil {
		host := strings.ToLower(strings.TrimSpace(req.URL.Hostname()))
		if host != "" {
			now := time.Now()
			if t != nil && t.now != nil {
				now = t.now()
			}
			if !t.isValidatedHost(host, now) {
				if err := validateResolvedIP(host); err != nil {
					return nil, err
				}
				// Remember the successful decision from the prelookup clock sample. This
				// cache avoids repeat validation but does not choose the base transport's dialed IP.
				t.validatedHosts.Store(host, now.Add(validatedHostTTL))
			}
		}
	}
	if t == nil || t.base == nil {
		return nil, fmt.Errorf("validated transport base is nil")
	}
	return t.base.RoundTrip(req)
}

// DefaultBatchHTTPClient returns a shared batch client with phase budgets and
// a sixty-second response-header limit. Its zero overall timeout permits long
// uploads or streams; an unexpected construction error returns
// http.DefaultClient as the recovery choice.
func DefaultBatchHTTPClient() *http.Client {
	client, err := GetClient(Options{
		ResponseHeaderTimeout: 60 * time.Second,
	})
	if err != nil {
		// Batch callers have no error result from this accessor. Supply the
		// standard default client if shared construction unexpectedly fails for these fixed options.
		return http.DefaultClient
	}
	return client
}

// CloseSharedIdleConnections asks cached HTTP and req clients to close idle
// connections while retaining cache entries. Ordinary clients constructed
// here have a timing wrapper lacking idle-close forwarding; req clients
// expose that operation through their concrete transport. Active response
// bodies remain caller-owned.
func CloseSharedIdleConnections() {
	sharedClients.Range(func(_, value any) bool {
		if client, ok := value.(*http.Client); ok {
			client.CloseIdleConnections()
		}
		return true
	})
	sharedReqClients.Range(func(_, value any) bool {
		if client, ok := value.(*req.Client); ok {
			client.GetClient().CloseIdleConnections()
		}
		return true
	})
}

// ValidateResolvedIP resolves host with a five-second background budget and
// rejects lookup errors or any nonpublic address. It only answers for this
// lookup. Connections made later need publicTargetDialContext to keep the
// checked address, and a proxy target stays outside both checks.
func ValidateResolvedIP(host string) error {
	ctx, cancel := context.WithTimeout(context.Background(), publicTargetLookupTimeout)
	defer cancel()

	_, err := resolvePublicIPs(ctx, host)
	return err
}
