package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// proxy_probe.go probes proxy connectivity and parses exit information from
// ordered target endpoints.
// Proxy reachability probes try exit-information targets in order and stop at
// the first successfully parsed HTTP 200 response. Explicit configured targets
// replace the built-in list, allowing the application to use endpoints
// available through a selected proxy.
//
// One shared HTTP client supplies per-request timeout and address checks, while
// each response has a bounded body read and parser-specific success conditions.
// Returned latency measures through response headers and excludes body parsing;
// overall failure discards target latencies and reports the last error.
// Configured insecure certificate verification is rejected during client
// construction. The adapter closes each response body, while shared client
// lifetime remains with the HTTP client pool.

// Proxy probe defaults bound each HTTP attempt and the response bytes accepted
// before parsing its exit-information payload.
const (
	defaultProxyProbeTimeout          = 10 * time.Second
	defaultProxyProbeResponseMaxBytes = int64(1024 * 1024)
)

// probeURLs lists built-in probe targets in priority order.
// Multiple targets support proxies that restrict which domains they can reach.
var probeURLs = []struct {
	url    string // exit-information target URL text
	parser string // selected response payload parser
}{
	{"http://ip-api.com/json/?lang=zh-CN", "ip-api"},
	{"http://api64.ipify.org?format=json", "ipify"},
}

// ProxyProbeTarget couples an exit-information endpoint with the response
// parser used after reachability succeeds.
type ProxyProbeTarget struct {
	URL    string // configured exit-information endpoint URL
	Parser string // configured response parser name
}

// ProxyProbeOptions supplies address checks, response limits, and ordered
// exit-information probe targets.
type ProxyProbeOptions struct {
	// ValidateResolvedIP and AllowPrivateHosts select shared-client address checks.
	// InsecureSkipVerify causes HTTP-client construction failure when probing
	// begins. MaxResponseBytes defaults when nonpositive, and nonempty Targets
	// replaces the built-in target list.
	InsecureSkipVerify bool // rejected insecure certificate flag
	AllowPrivateHosts  bool // resolved private-address permission flag
	ValidateResolvedIP bool // resolved-address validation activation flag
	// MaxResponseBytes needs room for one additional byte within signed int64.
	// probeWithURL uses that extra byte to reject body overflow; nonpositive input
	// selects the default before attempting the read.
	MaxResponseBytes int64              // maximum accepted response bytes
	Targets          []ProxyProbeTarget // ordered configured probe targets
}

// configuredProbeTarget retains one copied endpoint/parser pair in configured
// probe order.
type configuredProbeTarget struct {
	url    string // exit-information target URL text
	parser string // selected response payload parser
}

// proxyProbeService retains probe construction options and normalized
// response-limit selection for later attempts.
//
// configuredProbeURLs is copied during construction and replaces built-ins
// when populated. The retained transport flags are passed to the shared HTTP
// client for each ProbeProxy operation.
type proxyProbeService struct {
	insecureSkipVerify  bool                    // retained rejected certificate option
	allowPrivateHosts   bool                    // resolved private-address permission flag
	validateResolvedIP  bool                    // resolved-address validation activation flag
	maxResponseBytes    int64                   // maximum accepted response bytes
	configuredProbeURLs []configuredProbeTarget // owned configured target pairs
}

// NewProxyExitInfoProber copies configured target pairs and resolves a
// nonpositive response limit to one MiB. InsecureSkipVerify is retained and
// warned about because the shared HTTP client rejects that option when probing
// begins.
func NewProxyExitInfoProber(options ProxyProbeOptions) egress.ProxyExitInfoProber {
	maxBytes := options.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = defaultProxyProbeResponseMaxBytes
	}
	if options.InsecureSkipVerify {
		logging.LegacyPrintf("provider.proxy_probe", "[ProxyProbe] Warning: insecure_skip_verify is not allowed and will cause probe failure.")
	}
	var targets []configuredProbeTarget
	if len(options.Targets) > 0 {
		targets = make([]configuredProbeTarget, 0, len(options.Targets))
		for _, target := range options.Targets {
			targets = append(targets, configuredProbeTarget{url: target.URL, parser: target.Parser})
		}
	}
	return &proxyProbeService{insecureSkipVerify: options.InsecureSkipVerify, allowPrivateHosts: options.AllowPrivateHosts, validateResolvedIP: options.ValidateResolvedIP, maxResponseBytes: maxBytes, configuredProbeURLs: targets}
}

// ProbeProxy tries the configured target sequence, or built-ins when none were
// supplied, through one proxy client. It returns exit metadata and
// response-header latency for the first successfully parsed target.
//
// Client construction failure returns before attempts. Individual target errors
// cause the next endpoint to be tried, including HTTP, read-limit, or parser
// failure. If all fail, the returned latency is zero and the final error wraps
// the last attempt; the caller's context is passed to every request and the
// client timeout applies separately to each attempt.
func (s *proxyProbeService) ProbeProxy(ctx context.Context, proxyURL string) (*egress.ProxyExitInfo, int64, error) {
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:           proxyURL,
		Timeout:            defaultProxyProbeTimeout,
		InsecureSkipVerify: s.insecureSkipVerify,
		ValidateResolvedIP: s.validateResolvedIP,
		AllowPrivateHosts:  s.allowPrivateHosts,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create proxy client: %w", err)
	}

	// A proxy may reach one exit-information service and reject another.
	// Try the selected ordered target list, retaining its final error for diagnosis.
	var lastErr error
	if len(s.configuredProbeURLs) > 0 {
		for _, probe := range s.configuredProbeURLs {
			exitInfo, latencyMs, err := s.probeWithURL(ctx, client, probe.url, probe.parser)
			if err == nil {
				return exitInfo, latencyMs, nil
			}
			lastErr = err
		}
		return nil, 0, fmt.Errorf("all probe URLs failed, last error: %w", lastErr)
	}
	for _, probe := range probeURLs {
		exitInfo, latencyMs, err := s.probeWithURL(ctx, client, probe.url, probe.parser)
		if err == nil {
			return exitInfo, latencyMs, nil
		}
		lastErr = err
	}

	return nil, 0, fmt.Errorf("all probe URLs failed, last error: %w", lastErr)
}

// probeWithURL sends an HTTP GET, requires status 200, reads a bounded payload,
// and applies the selected exit parser. After a successful client.Do, it closes
// the received response body and reports milliseconds measured before body read
// and parsing.
//
// The body limit reads one extra byte so an oversized response is rejected
// rather than parsed as a successful prefix. Unknown parser names return an
// error after reading the body. The supplied client determines transport and
// redirect handling, and later target retry belongs to ProbeProxy.
func (s *proxyProbeService) probeWithURL(ctx context.Context, client *http.Client, url string, parser string) (*egress.ProxyExitInfo, int64, error) {
	startTime := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("proxy connection failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	latencyMs := time.Since(startTime).Milliseconds()

	if resp.StatusCode != http.StatusOK {
		return nil, latencyMs, fmt.Errorf("request failed with status: %d", resp.StatusCode)
	}

	maxResponseBytes := s.maxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultProxyProbeResponseMaxBytes
	}
	// Read one byte beyond the accepted cap to distinguish a complete payload
	// from an oversized prefix that could otherwise look like parseable exit data.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, latencyMs, fmt.Errorf("failed to read response: %w", err)
	}
	if int64(len(body)) > maxResponseBytes {
		return nil, latencyMs, fmt.Errorf("proxy probe response exceeds limit: %d", maxResponseBytes)
	}

	switch parser {
	case "ip-api":
		return s.parseIPAPI(body, latencyMs)
	case "ipify":
		return s.parseIPify(body, latencyMs)
	case "chatgpt-trace":
		return s.parseChatGPTTrace(body, latencyMs)
	default:
		return nil, latencyMs, fmt.Errorf("unknown parser: %s", parser)
	}
}

// parseIPAPI decodes an ip-api payload, requires a case-insensitive success
// status, and returns exit metadata with the supplied latency. RegionName wins
// over Region; parsing diagnostics include a bounded raw-body preview when JSON
// decoding fails.
func (s *proxyProbeService) parseIPAPI(body []byte, latencyMs int64) (*egress.ProxyExitInfo, int64, error) {
	var ipInfo struct {
		Status      string `json:"status"`      // payload success status text
		Message     string `json:"message"`     // remote failure message text
		Query       string `json:"query"`       // reported proxy exit address
		City        string `json:"city"`        // reported exit city text
		Region      string `json:"region"`      // short reported region code
		RegionName  string `json:"regionName"`  // preferred reported region name
		Country     string `json:"country"`     // reported exit country text
		CountryCode string `json:"countryCode"` // reported exit country code
	}

	if err := json.Unmarshal(body, &ipInfo); err != nil {
		preview := string(body)
		// Bound malformed response text included in the parser diagnostic.
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return nil, latencyMs, fmt.Errorf("failed to parse response: %w (body: %s)", err, preview)
	}
	if !strings.EqualFold(ipInfo.Status, "success") {
		if ipInfo.Message == "" {
			ipInfo.Message = "ip-api request failed"
		}
		return nil, latencyMs, fmt.Errorf("ip-api request failed: %s", ipInfo.Message)
	}

	region := ipInfo.RegionName
	if region == "" {
		region = ipInfo.Region
	}
	return &egress.ProxyExitInfo{
		IP:          ipInfo.Query,
		City:        ipInfo.City,
		Region:      region,
		Country:     ipInfo.Country,
		CountryCode: ipInfo.CountryCode,
	}, latencyMs, nil
}

// parseIPify decodes an ipify JSON payload and requires a nonempty IP string.
// It returns that text with the supplied latency, leaving textual IP validity
// and location metadata outside this parser's checks.
func (s *proxyProbeService) parseIPify(body []byte, latencyMs int64) (*egress.ProxyExitInfo, int64, error) {
	var result struct {
		IP string `json:"ip"` // reported proxy exit address
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, latencyMs, fmt.Errorf("failed to parse ipify response: %w", err)
	}
	if result.IP == "" {
		return nil, latencyMs, fmt.Errorf("ipify: no IP found in response")
	}
	return &egress.ProxyExitInfo{
		IP: result.IP,
	}, latencyMs, nil
}

// parseChatGPTTrace extracts ip and loc values from newline-delimited
// Cloudflare trace text. Later matching lines replace earlier values, missing
// ip returns a bounded-body error, and loc supplies CountryCode when present;
// supplied latency is retained.
func (s *proxyProbeService) parseChatGPTTrace(body []byte, latencyMs int64) (*egress.ProxyExitInfo, int64, error) {
	var ip, loc string
	for line := range strings.SplitSeq(string(body), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch key {
		case "ip":
			ip = strings.TrimSpace(value)
		case "loc":
			loc = strings.TrimSpace(value)
		}
	}
	if ip == "" {
		preview := string(body)
		// Bound malformed response text included in the parser diagnostic.
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return nil, latencyMs, fmt.Errorf("chatgpt-trace: no ip= found in response (body: %s)", preview)
	}
	info := &egress.ProxyExitInfo{IP: ip}
	if loc != "" {
		info.CountryCode = loc
	}
	return info, latencyMs, nil
}
