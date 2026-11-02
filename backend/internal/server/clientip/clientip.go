package clientip

import (
	"net"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ipmatch"
)

// clientip.go resolves request client addresses using captured forwarding
// settings and Gin proxy trust.
// A request's client address can come from configured headers, built-in proxy
// headers, or Gin's peer-trust policy. Compatibility mode reads raw forwarding
// values regardless of the direct peer, so deployments relying on it need an
// ingress that controls those headers. A captured request policy keeps identity
// and logging consumers on the same mode and ordered custom names.
//
// Compatibility selection delays private bridge addresses while looking for a
// candidate outside privateNets. Custom candidates take precedence over built-in
// candidates in that preferred class; private-only chains retain a custom then
// built-in fallback. privateNets covers RFC1918, loopback, and ULA ranges, so this
// preference follows those ranges rather than a general routability test.

// forwardedIPSettingsKey identifies the Gin value that pins forwarding mode
// and custom-header order for subsequent client-address readers in one request.
const forwardedIPSettingsKey = "tokenrouter.forwarded_ip_settings"

// privateNets holds fallback-address ranges compiled before requests and read
// unchanged by custom and built-in header preference checks during resolution.
var privateNets []*net.IPNet

// forwardedIPSettings is the copied forwarding policy shared by one request's readers.
type forwardedIPSettings struct {
	// trustForwarded and headers are captured together by SetForwardedIPSettings.
	// A false mode selects Gin resolution, while a true mode uses the captured
	// header order before checking built-in forwarding sources.
	trustForwarded bool // raw forwarding-header trust flag
	// headers uses copied slice storage so changing the caller's names later
	// cannot redirect readers of this already captured request policy.
	headers []string // ordered custom header names
}

// init compiles private fallback ranges before client-address resolution can run.
// The entries cover RFC1918, IPv4/IPv6 loopback, and ULA; invalid fixed CIDRs panic
// so a parsing failure cannot silently omit a configured preference range.
func init() {
	for _, cidr := range []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"::1/128",
		"fc00::/7",
	} {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("invalid CIDR: " + cidr)
		}
		privateNets = append(privateNets, block)
	}
}

// SetForwardedIPSettings pins forwarding mode and header order to a request.
// The header slice is copied before publication, nil contexts are ignored, and
// later caller slice edits cannot change the stored policy. Calling it again
// replaces the stored request policy.
func SetForwardedIPSettings(c *gin.Context, enabled bool, headers []string) {
	if c == nil {
		return
	}
	c.Set(forwardedIPSettingsKey, forwardedIPSettings{
		trustForwarded: enabled,
		headers:        append([]string(nil), headers...),
	})
}

// requestForwardedIPSettings returns the captured policy and whether its type matches.
// Nil contexts, absent values, and incorrectly typed values return false so
// callers can choose their absent-snapshot behavior. Successful results share
// the stored header slice, which these resolvers treat as read-only.
func requestForwardedIPSettings(c *gin.Context) (forwardedIPSettings, bool) {
	if c == nil {
		return forwardedIPSettings{}, false
	}
	value, ok := c.Get(forwardedIPSettingsKey)
	if !ok {
		return forwardedIPSettings{}, false
	}
	settings, ok := value.(forwardedIPSettings)
	return settings, ok
}

// requestUsesLegacyForwardedIPTrust selects compatibility when the captured mode permits it.
// Missing or incorrectly typed request settings select Gin's peer-trust policy, so
// a route without a snapshot cannot be given a client-chosen address.
func requestUsesLegacyForwardedIPTrust(c *gin.Context) bool {
	settings, ok := requestForwardedIPSettings(c)
	return ok && settings.trustForwarded
}

// GetClientIP resolves the request address using its captured forwarding policy.
// A nil context returns an empty string; a captured false mode or an absent
// snapshot delegates to Gin's trusted-proxy resolver.
//
// Compatibility first selects a custom candidate outside privateNets, then a
// built-in candidate outside those ranges. If neither source provides one, the
// custom private fallback precedes the built-in fallback, followed by Gin's
// normalized address. This ordering lets an external forwarded address take
// precedence over a private bridge while retaining a value for private-only
// chains. The deployment's ingress controls the authenticity of raw headers.
func GetClientIP(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if !requestUsesLegacyForwardedIPTrust(c) {
		return GetTrustedClientIP(c)
	}
	return resolveForwardedClientIP(c)
}

// resolveForwardedClientIP applies the compatibility order described by
// GetClientIP using the captured custom names, if any. The caller has already
// chosen compatibility mode and supplies a nonnil context.
func resolveForwardedClientIP(c *gin.Context) string {
	settings, _ := requestForwardedIPSettings(c)
	customIP, customFallback := resolveCustomForwardedClientIP(c, settings.headers)
	if customIP != "" {
		return customIP
	}

	// Search built-in headers before using either private fallback. Their
	// nonprivate candidate can identify the client beyond a custom bridge address.
	legacyIP, legacyFallback := resolveLegacyForwardedHeaderIP(c)
	if legacyIP != "" {
		return legacyIP
	}
	if customFallback != "" {
		return customFallback
	}
	if legacyFallback != "" {
		return legacyFallback
	}
	// With no usable header candidate, Gin's configured peer-trust policy still
	// supplies the address that normal request logging would use for this request.
	return ipmatch.NormalizeIP(c.ClientIP())
}

// resolveCustomForwardedClientIP returns a preferred custom address and private fallback.
// It visits configured names, repeated header values, and comma-separated literals
// in that order. Nil contexts return two empty strings, and invalid literals are
// skipped so later names remain eligible.
//
// The first address outside privateNets ends the scan. The first address within
// those ranges remains available as a fallback, but later private candidates
// cannot displace it; GetClientIP checks built-in nonprivate candidates before
// choosing that saved custom address.
func resolveCustomForwardedClientIP(c *gin.Context, headers []string) (string, string) {
	if c == nil {
		return "", ""
	}
	var fallback string
	for _, header := range headers {
		for _, value := range c.Request.Header.Values(header) {
			for candidate := range strings.SplitSeq(value, ",") {
				// Custom sources accept IP literals. Host:port text fails this check,
				// letting subsequent values or headers supply a valid client candidate.
				parsed := net.ParseIP(strings.TrimSpace(candidate))
				if parsed == nil {
					continue
				}
				normalized := parsed.String()
				// Keep the first private candidate while scanning later headers; a
				// later nonprivate literal takes precedence over the saved bridge.
				if isPrivateIP(normalized) {
					if fallback == "" {
						fallback = normalized
					}
					continue
				}
				return normalized, fallback
			}
		}
	}
	return "", fallback
}

// resolveLegacyForwardedHeaderIP resolves built-in forwarding sources for a nonnil context.
// It prefers the first address outside privateNets from CF-Connecting-IP, X-Real-IP,
// then X-Forwarded-For. normalizeValidIP also accepts literal addresses with ports
// that NormalizeIP can separate.
//
// The fallback retains the first valid CF or real-IP value. X-Forwarded-For
// supplies its first valid value only after its preferred scan finds no
// nonprivate address and the earlier headers supplied no fallback. This keeps
// private-only proxy chains usable without letting their first bridge address
// preempt a later nonprivate forwarded candidate.
func resolveLegacyForwardedHeaderIP(c *gin.Context) (string, string) {
	// Retain an earlier valid bridge while checking subsequent built-ins for
	// a nonprivate address, so header priority still decides private-only fallback.
	var fallback string
	if forwarded := normalizeValidIP(c.GetHeader("CF-Connecting-IP")); forwarded != "" {
		fallback = forwarded
		if !isPrivateIP(forwarded) {
			return forwarded, fallback
		}
	}
	if realIP := normalizeValidIP(c.GetHeader("X-Real-IP")); realIP != "" {
		if fallback == "" {
			fallback = realIP
		}
		if !isPrivateIP(realIP) {
			return realIP, fallback
		}
	}
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		for _, candidate := range ips {
			candidate = normalizeValidIP(candidate)
			if candidate != "" && !isPrivateIP(candidate) {
				return candidate, fallback
			}
		}
		// Reach this pass after the preferred XFF scan found no nonprivate IP.
		// Keep the earliest valid list member only when CF and real-IP supplied none.
		if fallback == "" {
			for _, candidate := range ips {
				if candidate = normalizeValidIP(candidate); candidate != "" {
					fallback = candidate
					break
				}
			}
		}
	}
	return "", fallback
}

// GetTrustedClientIP returns Gin's client address after whitespace and port normalization.
// Configure Engine.SetTrustedProxies before serving requests because Gin owns the
// peer-trust decision. A nil context returns an empty string for absent requests.
func GetTrustedClientIP(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return ipmatch.NormalizeIP(c.ClientIP())
}

// GetSecurityClientIP selects the client address for security-sensitive consumers.
// A captured request mode overrides trustForwarded, keeping repeated readers on
// that request's policy. True uses raw-header compatibility; false uses Gin's
// peer-trust resolution, and nil contexts produce an empty address.
// @project-doc docs/operations/edge_security.md#trusted_client_ip
func GetSecurityClientIP(c *gin.Context, trustForwarded bool) string {
	if requestSettings, ok := requestForwardedIPSettings(c); ok {
		trustForwarded = requestSettings.trustForwarded
	}
	if trustForwarded {
		return resolveForwardedClientIP(c)
	}
	return GetTrustedClientIP(c)
}

// normalizeValidIP canonicalizes a literal address after trimming and optional port removal.
// Hostnames, unknown markers, and other invalid values return an empty string;
// callers use that result to continue scanning later built-in forwarding sources.
func normalizeValidIP(value string) string {
	normalized := ipmatch.NormalizeIP(value)
	parsed := net.ParseIP(normalized)
	if parsed == nil {
		return ""
	}
	return parsed.String()
}

// isPrivateIP reports whether a parsed address lies in the compiled privateNets ranges.
// Invalid text returns false. Header resolvers validate literals before calling
// this helper, so invalid input cannot win their nonprivate-address preference.
func isPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, block := range privateNets {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}
