// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

import (
	"fmt"
	"net/textproto"
	"regexp"
	"strings"
)

var (
	cfRayPattern  = regexp.MustCompile(`(?i)cf-ray[:\s=]+([a-z0-9-]+)`)
	cRayPattern   = regexp.MustCompile(`(?i)cRay:\s*'([a-z0-9-]+)'`)
	htmlChallenge = []string{
		"window._cf_chl_opt",
		"just a moment",
		"enable javascript and cookies to continue",
		"__cf_chl_",
		"challenge-platform",
	}
)

// IsCloudflareChallengeResponse reports whether the upstream response matches Cloudflare challenge behavior.
func IsCloudflareChallengeResponse(statusCode int, headers map[string][]string, body []byte) bool {
	if statusCode != 403 && statusCode != 429 {
		return false
	}

	if headers != nil && strings.EqualFold(strings.TrimSpace(responseHeaderValue(headers, "cf-mitigated")), "challenge") {
		return true
	}

	preview := strings.ToLower(TruncateBody(body, 4096))
	for _, marker := range htmlChallenge {
		if strings.Contains(preview, marker) {
			return true
		}
	}

	contentType := ""
	if headers != nil {
		contentType = strings.ToLower(strings.TrimSpace(responseHeaderValue(headers, "content-type")))
	}
	if strings.Contains(contentType, "text/html") &&
		(strings.Contains(preview, "<html") || strings.Contains(preview, "<!doctype html")) &&
		(strings.Contains(preview, "cloudflare") || strings.Contains(preview, "challenge")) {
		return true
	}

	return false
}

// ExtractCloudflareRayID extracts cf-ray from headers or response body.
func ExtractCloudflareRayID(headers map[string][]string, body []byte) string {
	if headers != nil {
		rayID := strings.TrimSpace(responseHeaderValue(headers, "cf-ray"))
		if rayID != "" {
			return rayID
		}
		rayID = strings.TrimSpace(responseHeaderValue(headers, "Cf-Ray"))
		if rayID != "" {
			return rayID
		}
	}

	preview := TruncateBody(body, 8192)
	if matches := cfRayPattern.FindStringSubmatch(preview); len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	if matches := cRayPattern.FindStringSubmatch(preview); len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// FormatCloudflareChallengeMessage appends cf-ray info when available.
func FormatCloudflareChallengeMessage(base string, headers map[string][]string, body []byte) string {
	rayID := ExtractCloudflareRayID(headers, body)
	if rayID == "" {
		return base
	}
	return fmt.Sprintf("%s (cf-ray: %s)", base, rayID)
}

// TruncateBody truncates body text for logging/inspection.
func TruncateBody(body []byte, max int) string {
	if max <= 0 {
		max = 512
	}
	raw := strings.TrimSpace(string(body))
	if len(raw) <= max {
		return raw
	}
	return raw[:max] + "...(truncated)"
}
func responseHeaderValue(headers map[string][]string, key string) string {
	values := headers[textproto.CanonicalMIMEHeaderKey(key)]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
