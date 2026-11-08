package middleware

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// request_origin.go captures the public origin using explicit proxy peer trust.
// Social metadata needs an absolute resource URL even when no frontend address
// is configured. Forwarded host and scheme are accepted only from peers in the
// same explicit address list used to configure Gin's proxy trust.

const (
	// requestOriginKey carries the captured origin independently from optional
	// legacy client-IP compatibility, which can trust raw headers from any peer.
	requestOriginKey = "tokenrouter.request_origin"
)

// RequestOrigin creates middleware for the configured trusted proxy addresses.
// Invalid lists disable all forwarded-origin handling. Install this before
// frontend serving, and pass nil when proxy trust was not explicitly configured.
func RequestOrigin(trusted []string) gin.HandlerFunc {
	networks := make([]*net.IPNet, 0, len(trusted))
	for _, entry := range trusted {
		if ip := net.ParseIP(entry); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			networks = append(networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			// Match Gin's all-or-nothing trust configuration: a malformed entry must
			// not leave an earlier valid prefix able to supply forwarded origin headers.
			networks = nil
			break
		}
		networks = append(networks, network)
	}
	return func(c *gin.Context) {
		origin := directRequestOrigin(c.Request)
		peer, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		for _, network := range networks {
			if !network.Contains(net.ParseIP(peer)) {
				continue
			}
			parsed, _ := url.Parse(origin)
			if parsed == nil {
				break
			}
			proto := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0])
			if proto == "http" || proto == "https" {
				parsed.Scheme = proto
			}
			host := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Host"), ",")[0])
			if host != "" && validOriginHost(host) {
				parsed.Host = host
			}
			origin = parsed.String()
			break
		}
		c.Set(requestOriginKey, origin)
		c.Next()
	}
}

// GetRequestOrigin returns the captured origin or the direct request origin.
// The fallback never consumes forwarded headers, allowing isolated handlers and
// tests to operate safely without the router's origin-capturing middleware.
func GetRequestOrigin(c *gin.Context) string {
	if value := c.GetString(requestOriginKey); value != "" {
		return value
	}
	return directRequestOrigin(c.Request)
}

// directRequestOrigin derives an HTTP origin from TLS and a validated Host.
// Invalid host text returns an empty origin, preventing attribute injection and
// avoiding fabricated absolute links when the request has no usable authority.
func directRequestOrigin(r *http.Request) string {
	if !validOriginHost(r.Host) {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// validOriginHost rejects authorities containing credentials, paths, or invalid URL syntax.
func validOriginHost(host string) bool {
	parsed, err := url.Parse("http://" + host)
	return err == nil && parsed.Host != "" && parsed.Host == host && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && !strings.ContainsAny(host, "\\ \t\r\n")
}
