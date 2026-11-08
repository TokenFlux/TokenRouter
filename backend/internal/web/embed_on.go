//go:build embed

package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	htmlpkg "html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
)

// embed_on.go serves embedded frontend assets and injects public settings into
// language-specific HTML pages.
// The embedded SPA shares HTTP entry paths with gateway APIs, including the
// browser model marketplace at /models. Request negotiation checks methods,
// credential hints, and Accept preferences before the frontend serves a page.
// Embedded asset existence controls local override checks, while other non-API
// paths resolve to the SPA entry.
//
// Injected HTML is cached by language with a reusable nonce placeholder, and
// rendered 200 responses substitute the nonce supplied by SecurityHeaders. Settings
// reads and rendering can overlap invalidation, so HTMLCache compares the
// observed generation before storing a rendered page. A rejected stale
// publication can still be served to its initiating request. The separate
// ServeEmbeddedFrontend entry serves the bundle directly when settings
// injection is unnecessary.

// NonceHTMLPlaceholder marks reusable script attributes in cached HTML so
// request-specific nonce values can be substituted after the settings render is
// retrieved or published.
const (
	// NonceHTMLPlaceholder identifies the request nonce position in rendered script tags.
	NonceHTMLPlaceholder = "__CSP_NONCE_VALUE__"
)

var (
	// frontendFS includes the complete dist tree in embed builds, including hidden
	// files selected by the all prefix used by this embedded filesystem directive.
	//go:embed all:dist
	frontendFS embed.FS

	// htmlLanguagePattern matches the bundle's opening html spelling and optional
	// lang attribute so injection can set the selected language and text direction.
	htmlLanguagePattern = regexp.MustCompile(`<html(?:\s+lang="[^"]*")?`)
)

// PublicSettingsProvider supplies locale-aware public configuration used while
// rendering the initial HTML page.
type PublicSettingsProvider interface {
	// GetPublicSettingsForInjection returns JSON-compatible public settings for
	// the context language. Keep returned data stable while the consumer encodes
	// it, and observe ctx cancellation for the HTML request's query budget; an
	// error selects the base page.
	GetPublicSettingsForInjection(ctx context.Context) (any, error)
}

// FrontendServer serves one embedded bundle with locale-specific settings pages
// and optional disk asset overrides.
type FrontendServer struct {
	// distFS, fileServer, and baseHTML come from one embedded bundle at construction.
	// The filesystem serves assets, while the retained entry bytes supply settings
	// rendering and read-failure fallback pages throughout this server's lifetime.
	// Keep baseHTML immutable while serving requests that read or brand its bytes.
	distFS     fs.FS        // embedded bundle filesystem view
	fileServer http.Handler // embedded static asset handler
	baseHTML   []byte       // retained base entry bytes
	// cache holds language pages and their invalidation generation. Settings change
	// notifications advance that generation before later renders can be stored.
	cache *HTMLCache
	// settings supplies cache-miss public data in the request context's language.
	// The provider must remain callable while this server handles injected page requests.
	settings PublicSettingsProvider
	// overrideDir selects disk copies for paths already present in the bundle.
	// Empty disables those checks; local override responses skip immutable asset caching.
	overrideDir string
}

// NewFrontendServer reads the embedded dist entry and prepares file serving
// plus an empty language cache. Supply a callable settingsProvider before using
// injection middleware, because construction retains it as supplied and cache
// misses invoke it. The opened entry is closed with its error discarded.
func NewFrontendServer(settingsProvider PublicSettingsProvider) (*FrontendServer, error) {
	distFS, err := fs.Sub(frontendFS, "dist")
	if err != nil {
		return nil, err
	}

	file, err := distFS.Open("index.html")
	if err != nil {
		return nil, err
	}
	// Close the opened bundle entry after reading it, including on read failure.
	// Close errors are discarded because the entry read supplies this operation's result.
	defer func() { _ = file.Close() }()

	baseHTML, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	cache := NewHTMLCache()
	cache.SetBaseHTML(baseHTML)

	return &FrontendServer{
		distFS:      distFS,
		fileServer:  http.FileServer(http.FS(distFS)),
		baseHTML:    baseHTML,
		cache:       cache,
		settings:    settingsProvider,
		overrideDir: filepath.Join("data", "public"),
	}, nil
}

// InvalidateCache clears all cached language pages and advances their
// publication generation through HTMLCache. Call it after public settings
// change; nil receivers and nil caches accept this notification without further
// work.
func (s *FrontendServer) InvalidateCache() {
	if s != nil && s.cache != nil {
		s.cache.Invalidate()
	}
}

// Middleware serves injected SPA pages and embedded assets while passing
// reserved API requests onward. Use a server created by NewFrontendServer and
// install the nonce and locale middleware before this handler when those values
// are needed.
//
// Index requests and paths that cannot be opened in the bundle render the SPA
// entry first. For other existing bundle paths, an accepted disk override is
// served before immutable-cache headers are considered. Remaining assets use
// the embedded file server and abort Gin processing. /models receives Vary:
// Accept before either API forwarding or browser-page rendering so negotiation
// remains visible to caches.
func (s *FrontendServer) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		addEmbeddedFrontendNegotiationHeaders(c)

		if shouldBypassEmbeddedFrontend(c.Request) {
			c.Next()
			return
		}

		cleanPath := strings.TrimPrefix(path, "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}

		// Unopenable bundle paths can be client-side SPA routes. Render the entry
		// before checking disk overrides so those routes receive settings and language injection.
		if cleanPath == "index.html" || !s.fileExists(cleanPath) {
			s.serveIndexHTML(c)
			return
		}

		if tryServeOverrideFile(c, s.overrideDir, cleanPath) {
			return
		}

		// Disk overrides can change while keeping the same filename. Apply immutable
		// caching after their serving branch so that policy describes the embedded fingerprinted asset.
		applyStaticAssetCacheHeaders(c.Writer.Header(), cleanPath)
		s.fileServer.ServeHTTP(c.Writer, c.Request)
		c.Abort()
	}
}

// fileExists reports whether the bundle can open path, including a directory
// entry. Every open error becomes false, and a successful probe closes its
// handle with the close error discarded before later serving.
func (s *FrontendServer) fileExists(path string) bool {
	file, err := s.distFS.Open(path)
	if err != nil {
		return false
	}
	// Existence probing owns this handle until the later HTTP server opens its
	// own serving handle. Discard close errors while retaining the successful open observation.
	_ = file.Close()
	return true
}

// serveIndexHTML renders or reuses the language page and ends Gin processing
// with its response. Language comes from the request context, and
// SecurityHeaders supplies the nonce substituted into the reusable rendered
// script.
//
// A page accepts an exact final If-None-Match value as a 304 condition.
// Metadata is completed before validation, and both 200 and 304 responses emit
// the final ETag and no-cache. A cache miss
// supplies a two-second request-derived context to the settings provider and
// returns the raw base page on read or JSON encoding failure.
//
// Rendering retains the nonce placeholder and adjusts the opening html language
// before Publish checks the observed generation. A concurrent invalidation can
// prevent storage while this request still returns its rendered page. ETags
// include the completed social metadata, template, language, and settings bytes,
// and exclude the substituted per-request nonce.
func (s *FrontendServer) serveIndexHTML(c *gin.Context) {
	// Retrieve this request's trusted nonce before rendering. Cached pages keep
	// the placeholder so response substitution uses this value rather than a prior request's nonce.
	nonce := middleware.GetNonceFromContext(c)

	language := locale.FromContext(c.Request.Context())
	c.Writer.Header().Add("Vary", "Accept-Language, Cookie")
	c.Header("Content-Language", language)
	cached, version := s.cache.Snapshot(language)
	if cached != nil {
		s.writeIndexHTML(c, *cached, nonce)
		return
	}

	// Supply a short request-derived query budget to the provider. It must honor
	// that context for cancellation, while its failure path still leaves the base page available.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	settings, err := s.settings.GetPublicSettingsForInjection(ctx)
	if err != nil {
		// Failed public-data lookup leaves the raw embedded entry usable for browser
		// startup. This branch skips injected settings, cache publication, and response nonce substitution.
		c.Data(http.StatusOK, "text/html; charset=utf-8", s.baseHTML)
		c.Abort()
		return
	}

	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		// Unencodable settings cannot form the inline configuration script. Return raw
		// entry bytes so browser startup still receives a page, without caching this failed rendering.
		c.Data(http.StatusOK, "text/html; charset=utf-8", s.baseHTML)
		c.Abort()
		return
	}

	rendered := s.injectSettings(settingsJSON)
	rendered = htmlLanguagePattern.ReplaceAll(rendered, []byte(`<html lang="`+language+`" dir="`+locale.Direction(language)+`"`))
	// Invalidation may have advanced after Snapshot and the provider read. Publish
	// can reject cache storage while this initiating request still receives its completed render.
	snapshot := s.cache.Publish(language, version, rendered, settingsJSON)

	s.writeIndexHTML(c, snapshot, nonce)
}

// writeIndexHTML completes request-specific metadata before conditional response handling.
// The validator excludes the CSP nonce, but includes the destination URL and
// language snapshot. Both successful and not-modified responses carry cache headers.
func (s *FrontendServer) writeIndexHTML(c *gin.Context, snapshot CachedHTML, nonce string) {
	content, etag := renderSocialMetadata(c, snapshot)
	c.Header("ETag", etag)
	c.Header("Cache-Control", "no-cache")
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		c.Abort()
		return
	}
	// Keep nonce replacement after metadata hashing so random per-request CSP
	// values do not prevent conditional requests from reusing otherwise identical HTML.
	content = replaceNoncePlaceholder(content, nonce)
	c.Data(http.StatusOK, "text/html; charset=utf-8", content)
	c.Abort()
}

// injectSettings builds reusable HTML containing the supplied inline settings
// JSON and a nonce placeholder. settingsJSON must already be suitable for
// embedding in a script; serveIndexHTML supplies json.Marshal output, while
// this helper inserts the supplied bytes directly.
//
// The first literal closing head tag selects script placement. If it is absent,
// the page still passes through title and favicon branding even though the
// settings script was not inserted. The cached placeholder stays available for
// replacement at response time, allowing one settings render to serve requests
// with different nonces.
func (s *FrontendServer) injectSettings(settingsJSON []byte) []byte {
	// Keep nonce positions unresolved in the reusable settings render. The serving
	// path substitutes a trusted request nonce after lookup or publication of this HTML.

	script := []byte(`<script nonce="` + NonceHTMLPlaceholder + `">window.__APP_CONFIG__=` + string(settingsJSON) + `;</script>`)

	headClose := []byte("</head>")
	result := bytes.Replace(s.baseHTML, headClose, append(script, headClose...), 1)

	// Update title and icon alongside configuration insertion so initial markup
	// uses the public branding before application JavaScript reads window.__APP_CONFIG__.
	result = injectSiteTitle(result, settingsJSON)
	result = injectSiteFavicon(result, settingsJSON)

	return result
}

// injectSiteFavicon replaces the first literal icon link with a configured logo
// accepted by safeImageURL. Invalid JSON, empty or rejected logo text, or
// missing link delimiters return the supplied HTML unchanged.
//
// The replacement escapes the logo URL as an HTML attribute and replaces the
// complete original link element. Its other attributes are dropped, while
// prefix and suffix bytes around that element remain available in the new
// output. The caller supplies bundle markup using the recognized link spelling;
// this is a byte-pattern transformation rather than general HTML parsing.
func injectSiteFavicon(html, settingsJSON []byte) []byte {
	var cfg struct {
		SiteLogo string `json:"site_logo"` // configured favicon URL text
	}
	// Malformed branding data leaves the existing icon available. The helper
	// returns supplied HTML so a settings decode failure cannot remove its bundle icon.
	if err := json.Unmarshal(settingsJSON, &cfg); err != nil {
		return html
	}

	logoURL := safeImageURL(cfg.SiteLogo)
	if logoURL == "" {
		return html
	}

	linkStart := bytes.Index(html, []byte(`<link rel="icon"`))
	if linkStart == -1 {
		return html
	}
	linkEndOffset := bytes.IndexByte(html[linkStart:], '>')
	if linkEndOffset == -1 {
		return html
	}
	linkEnd := linkStart + linkEndOffset + 1
	// Escape the selected URL for its quoted HTML attribute before replacing the
	// complete link. Surrounding page bytes stay on either side of this reconstructed element.
	replacement := []byte(`<link rel="icon" href="` + htmlpkg.EscapeString(logoURL) + `" />`)

	// bytes.Buffer.Write returns a nil error for these in-memory copies. Compose
	// the replacement and retained page segments while discarding those unused write results.
	var buf bytes.Buffer
	_, _ = buf.Write(html[:linkStart])
	_, _ = buf.Write(replacement)
	_, _ = buf.Write(html[linkEnd:])
	return buf.Bytes()
}

// safeImageURL returns trimmed logo text accepted for favicon injection, or
// empty text when rejected. A single-slash path, case-insensitive data:image/
// prefix, or parsed HTTP(S) URL with a host is accepted; image content and URL
// userinfo are not inspected.
func safeImageURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "/") && !strings.HasPrefix(trimmed, "//") {
		return trimmed
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "data:image/") {
		return trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	return trimmed
}

// injectSiteTitle replaces the first attribute-free title element with the
// configured site name and fixed gateway suffix. Empty names, invalid settings
// JSON, or missing or misordered title delimiters return the supplied HTML
// unchanged.
//
// The configured name is escaped as HTML text before the replacement is
// assembled. Prefix and suffix bytes surrounding the selected title remain in
// the output, so other page elements continue to use their supplied markup.
// Titles with attributes do not match the opening delimiter; the caller uses
// the bundle's known title spelling.
func injectSiteTitle(html, settingsJSON []byte) []byte {
	var cfg struct {
		SiteName string `json:"site_name"` // configured page title text
	}
	// Missing usable name data leaves the bundle title available. Treat a settings
	// decode failure as unchanged branding before inspecting the known title delimiters.
	if err := json.Unmarshal(settingsJSON, &cfg); err != nil || cfg.SiteName == "" {
		return html
	}

	titleStart := bytes.Index(html, []byte("<title>"))
	titleEnd := bytes.Index(html, []byte("</title>"))
	if titleStart == -1 || titleEnd == -1 || titleEnd <= titleStart {
		return html
	}

	// The setting is text inside title, so escape it before rebuilding that element.
	// The prefix and suffix writes retain the remaining markup around the selected title.
	newTitle := []byte("<title>" + htmlpkg.EscapeString(cfg.SiteName) + " - AI API Gateway</title>")
	// bytes.Buffer.Write returns a nil error for these in-memory copies. Compose
	// the replacement and retained page segments while discarding those unused write results.
	var buf bytes.Buffer
	_, _ = buf.Write(html[:titleStart])
	_, _ = buf.Write(newTitle)
	_, _ = buf.Write(html[titleEnd+len("</title>"):])
	return buf.Bytes()
}

// replaceNoncePlaceholder substitutes every HTML placeholder with the supplied
// nonce bytes. An empty nonce empties those positions, and callers supply the
// trusted value from SecurityHeaders because this byte replacement performs no
// HTML escaping.
func replaceNoncePlaceholder(html []byte, nonce string) []byte {
	return bytes.ReplaceAll(html, []byte(NonceHTMLPlaceholder), []byte(nonce))
}

// ServeEmbeddedFrontend constructs direct bundle middleware with SPA fallback
// and optional existing-asset overrides. Failure to obtain the embedded dist
// filesystem panics during construction, and the returned handler owns file
// probes while the HTTP file server owns its serving handles.
//
// Reserved APIs are forwarded before file lookup. Existing bundle paths can
// select a disk override, then receive fingerprint-based caching when the
// embedded copy is served; absent paths read the raw entry page. This entry
// does not inject settings or per-response nonces. /models uses the shared
// negotiation rule and appends Vary: Accept in either route outcome.
func ServeEmbeddedFrontend() gin.HandlerFunc {
	distFS, err := fs.Sub(frontendFS, "dist")
	if err != nil {
		panic("failed to get dist subdirectory: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(distFS))
	overrideDir := filepath.Join("data", "public")

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		addEmbeddedFrontendNegotiationHeaders(c)

		if shouldBypassEmbeddedFrontend(c.Request) {
			c.Next()
			return
		}

		cleanPath := strings.TrimPrefix(path, "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}

		if file, err := distFS.Open(cleanPath); err == nil {
			// The open was an existence probe. Close its handle before the override
			// or embedded HTTP serving path obtains the handle needed for the actual response.
			_ = file.Close()

			if tryServeOverrideFile(c, overrideDir, cleanPath) {
				return
			}
			applyStaticAssetCacheHeaders(c.Writer.Header(), cleanPath)
			fileServer.ServeHTTP(c.Writer, c.Request)
			c.Abort()
			return
		}

		// A missing bundle path can name a client-side route. Serve the raw entry
		// page so the SPA router can interpret that path after browser startup.
		serveIndexHTML(c, distFS)
	}
}

// addEmbeddedFrontendNegotiationHeaders appends Vary: Accept when the trimmed
// request path is /models.
func addEmbeddedFrontendNegotiationHeaders(c *gin.Context) {
	if strings.TrimSpace(c.Request.URL.Path) == "/models" {
		c.Writer.Header().Add("Vary", "Accept")
	}
}

// tryServeOverrideFile serves a nondirectory disk override and aborts Gin
// processing after writing the response. Callers check bundle existence first
// and apply embedded cache headers after a false result. An empty overrideDir,
// stat failure, or directory-valued candidate returns false.
func tryServeOverrideFile(c *gin.Context, overrideDir, cleanPath string) bool {
	if overrideDir == "" {
		return false
	}
	filePath := filepath.Join(overrideDir, filepath.Clean("/"+cleanPath))
	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		return false
	}
	c.File(filePath)
	c.Abort()
	return true
}

// shouldBypassEmbeddedFrontend reports whether a request path should continue
// to registered API handlers. Nil request or URL returns false; other paths are
// trimmed before reserved prefix and exact-path comparisons.
//
// The shared /models path first permits browser marketplace navigation through
// shouldServeModelMarketplace. Other /models requests bypass the frontend. API,
// versioned, backend, setup, Antigravity, response, image, and video path
// families are forwarded, along with MCP, health, and AlphaSearch entry paths.
// Unrecognized paths stay available for the frontend's SPA fallback.
func shouldBypassEmbeddedFrontend(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}

	trimmed := strings.TrimSpace(request.URL.Path)
	// The model marketplace page and Codex model API share /models.
	if trimmed == "/models" && shouldServeModelMarketplace(request) {
		return false
	}

	return strings.HasPrefix(trimmed, "/api/") ||
		strings.HasPrefix(trimmed, "/.well-known/oauth-") ||
		trimmed == "/mcp" ||
		strings.HasPrefix(trimmed, "/mcp/") ||
		strings.HasPrefix(trimmed, "/v1/") ||
		strings.HasPrefix(trimmed, "/v1beta/") ||
		strings.HasPrefix(trimmed, "/backend-api/") ||
		strings.HasPrefix(trimmed, "/antigravity/") ||
		strings.HasPrefix(trimmed, "/setup/") ||
		trimmed == "/health" ||
		trimmed == "/models" ||
		trimmed == "/responses" ||
		strings.HasPrefix(trimmed, "/responses/") ||
		trimmed == "/alpha/search" ||
		strings.HasPrefix(trimmed, "/images/") ||
		strings.HasPrefix(trimmed, "/videos/")
}

// shouldServeModelMarketplace reports whether /models can be rendered as
// browser navigation. The caller supplies a nonnil request and URL; GET and
// HEAD are eligible, while nonblank version or API-key query hints and
// credential headers select API handling.
//
// Eligible requests then use requestPrefersHTML to compare accepted media
// types. This ordering keeps a credential-bearing model query on the API path
// even when its client also advertises HTML. Blank hints are treated as absent,
// and the helper leaves path matching to shouldBypassEmbeddedFrontend.
func shouldServeModelMarketplace(request *http.Request) bool {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return false
	}

	query := request.URL.Query()
	if strings.TrimSpace(query.Get("client_version")) != "" ||
		strings.TrimSpace(query.Get("key")) != "" ||
		strings.TrimSpace(query.Get("api_key")) != "" {
		return false
	}
	for _, header := range []string{"Authorization", "x-api-key", "x-goog-api-key"} {
		if strings.TrimSpace(request.Header.Get(header)) != "" {
			return false
		}
	}

	return requestPrefersHTML(request.Header.Get("Accept"))
}

// requestPrefersHTML reports whether a positive HTML preference wins over JSON
// in Accept text. It recognizes text/html and application/xhtml+xml as HTML,
// and application/json or types ending in +json as JSON; other media entries do
// not enter the comparison.
//
// Accepted entries default q to one. Parse failures or out-of-range numeric
// qualities are skipped, and each class retains its greatest quality with the
// earliest occurrence at that quality. HTML wins when JSON is absent or has
// lower quality; equal quality uses that retained declaration order. Missing or
// zero-quality HTML leaves API handling selected.
func requestPrefersHTML(acceptHeader string) bool {
	parts := strings.Split(acceptHeader, ",")
	htmlQuality, jsonQuality := -1.0, -1.0
	htmlOrder, jsonOrder := len(parts), len(parts)

	for order, part := range parts {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			// An invalid media entry supplies no usable quality observation. Continue
			// examining other entries so a malformed item does not erase a valid preference.
			continue
		}

		quality := 1.0
		if rawQuality := strings.TrimSpace(params["q"]); rawQuality != "" {
			quality, err = strconv.ParseFloat(rawQuality, 64)
			// Rejected quality text leaves other media observations available. A zero
			// quality is retained but cannot establish a positive HTML navigation preference.
			if err != nil || quality < 0 || quality > 1 {
				continue
			}
		}

		// Replace a class winner only for greater quality. Keeping equal-quality
		// entries in their first observed positions lets cross-class ties follow declaration order.
		switch {
		case mediaType == "text/html" || mediaType == "application/xhtml+xml":
			if quality > htmlQuality {
				htmlQuality = quality
				htmlOrder = order
			}
		case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
			if quality > jsonQuality {
				jsonQuality = quality
				jsonOrder = order
			}
		}
	}

	if htmlQuality <= 0 {
		return false
	}
	if jsonQuality < 0 || htmlQuality > jsonQuality {
		return true
	}
	if htmlQuality < jsonQuality {
		return false
	}
	return htmlOrder < jsonOrder
}

// serveIndexHTML reads the raw bundle entry and ends Gin handling with HTML.
// Missing or unreadable entry files produce HTTP 404 or 500 respectively, and
// an opened file is closed with its cleanup error discarded.
func serveIndexHTML(c *gin.Context, fsys fs.FS) {
	file, err := fsys.Open("index.html")
	if err != nil {
		c.String(http.StatusNotFound, "Frontend not found")
		c.Abort()
		return
	}
	// Close the opened bundle entry after reading it, including on read failure.
	// Close errors are discarded because the entry read supplies this operation's result.
	defer func() { _ = file.Close() }()

	content, err := io.ReadAll(file)
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to read index.html")
		c.Abort()
		return
	}

	c.Data(http.StatusOK, "text/html; charset=utf-8", content)
	c.Abort()
}

// HasEmbeddedFrontend reports whether the compiled filesystem can read its
// dist/index.html entry. Read failures produce false, allowing application
// construction to choose whether frontend middleware is available for this
// bundle.
func HasEmbeddedFrontend() bool {
	_, err := frontendFS.ReadFile("dist/index.html")
	return err == nil
}
