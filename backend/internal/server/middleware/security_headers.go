package middleware

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/server/httpconfig"
)

// security_headers.go sets browser security headers and prepares nonce-bearing
// content policies.
// Placeholder-based browser policies use a request nonce in both the CSP header
// and the HTML rendered by the frontend server. Configured policies are
// supplemented with the sources the application's CAPTCHA, identity, payment,
// analytics, and loopback-import integrations are granted. A fixed policy is
// prepared when middleware is constructed, while optional frame origins are
// read per request.
//
// Baseline response headers apply before the selected gateway-path exclusions,
// which skip nonce generation and CSP assignment. Other paths receive nonce
// replacement when CSP is enabled. Policy editing uses textual directive
// insertion, so callers provide canonical CSP source tokens and policy syntax.
// If the entropy reader fails, crypto/rand.Read terminates the process before
// the middleware can apply its fallback policy.

const (
	// Nonce identifiers and integration source tokens connect request context,
	// frontend rendering, and the CSP directives supplemented by this middleware.
	// CSPNonceKey names the Gin entry used to share the request nonce with
	// frontend rendering.
	CSPNonceKey = "csp_nonce"
	// NonceTemplate marks positions replaced by the current request's nonce in CSP
	// policy text.
	NonceTemplate = "__CSP_NONCE__"
	// CloudflareInsightsDomain is the analytics script source granted by runtime
	// CSP supplementation.
	CloudflareInsightsDomain = "https://static.cloudflareinsights.com"
	// GoogleIdentityScriptURL is the identity script source granted by runtime CSP
	// supplementation.
	GoogleIdentityScriptURL = "https://accounts.google.com/gsi/client"
	// GoogleIdentityResourceURL is the identity source granted to frame and
	// connection directives.
	GoogleIdentityResourceURL = "https://accounts.google.com/gsi/"
	// GoogleIdentityStyleURL is the identity stylesheet source granted by runtime
	// CSP supplementation.
	GoogleIdentityStyleURL = "https://accounts.google.com/gsi/style"
	// TencentCaptchaDomain is the CAPTCHA source granted to script, frame, and
	// connection directives.
	TencentCaptchaDomain = "https://turing.captcha.qcloud.com"
	// TencentCaptchaStaticDomain is the wildcard CAPTCHA stylesheet source granted
	// by policy supplementation.
	TencentCaptchaStaticDomain = "https://*.captcha.gtimg.com"
	// TencentCaptchaCDNDomain is a CAPTCHA script source granted by runtime CSP
	// supplementation.
	TencentCaptchaCDNDomain = "https://turing.captcha.gtimg.com"
	// TencentCaptchaGlobalDomain is the global CAPTCHA source granted to script
	// and frame directives.
	TencentCaptchaGlobalDomain = "https://ca.turing.captcha.qcloud.com"
	// TencentCaptchaGlobalCDNDomain is a global CAPTCHA script source granted by
	// policy supplementation.
	TencentCaptchaGlobalCDNDomain = "https://global.turing.captcha.gtimg.com"
	// TencentCaptchaPrehandleDomain is the CAPTCHA source granted to script,
	// connection, and frame directives.
	TencentCaptchaPrehandleDomain = "https://www.tycaptcha.com"
	// TencentCaptchaJQueryDomain is a CAPTCHA dependency script source granted by
	// policy supplementation.
	TencentCaptchaJQueryDomain = "https://cloudcache.tencentcs.com"
	// TencentCaptchaRceDomain is the CAPTCHA risk-check connection source granted
	// by policy supplementation.
	TencentCaptchaRceDomain = "https://rce.tencentrio.com"
	// TencentCaptchaWorkerSource grants the blob scheme to the supplemented
	// CAPTCHA worker directive.
	TencentCaptchaWorkerSource = "blob:"
	// AliyunCaptchaStaticDomain is the wildcard CAPTCHA source granted to script
	// and style directives.
	AliyunCaptchaStaticDomain = "https://*.alicdn.com"
	// StripeDomain is the wildcard Stripe source granted to script and frame
	// directives.
	StripeDomain = "https://*.stripe.com"
	// AirwallexStaticDomain is the production payment SDK source granted to script
	// and style directives.
	AirwallexStaticDomain = "https://static.airwallex.com"
	// AirwallexCheckoutDomain is the production checkout source granted to script,
	// style, and frame directives.
	AirwallexCheckoutDomain = "https://checkout.airwallex.com"
	// AirwallexDemoStaticDomain is the sandbox payment SDK source granted to
	// script and style directives.
	AirwallexDemoStaticDomain = "https://static-demo.airwallex.com"
	// AirwallexDemoCheckoutDomain is the sandbox checkout source granted to
	// script, style, and frame directives.
	AirwallexDemoCheckoutDomain = "https://checkout-demo.airwallex.com"
)

// requiredCSPDirectiveValues lists the directive/source pairs granted by
// runtime policy supplementation for application frontend integrations.
var requiredCSPDirectiveValues = []struct {
	// directive and value identify one required source permission together.
	// enhanceCSPPolicy checks the pair against the first matching directive before
	// inserting missing values into that policy.
	directive string // exact CSP directive name
	value     string // required source token
}{
	{"script-src", CloudflareInsightsDomain},
	{"script-src", GoogleIdentityScriptURL},
	{"frame-src", GoogleIdentityResourceURL},
	{"connect-src", GoogleIdentityResourceURL},
	{"style-src", GoogleIdentityStyleURL},
	{"script-src", TencentCaptchaDomain},
	{"frame-src", TencentCaptchaDomain},
	{"style-src", TencentCaptchaStaticDomain},
	{"script-src", TencentCaptchaCDNDomain},
	{"script-src", TencentCaptchaGlobalDomain},
	{"script-src", TencentCaptchaGlobalCDNDomain},
	{"script-src", TencentCaptchaPrehandleDomain},
	{"script-src", TencentCaptchaJQueryDomain},
	{"connect-src", TencentCaptchaDomain},
	{"connect-src", TencentCaptchaPrehandleDomain},
	{"connect-src", TencentCaptchaRceDomain},
	{"frame-src", TencentCaptchaGlobalDomain},
	{"frame-src", TencentCaptchaPrehandleDomain},
	{"worker-src", TencentCaptchaWorkerSource},
	{"script-src", AliyunCaptchaStaticDomain},
	{"style-src", AliyunCaptchaStaticDomain},
	{"script-src", StripeDomain},
	{"frame-src", StripeDomain},
	{"script-src", AirwallexStaticDomain},
	{"script-src", AirwallexCheckoutDomain},
	{"style-src", AirwallexStaticDomain},
	{"style-src", AirwallexCheckoutDomain},
	{"frame-src", AirwallexCheckoutDomain},
	{"script-src", AirwallexDemoStaticDomain},
	{"script-src", AirwallexDemoCheckoutDomain},
	{"style-src", AirwallexDemoStaticDomain},
	{"style-src", AirwallexDemoCheckoutDomain},
	{"frame-src", AirwallexDemoCheckoutDomain},
	// @project-doc docs/interfaces/tf_cli_web_import.md#browser_security_headers
	{"connect-src", "http://127.0.0.1:43110"},
	{"connect-src", "http://127.0.0.1:43111"},
	{"connect-src", "http://127.0.0.1:43112"},
	{"connect-src", "http://127.0.0.1:43113"},
	{"connect-src", "http://127.0.0.1:43114"},
	{"connect-src", "http://127.0.0.1:43115"},
	{"connect-src", "http://127.0.0.1:43116"},
	{"connect-src", "http://127.0.0.1:43117"},
	{"connect-src", "http://127.0.0.1:43118"},
	{"connect-src", "http://127.0.0.1:43119"},
}

// GenerateNonce returns sixteen random bytes encoded as padded standard base64
// text for CSP. If the entropy reader fails, crypto/rand.Read terminates the
// process before GenerateNonce can return an error to its caller.
func GenerateNonce() (string, error) {
	// Sixteen random bytes supply a 128-bit nonce value.
	b := make([]byte, 16)
	// The error check follows the Read signature. An entropy-reader failure
	// terminates the process before this branch can run.
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate CSP nonce: %w", err)
	}
	// Encode nonce bytes as padded base64 so the shared HTML and response-policy
	// value uses printable text rather than raw random bytes.
	return base64.StdEncoding.EncodeToString(b), nil
}

// GetNonceFromContext returns the string nonce stored under CSPNonceKey in a
// nonnil Gin context. Missing values or other types return empty text; frontend
// rendering uses the same value assigned by SecurityHeaders.
func GetNonceFromContext(c *gin.Context) string {
	if nonce, exists := c.Get(CSPNonceKey); exists {
		if s, ok := nonce.(string); ok {
			return s
		}
	}
	return ""
}

// SecurityHeaders constructs baseline browser-header middleware from cfg and an
// optional frame-origin reader. Blank policy selects DefaultCSPPolicy and
// runtime supplementation runs at construction. The frame reader is called per
// request before path and enablement checks; supply canonical CSP sources safe
// for concurrent reads.
//
// Selected gateway prefixes skip CSP and nonce assignment after baseline
// headers are set. Other paths generate and store a request nonce when CSP is
// enabled, then replace every policy placeholder with that value for frontend
// rendering. The source insertion helpers edit text and do not validate
// supplied origin tokens or deduplicate dynamic frame values.
func SecurityHeaders(cfg httpconfig.CSPConfig, getFrameSrcOrigins func() []string) gin.HandlerFunc {
	policy := strings.TrimSpace(cfg.Policy)
	if policy == "" {
		policy = httpconfig.DefaultCSPPolicy
	}

	// Apply the configured integration permissions once before serving requests.
	// Per-request work can then add current frame sources and substitute that
	// request's nonce.
	policy = enhanceCSPPolicy(policy)

	return func(c *gin.Context) {
		finalPolicy := policy
		if getFrameSrcOrigins != nil {
			for _, origin := range getFrameSrcOrigins() {
				if origin != "" {
					finalPolicy = addToDirective(finalPolicy, "frame-src", origin)
				}
			}
		}

		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// Per-request nonce text belongs to frontend rendering. The selected gateway
		// path skips that phase after the frame reader and baseline response headers
		// have already run.
		if isAPIRoutePath(c) {
			c.Next()
			return
		}

		if cfg.Enabled {
			nonce, err := GenerateNonce()
			if err != nil {
				// If the nonce helper returns an error, replace each placeholder with
				// unsafe-inline because rendering has no request nonce to share with CSP.
				log.Printf("[SecurityHeaders] %v; using CSP without a nonce", err)
				c.Header("Content-Security-Policy", strings.ReplaceAll(finalPolicy, NonceTemplate, "'unsafe-inline'"))
			} else {
				// Publish the nonce before downstream frontend rendering. A
				// placeholder-based policy substitutes that same value in every matching
				// header position so rendered HTML and CSP agree.
				c.Set(CSPNonceKey, nonce)
				c.Header("Content-Security-Policy", strings.ReplaceAll(finalPolicy, NonceTemplate, "'nonce-"+nonce+"'"))
			}
		}
		c.Next()
	}
}

// isAPIRoutePath selects the gateway prefixes that skip CSP assignment: /v1/,
// /v1beta/, /antigravity/, /responses, and /images. It returns false for
// missing request data; other API paths are not covered by this predicate.
func isAPIRoutePath(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	path := c.Request.URL.Path
	return strings.HasPrefix(path, "/v1/") ||
		strings.HasPrefix(path, "/v1beta/") ||
		strings.HasPrefix(path, "/antigravity/") ||
		strings.HasPrefix(path, "/responses") ||
		strings.HasPrefix(path, "/images")
}

// enhanceCSPPolicy adds configured integration permissions and a script nonce
// placeholder when the policy contains neither that placeholder nor any nonce
// prefix. Directive and source matching is case-sensitive; existing matching
// values stay in the first recognized directive, and literal nonce text is
// retained.
func enhanceCSPPolicy(policy string) string {
	// A policy already containing placeholder or nonce text supplies its own
	// nonce-bearing syntax. Add the script placeholder only when both forms are
	// absent.
	if !strings.Contains(policy, NonceTemplate) && !strings.Contains(policy, "'nonce-") {
		policy = addToDirective(policy, "script-src", NonceTemplate)
	}

	// Custom policies can omit configured integration sources. Add each missing
	// pair so default and supplemented policies advertise the same listed
	// permissions.
	for _, required := range requiredCSPDirectiveValues {
		if !directiveHasValue(policy, required.directive, required.value) {
			policy = addToDirective(policy, required.directive, required.value)
		}
	}

	return policy
}

// directiveHasValue reports whether the first exact matching directive contains
// value as a complete whitespace-delimited token. It splits semicolon sections
// and compares case-sensitively; a later repeated directive is not consulted
// after the first match.
func directiveHasValue(policy, directive, value string) bool {
	for rawDirective := range strings.SplitSeq(policy, ";") {
		fields := strings.Fields(strings.TrimSpace(rawDirective))
		if len(fields) == 0 || fields[0] != directive {
			continue
		}
		return slices.Contains(fields[1:], value)
	}
	return false
}

// addToDirective inserts value into the first textual directive followed by a
// space. Callers provide canonical directive names and source tokens; insertion
// neither validates nor deduplicates them. Missing directives gain self and
// value after a terminated default-src section, or at the policy's front.
func addToDirective(policy, directive, value string) string {
	directivePrefix := directive + " "
	idx := strings.Index(policy, directivePrefix)

	if idx == -1 {
		// When no textual directive matches, create its own source list containing
		// self and the requested value. A terminated default-src section supplies an
		// insertion position.
		defaultSrcIdx := strings.Index(policy, "default-src ")
		if defaultSrcIdx != -1 {
			endIdx := strings.Index(policy[defaultSrcIdx:], ";")
			if endIdx != -1 {
				insertPos := defaultSrcIdx + endIdx + 1

				return policy[:insertPos] + " " + directive + " 'self' " + value + ";" + policy[insertPos:]
			}
		}

		return directive + " 'self' " + value + "; " + policy
	}

	endIdx := strings.Index(policy[idx:], ";")

	// An unterminated final directive has no separator before which to insert the
	// source. Append to that final source list so the policy text stays
	// contiguous.
	if endIdx == -1 {
		return policy + " " + value
	}

	insertPos := idx + endIdx
	return policy[:insertPos] + " " + value + policy[insertPos:]
}
