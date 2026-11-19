package httpclient

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/imroc/req/v3"

	proxyurl "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
	servertiming "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// req_pool.go caches configured req clients and instruments their HTTP
// requests for dependency timing.
// req clients share connections only when their supplied proxy text, overall
// timeout, Chrome impersonation flag, and force-HTTP/2 flag identify the same
// cache key. Proxy syntax is checked during construction, while the key uses
// trimmed input before SOCKS scheme normalization, so equivalent normalized
// proxies can still have separate entries.
//
// Concurrent misses may build several candidates. LoadOrStore chooses a
// shared client when the stored value has the expected type; an unexpected
// existing value stays stored and the new candidate is returned directly.
// Callers treat returned client configuration as immutable, and transport
// instrumentation is installed during construction before the client can be
// published.

// sharedReqClients caches req clients by construction options.
//
// Clients share their underlying connection pools.
// The key includes proxy, timeout, impersonation, and HTTP/2 settings.
// Concurrent misses may construct multiple candidates.
// LoadOrStore selects the shared instance returned to callers.
var sharedReqClients sync.Map

// ReqClientOptions selects cache identity and construction settings for shared req clients.
type ReqClientOptions struct {
	ProxyURL    string        // HTTP, HTTPS, SOCKS5, or SOCKS5H proxy URL
	Timeout     time.Duration // Overall request timeout
	Impersonate bool          // enable req Chrome fingerprint
	ForceHTTP2  bool          // enable forced HTTP/2 negotiation
}

// GetSharedReqClient returns a req client keyed by the supplied construction
// options. Callers keep its configuration immutable after publication,
// because requests can share its transport and connection pool. Invalid proxy
// syntax stops construction.
//
// Concurrent cache misses can construct separate candidates before
// LoadOrStore selects one stored client. If an unexpected value already
// occupies the key, that value remains in the cache and this caller receives
// its new candidate. Timing instrumentation is attached before either return
// path.
func GetSharedReqClient(opts ReqClientOptions) (*req.Client, error) {
	key := buildReqClientKey(opts)
	if cached, ok := sharedReqClients.Load(key); ok {
		if c, ok := cached.(*req.Client); ok {
			return c, nil
		}
	}

	client := req.C().SetTimeout(opts.Timeout).SetCookieJar(nil)
	if opts.ForceHTTP2 {
		client = client.EnableForceHTTP2()
	}
	if opts.Impersonate {
		client = client.ImpersonateChrome()
	}
	trimmed, _, err := proxyurl.Parse(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	if trimmed != "" {
		client.SetProxyURL(trimmed)
	}
	client = InstrumentReqClient(client)

	// Publish only after construction and instrumentation. LoadOrStore selects
	// a competing client when usable; an unexpected cached type remains stored.
	actual, _ := sharedReqClients.LoadOrStore(key, client)
	if c, ok := actual.(*req.Client); ok {
		return c, nil
	}
	return client, nil
}

// InstrumentReqClient adds request-context dependency timing to client's
// transport and returns the same pointer. Nil input returns nil; call before
// concurrent use, since registration mutates the transport and repeated calls
// add another wrapper.
func InstrumentReqClient(client *req.Client) *req.Client {
	if client == nil {
		return nil
	}
	client.GetTransport().WrapRoundTripFunc(func(rt http.RoundTripper) req.HttpRoundTripFunc {
		timed := servertiming.WrapRoundTripper(rt)
		return timed.RoundTrip
	})
	return client
}

// buildReqClientKey returns trimmed supplied proxy text, timeout, and both
// transport flags as one cache identity. It does not normalize URL schemes,
// so normalized routing equivalence alone does not establish client reuse.
func buildReqClientKey(opts ReqClientOptions) string {
	return fmt.Sprintf("%s|%s|%t|%t",
		strings.TrimSpace(opts.ProxyURL),
		opts.Timeout.String(),
		opts.Impersonate,
		opts.ForceHTTP2,
	)
}
