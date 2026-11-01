package httpapi

import (
	"context"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/http/httpguts"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// proxy.go matches immutable manifest routes before selecting panel authentication
// and forwarding bounded requests to one retained process generation. Header
// allowlists exclude browser credentials, while verified caller identity travels
// in a private RPC field with a short-lived capability grant. Plugin replies
// remain raw HTTP bodies after status, size, MIME, and header validation.
//
// Route selection and process acquisition can straddle a version change. The
// acquired version and current manifest digest must still match the selected
// authorization rule before execution begins. A mismatch returns a retryable
// restart error instead of applying an older public rule to a newer endpoint.
// The lease stays held until the RPC returns, including deadline failure.

const (
	// routeSelectionKey keeps the private matched declaration together across authentication, panel limiting, and final contribution forwarding handlers.
	routeSelectionKey = "tokenrouter_plugin_route"
	// maxResponseBody bounds buffered native contribution output before browser headers or body bytes become visible to the caller.
	maxResponseBody = 4 << 20
)

// selectedRoute retains one request's accepted declaration and canonical relative path.
type selectedRoute struct {
	loaded plugin.Loaded  // immutable accepted release snapshot
	route  manifest.Route // selected authorization and body policy
	path   string         // canonical plugin-relative request path
}

// Resolve selects the first matching declaration before authentication or plugin execution.
// Unknown, disabled, or undeclared endpoints return a not-found reason. Noncanonical
// paths are rejected so dot segments cannot change which authorization rule applies.
func (h *PublicHandler) Resolve(c *gin.Context) {
	requestPath := c.Param("path")
	if !canonicalProxyPath(requestPath) {
		httpx.ErrorFrom(c, plugin.ErrRouteNotFound)
		c.Abort()
		return
	}
	loaded, found := h.options.Runtime.Snapshot(c.Param("id"))
	if !found || loaded.Installation == nil || !loaded.Installation.Enabled {
		httpx.ErrorFrom(c, plugin.ErrPluginNotFound)
		c.Abort()
		return
	}
	declaration := loaded.Installation.Manifest
	if declaration != nil && declaration.Backend != nil {
		for _, route := range declaration.Backend.Routes {
			if route.Matches(c.Request.Method, requestPath) {
				c.Set(routeSelectionKey, selectedRoute{loaded: loaded, route: route, path: requestPath})
				c.Next()
				return
			}
		}
	}
	httpx.ErrorFrom(c, plugin.ErrRouteNotFound)
	c.Abort()
}

// Proxy forwards a validated request and emits a bounded raw plugin response.
// Existing route middleware has authenticated the selected audience. A caller
// grant is created only for the verified principal and never enters forwarded headers.
//
// Body admission precedes lease acquisition so a slow or oversized upload cannot
// occupy a plugin execution slot. After acquisition, both version and manifest
// identity are checked against the rule used for authentication. All response
// checks finish before copying any plugin-controlled headers to the browser;
// failure therefore cannot leave a partial successful response or set a cookie.
func (h *PublicHandler) Proxy(c *gin.Context) {
	selected, ok := selectedFrom(c)
	if !ok {
		httpx.ErrorFrom(c, plugin.ErrRouteNotFound)
		return
	}
	limit := int64(selected.route.BodyLimit())
	if c.Request.ContentLength > limit {
		httpx.Error(c, http.StatusRequestEntityTooLarge, "Request body exceeds the plugin route limit")
		return
	}
	// Read one extra byte to distinguish an exact-limit body from an oversized chunked request without trusting Content-Length.
	reader := c.Request.Body
	if reader == nil {
		reader = http.NoBody
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		httpx.BadRequest(c, "Could not read the plugin request body")
		return
	}
	if int64(len(body)) > limit {
		httpx.Error(c, http.StatusRequestEntityTooLarge, "Request body exceeds the plugin route limit")
		return
	}
	caller, err := h.caller(c, selected.loaded.Installation.PluginID)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	lease, err := h.options.Runtime.Acquire(c.Request.Context(), selected.loaded.Installation.PluginID)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	defer lease.Release()
	current, found := h.options.Runtime.Snapshot(selected.loaded.Installation.PluginID)
	if !found || current.Installation == nil || lease.Version != selected.loaded.Installation.Version ||
		current.Installation.ManifestSHA256 != selected.loaded.Installation.ManifestSHA256 {
		httpx.ErrorFrom(c, plugin.ErrPluginRestarting)
		return
	}
	if lease.Conn == nil || lease.Conn.HTTP == nil {
		httpx.ErrorFrom(c, plugin.ErrPluginRequestFailed)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(selected.route.Timeout())*time.Second)
	defer cancel()
	response, err := lease.Conn.HTTP.Exchange(ctx, &plugin.HTTPRequest{
		Method:   c.Request.Method,
		Path:     selected.path,
		RawQuery: c.Request.URL.RawQuery,
		Headers:  requestHeaders(c.Request.Header),
		Body:     body,
		Caller:   caller,
		ClientIP: h.options.ClientIP(c),
	})
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	headers, valid := responseHeaders(response)
	if !valid {
		httpx.ErrorFrom(c, plugin.ErrPluginRequestFailed)
		return
	}
	for name, value := range headers {
		c.Header(name, value)
	}
	// Mark only validated plugin replies so browser session recovery cannot mistake
	// an author's 401 for host authentication failure and replay a completed action.
	c.Header("X-TokenRouter-Plugin-Response", "1")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(response.Status, headers["Content-Type"], response.Body)
}

// caller projects a verified principal and signs its plugin-scoped capability ticket.
// Anonymous public requests return nil. Username comes from the current account
// reader, while identity and role remain those established by panel authentication.
// Locale uses the shared language reader's negotiated value instead of reparsing
// the client's raw Accept-Language preferences inside the plugin boundary.
func (h *PublicHandler) caller(c *gin.Context, id string) (*plugin.HTTPCaller, error) {
	principal, found := authctx.GetPrincipal(c)
	if !found || principal.UserID <= 0 {
		return nil, nil
	}
	profile, err := h.options.Profiles.Account(c.Request.Context(), principal.UserID)
	if err != nil {
		return nil, err
	}
	ticket, err := h.options.Grants.Issue(id, principal.UserID)
	if err != nil {
		return nil, err
	}
	return &plugin.HTTPCaller{
		UserID:   principal.UserID,
		Username: profile.User.Username,
		Role:     principal.Role,
		Locale:   h.options.Language(c),
		Grant:    ticket,
	}, nil
}

// selectedFrom retrieves only the private typed route selected before authentication.
func selectedFrom(c *gin.Context) (selectedRoute, bool) {
	value, exists := c.Get(routeSelectionKey)
	selected, ok := value.(selectedRoute)
	return selected, exists && ok
}

// canonicalProxyPath accepts one decoded absolute path without traversal or redundant separators.
// A single trailing slash remains meaningful for prefix routes. Percent characters
// remain literal after Gin's path decoding and are not decoded a second time.
func canonicalProxyPath(value string) bool {
	if !strings.HasPrefix(value, "/") || !utf8.ValidString(value) || strings.Contains(value, "\\") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	clean := path.Clean(value)
	if clean != "/" && strings.HasSuffix(value, "/") {
		clean += "/"
	}
	return clean == value
}

// requestHeaders copies allowed noncredential headers into the contribution request.
// Repeated values join with commas. Invalid names or values are discarded even
// when their spelling otherwise matches the plugin-owned extension prefix.
func requestHeaders(input http.Header) map[string]string {
	result := make(map[string]string)
	for name, values := range input {
		canonical := http.CanonicalHeaderKey(name)
		allowed := strings.HasPrefix(strings.ToLower(canonical), "x-plugin-")
		switch canonical {
		case "Accept", "Accept-Language", "Content-Type", "If-None-Match", "If-Modified-Since", "User-Agent", "X-Request-Id":
			allowed = true
		}
		value := strings.Join(values, ", ")
		if allowed && utf8.ValidString(value) && httpguts.ValidHeaderFieldName(canonical) && httpguts.ValidHeaderFieldValue(value) {
			result[canonical] = value
		}
	}
	return result
}

// responseHeaders validates the whole buffered reply before exposing any header or body.
// Only explicit passive MIME types are accepted, including valid parameters.
// Duplicate case-insensitive allowed headers fail deterministically instead of depending
// on map iteration order; forbidden or malformed extension headers are discarded.
func responseHeaders(response *plugin.HTTPResponse) (map[string]string, bool) {
	if response == nil || response.Status < 200 || response.Status > 599 || len(response.Body) > maxResponseBody {
		return nil, false
	}
	result := make(map[string]string)
	for name, value := range response.Headers {
		canonical := http.CanonicalHeaderKey(name)
		allowed := strings.HasPrefix(strings.ToLower(canonical), "x-plugin-")
		switch canonical {
		case "Content-Type", "Cache-Control", "Etag", "Last-Modified", "Content-Disposition", "Retry-After":
			allowed = true
		}
		if !allowed || !httpguts.ValidHeaderFieldName(canonical) || !httpguts.ValidHeaderFieldValue(value) {
			continue
		}
		if _, duplicate := result[canonical]; duplicate {
			return nil, false
		}
		result[canonical] = value
	}
	media, _, err := mime.ParseMediaType(result["Content-Type"])
	if err != nil {
		return nil, false
	}
	switch strings.ToLower(media) {
	case "application/json", "text/plain", "text/csv", "image/png", "image/jpeg", "image/webp", "image/gif", "application/octet-stream":
		return result, true
	default:
		return nil, false
	}
}
