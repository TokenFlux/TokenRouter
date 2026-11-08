//go:build embed

package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
)

// social_metadata.go renders social cards from cached language settings and the
// current request address. Language HTML remains reusable across hosts and paths,
// while canonical and image URLs are resolved only after selecting that snapshot.
// Configured frontend addresses override request origins; absent configuration uses
// the origin captured by the server's explicit trusted-proxy policy.
//
// Response validators hash the final metadata before per-request nonce substitution.
// This binds each ETag to its actual sharing destination without putting arbitrary
// hosts or paths into the language cache. Text and URLs are escaped as attributes,
// and missing covers select the summary layout without advertising an empty image.

// socialSettings contains only the public fields needed to render social cards.
type socialSettings struct {
	SiteName     string `json:"site_name"`               // resolved site name
	SiteTitle    string `json:"site_title"`              // resolved homepage title
	SiteSubtitle string `json:"site_subtitle"`           // resolved homepage subtitle
	Title        string `json:"site_share_title"`        // resolved sharing title
	Description  string `json:"site_share_description"`  // resolved sharing description
	Image        string `json:"site_share_image_url"`    // digest-addressed cover path
	ImageType    string `json:"site_share_image_type"`   // validated cover MIME
	ImageWidth   int    `json:"site_share_image_width"`  // pixels
	ImageHeight  int    `json:"site_share_image_height"` // pixels
	FrontendURL  string `json:"frontend_url"`            // configured frontend address
}

// renderSocialMetadata completes one cached language page for the request URL.
// It returns rendered HTML and a nonce-independent validator. Invalid settings
// leave the cached content intact, while invalid origins omit absolute URL tags.
//
// Request-specific markup is never published back into the language cache. The
// final content digest includes both canonical and image destinations, preventing
// a validator from another host or path from suppressing the current response.
func renderSocialMetadata(c *gin.Context, snapshot CachedHTML) ([]byte, string) {
	var settings socialSettings
	content := snapshot.Content
	if json.Unmarshal(snapshot.SettingsJSON, &settings) == nil {
		base := strings.TrimSpace(settings.FrontendURL)
		if base == "" {
			base = middleware.GetRequestOrigin(c)
		}
		tags := settings.tags(base, c.Request.URL.EscapedPath())
		content = bytes.Replace(content, []byte("</head>"), []byte(tags+"</head>"), 1)
	}
	hash := sha256.Sum256(append([]byte(snapshot.ETag), content...))
	return content, `"` + hex.EncodeToString(hash[:]) + `"`
}

// tags formats one Open Graph and Twitter metadata block with escaped values.
// Blank copy inherits site branding; missing images select summary cards. Only
// HTTP(S) bases without credentials can produce canonical and image URL tags.
func (s socialSettings) tags(base, path string) string {
	title := strings.TrimSpace(s.Title)
	if title == "" {
		title = strings.TrimSpace(s.SiteTitle)
	}
	if title == "" {
		title = s.SiteName
	}
	description := strings.TrimSpace(s.Description)
	if description == "" {
		description = s.SiteSubtitle
	}
	var result strings.Builder
	// Attribute escaping is shared by text and URL fields. Administrators can
	// include quotes or markup in copy without creating additional tags or attributes.
	add := func(attribute, name, value string) {
		if value == "" {
			return
		}
		// strings.Builder writes remain in memory and always return a nil error;
		// the escaped tag is appended without a fallible external output operation.
		_, _ = result.WriteString(`<meta ` + attribute + `="` + name + `" content="` + html.EscapeString(value) + `" />` + "\n")
	}
	imageURL := ""
	canonical := ""
	parsed, err := url.Parse(base)
	if err == nil && parsed.Host != "" && parsed.User == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		reference, pathErr := url.Parse(path)
		if pathErr == nil && !reference.IsAbs() && reference.Host == "" {
			reference.RawQuery = ""
			reference.Fragment = ""
			canonical = parsed.ResolveReference(reference).String()
		}
		if strings.HasPrefix(s.Image, "/api/v1/settings/share-image/") {
			imageURL = parsed.ResolveReference(&url.URL{Path: s.Image}).String()
		}
	}
	card := "summary"
	if imageURL != "" {
		card = "summary_large_image"
	}
	add("name", "twitter:card", card)
	add("name", "twitter:title", title)
	add("name", "twitter:description", description)
	add("name", "twitter:image", imageURL)
	add("property", "og:type", "website")
	add("property", "og:title", title)
	add("property", "og:description", description)
	add("property", "og:site_name", s.SiteName)
	add("property", "og:url", canonical)
	add("property", "og:image", imageURL)
	if imageURL != "" {
		add("property", "og:image:type", s.ImageType)
		if s.ImageWidth > 0 {
			add("property", "og:image:width", strconv.Itoa(s.ImageWidth))
		}
		if s.ImageHeight > 0 {
			add("property", "og:image:height", strconv.Itoa(s.ImageHeight))
		}
	}
	return result.String()
}
