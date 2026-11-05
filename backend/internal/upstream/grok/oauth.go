package grok

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	urlvalidator "github.com/TokenFlux/TokenRouter/internal/egress/urlpolicy"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/pkg/oauthpkce"
	wiregrok "github.com/TokenFlux/TokenRouter/internal/protocol/grok"
)

// oauth.go resolves and validates Grok OAuth endpoints, builds API URLs, and
// parses authorization input.
// OAuth endpoints and API forwarding URLs use different local policies.
// Authorization and token endpoints normally require trusted x.ai hosts,
// while general base URLs permit HTTPS destinations after literal-host
// checks. The unsafe override switch selects format-only validation and can
// bypass the normal known-path normalization when no custom validator is
// supplied.
//
// Base URL builders append paths only after effective-value selection and
// validation. Official base hosts use /v1, while custom prefixes remain
// usable; query, fragment, and userinfo are rejected by known-path
// normalization. Authorization callback parsing retains whether state must be
// checked, and random helpers provide state, nonce, session, and PKCE values.
// Validation and generation here do not consume authorization sessions or
// persist credentials; the authorization caller owns those steps and
// redirect/state verification.

// OAuth defaults and configuration fields select issuer, client, scopes, callbacks,
// forwarding hosts, and opt-in policy relaxations before the respective validators run.
const (
	OAuthIssuer = "https://auth.x.ai" // default OAuth issuer origin

	DefaultAuthorizeURL   = OAuthIssuer + "/oauth2/authorize"                                // default authorization endpoint URL
	DefaultTokenURL       = OAuthIssuer + "/oauth2/token"                                    // default token exchange URL
	DefaultBaseURL        = "https://api.x.ai/v1"                                            // default public API URL
	DefaultCLIBaseURL     = "https://cli-chat-proxy.grok.com/v1"                             // default CLI proxy URL
	DefaultUSEast1BaseURL = "https://us-east-1.api.x.ai/v1"                                  // US east regional URL
	DefaultUSWest2BaseURL = "https://us-west-2.api.x.ai/v1"                                  // US west regional URL
	DefaultEUWest1BaseURL = "https://eu-west-1.api.x.ai/v1"                                  // EU west regional URL
	DefaultClientID       = "b1a00492-073a-47ea-816f-4c329264a828"                           // default OAuth client identifier
	DefaultScope          = "openid profile email offline_access grok-cli:access api:access" // default authorization scope text
	DefaultRedirectURI    = "http://127.0.0.1:56121/callback"                                // default loopback callback URL

)

// These host patterns distinguish OAuth issuer endpoints from recognized API
// and CLI forwarding hosts; validators and official-host classification use separate lists.
var (
	oauthEndpointAllowedHosts = []string{"x.ai", "*.x.ai"}
	// baseURLAllowedHosts includes regional API subdomains alongside the public API
	// and CLI proxy, defining which hosts receive the official /v1 path rule.
	baseURLAllowedHosts = []string{"api.x.ai", "*.api.x.ai", "cli-chat-proxy.grok.com"}
)

// BaseURLValidator validates an effective forwarding base URL before endpoint
// paths are appended. Callers select their provider's host and scheme policy;
// successful values then pass through known-path normalization in injected
// validator paths.
type BaseURLValidator func(string) (string, error)

// RuntimeSanityCheck records one diagnostic value with its validation and
// default-selection result.
type RuntimeSanityCheck struct {
	// Value, Valid, and Error describe one validation result. On success,
	// Value contains sanitized normalized text. Failure displays the sanitized
	// input and keeps the redacted diagnostic in Error.
	Value string `json:"value"`           // displayed effective URL text
	Valid bool   `json:"valid"`           // effective value passed validation
	Error string `json:"error,omitempty"` // redacted URL validation diagnostic
	// IsDefault reports whether the associated configuration field is blank when the
	// check runs. It does not compare the selected text with a default literal.
	IsDefault bool `json:"is_default,omitempty"` // configuration selection was blank
}

// RuntimeSanityReport summarizes effective URL checks and runtime diagnostic
// policy flags for consumers.
type RuntimeSanityReport struct {
	BaseURL               RuntimeSanityCheck `json:"base_url"`                // effective forwarding URL check
	OAuthAuthorizeURL     RuntimeSanityCheck `json:"oauth_authorize_url"`     // effective authorize endpoint check
	OAuthTokenURL         RuntimeSanityCheck `json:"oauth_token_url"`         // effective token endpoint check
	OAuthRedirectURI      RuntimeSanityCheck `json:"oauth_redirect_uri"`      // effective redirect format check
	UnsafeURLOverrides    bool               `json:"unsafe_url_overrides"`    // URL override relaxation enabled
	UnsafeHighConcurrency bool               `json:"unsafe_high_concurrency"` // concurrency override relaxation enabled
	PublicGatewayScope    string             `json:"public_gateway_scope"`    // fixed gateway diagnostic label
	ProxyPolicy           string             `json:"proxy_policy"`            // fixed proxy diagnostic description
}

// AuthorizationInput aliases parsed authorization code and state information
// used by provider authorization.
type AuthorizationInput = wiregrok.AuthorizationInput

// TokenResponse aliases the xAI OAuth token fields used by credential
// exchange callers.
type TokenResponse = wiregrok.TokenResponse

// EffectiveAuthorizeURL returns the trimmed authorization endpoint
// configuration override or DefaultAuthorizeURL. This selection performs no URL
// validation; callers use ValidatedAuthorizeURL before constructing a
// credential-related authorization request.
func EffectiveAuthorizeURL() string {
	return overrideOrDefault(currentOverrides().AuthorizeURL, DefaultAuthorizeURL)
}

// ValidatedAuthorizeURL validates the currently selected authorization
// endpoint through OAuth endpoint policy. It returns validation errors rather
// than replacing invalid configuration input; NewOAuthClient's token
// construction has a separate default-on-error rule.
func ValidatedAuthorizeURL() (string, error) {
	return ValidateOAuthEndpointURL(EffectiveAuthorizeURL())
}

// EffectiveTokenURL returns the trimmed token endpoint configuration override
// or DefaultTokenURL. The selected text is unvalidated; credential exchange
// construction uses ValidatedTokenURL or its own checked default selection.
func EffectiveTokenURL() string {
	return overrideOrDefault(currentOverrides().TokenURL, DefaultTokenURL)
}

// ValidatedTokenURL checks the selected token endpoint through OAuth policy.
// An invalid override returns an error, allowing credential exchange callers
// to choose whether to reject it or use a trusted default.
func ValidatedTokenURL() (string, error) {
	return ValidateOAuthEndpointURL(EffectiveTokenURL())
}

// EffectiveClientID returns the trimmed configured OAuth client identifier,
// or DefaultClientID when the configuration value is blank. The value is
// selected when called rather than pinned globally at process startup.
func EffectiveClientID() string {
	return overrideOrDefault(currentOverrides().ClientID, DefaultClientID)
}

// EffectiveScope returns the trimmed configured OAuth scope or DefaultScope
// when the configuration value is blank. It retains nonblank scope text as
// supplied after trimming, leaving scope interpretation to authorization
// requests.
func EffectiveScope() string {
	return overrideOrDefault(currentOverrides().Scope, DefaultScope)
}

// EffectiveRedirectURI prefers a nonblank trimmed argument, then the
// configuration value, then DefaultRedirectURI. It does not validate the
// result; authorization callers validate redirect policy separately before
// using the selected callback destination.
func EffectiveRedirectURI(override string) string {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		return trimmed
	}
	return overrideOrDefault(currentOverrides().RedirectURI, DefaultRedirectURI)
}

// EffectiveBaseURL prefers a nonblank trimmed argument, then configured
// configuration input, then DefaultBaseURL, trimming trailing slashes from the
// selected text. It performs no destination validation before endpoint
// builders apply their policy.
func EffectiveBaseURL(override string) string {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		return strings.TrimRight(trimmed, "/")
	}
	return strings.TrimRight(overrideOrDefault(currentOverrides().BaseURL, DefaultBaseURL), "/")
}

// ValidatedBaseURL selects an effective base URL and applies ValidateBaseURL.
// It uses the public-base policy, including its unsafe override mode; OAuth
// callers needing official-host restriction choose ValidateTrustedBaseURL
// through their provider-specific policy.
func ValidatedBaseURL(override string) (string, error) {
	return ValidateBaseURL(EffectiveBaseURL(override))
}

// validatedBaseURLWithValidator selects the effective base and validates it
// using the supplied policy. Nil uses ValidatedBaseURL directly. A nonnil
// validator's successful output passes through normalizeKnownBaseURLPath,
// even when unsafe configuration overrides are enabled.
func validatedBaseURLWithValidator(override string, validator BaseURLValidator) (string, error) {
	if validator == nil {
		return ValidatedBaseURL(override)
	}
	raw := EffectiveBaseURL(override)
	validated, err := validator(raw)
	if err != nil {
		return "", err
	}
	// Injected policies can permit custom hosts, but path components still need
	// normalization before callers append an endpoint, including under the unsafe configuration switch.
	return normalizeKnownBaseURLPath(validated)
}

// RuntimeSanity assembles effective URL checks and unsafe-switch flags for
// diagnostics. Values are read from the configuration during construction;
// PublicGatewayScope and ProxyPolicy are fixed diagnostic labels rather than
// a generated endpoint capability inventory.
func RuntimeSanity() RuntimeSanityReport {
	return RuntimeSanityReport{
		BaseURL: runtimeSanityCheck(EffectiveBaseURL(""), currentOverrides().BaseURL, ValidatedBaseURL),

		OAuthAuthorizeURL: runtimeSanityCheck(EffectiveAuthorizeURL(), currentOverrides().AuthorizeURL, func(string) (string, error) {
			return ValidatedAuthorizeURL()
		}),

		OAuthTokenURL: runtimeSanityCheck(EffectiveTokenURL(), currentOverrides().TokenURL, func(string) (string, error) {
			return ValidatedTokenURL()
		}),

		OAuthRedirectURI: runtimeSanityCheck(EffectiveRedirectURI(""), currentOverrides().RedirectURI, validateRedirectURI),

		UnsafeURLOverrides: AllowUnsafeURLOverrides(),

		UnsafeHighConcurrency: AllowUnsafeHighConcurrency(),

		PublicGatewayScope: "responses_only",

		ProxyPolicy: "provider_proxy_optional; OAuth URLs use trusted-host allowlists; API-key base URLs require public HTTPS unless unsafe overrides are enabled",
	}
}

// runtimeSanityCheck validates a selected value and records whether its
// configuration source is blank. Successful normalized URLs are sanitized for
// display; failures retain a sanitized supplied value and a redacted error.
// The validate callback is required.
func runtimeSanityCheck(value string, configured string, validate func(string) (string, error)) RuntimeSanityCheck {
	normalized, err := validate(value)
	check := RuntimeSanityCheck{
		Value:     sanitizeRuntimeURLValue(normalized),
		Valid:     err == nil,
		IsDefault: strings.TrimSpace(configured) == "",
	}
	if err != nil {
		check.Value = sanitizeRuntimeURLValue(value)
		check.Error = sanitizeRuntimeError(err.Error(), value)
	}
	return check
}

// validateRedirectURI checks callback URL format with HTTP allowed. It does
// not apply a trusted-host list or a callback-state check; authorization
// callers decide which otherwise valid redirect destinations they permit.
func validateRedirectURI(raw string) (string, error) {
	return urlvalidator.ValidateURLFormat(raw, true)
}

// sanitizeRuntimeURLValue trims text and removes userinfo, query, and
// fragment from parseable absolute URLs. Empty input stays empty, while
// unparseable or nonabsolute text is returned trimmed as supplied, so callers
// cannot treat it as general secret redaction.
func sanitizeRuntimeURLValue(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return trimmed
	}
	// Display parseable URLs without credential-bearing authority, query, or fragment
	// components; malformed text takes the earlier trimmed-text path and needs separate redaction.
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

// sanitizeRuntimeError applies logredact text redaction and replaces raw or
// redacted occurrences of the supplied value with its URL-sanitized form.
// Blank rawValue needs only text redaction; non-URL values retain the
// sanitizer's trimmed-text behavior.
func sanitizeRuntimeError(rawErr string, rawValue string) string {
	redacted := logredact.RedactText(rawErr)
	trimmedValue := strings.TrimSpace(rawValue)
	if trimmedValue == "" {
		return redacted
	}
	sanitizedValue := sanitizeRuntimeURLValue(trimmedValue)
	redacted = strings.ReplaceAll(redacted, trimmedValue, sanitizedValue)
	redacted = strings.ReplaceAll(redacted, logredact.RedactText(trimmedValue), sanitizedValue)
	return redacted
}

// ValidateOAuthEndpointURL normally requires HTTPS and a host in the x.ai
// allowlist while rejecting blocked literal hosts. Unsafe overrides select
// format-only HTTP/HTTPS validation. DNS-address checks belong to the
// caller's transport policy.
func ValidateOAuthEndpointURL(raw string) (string, error) {
	// Format-only mode allows operator-controlled HTTP/HTTPS endpoints; trusted
	// issuer hosts apply below when that relaxation is disabled before authorization use.
	if AllowUnsafeURLOverrides() {
		return urlvalidator.ValidateURLFormat(raw, true)
	}
	return urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
		AllowedHosts:     oauthEndpointAllowedHosts,
		RequireAllowlist: true,
		AllowPrivate:     false,
	})
}

// ValidateBaseURL normally validates HTTPS and literal-host permission, then
// normalizes known host/path rules. Custom hostnames are permitted here.
// Unsafe overrides return format-only validation directly, allowing custom
// paths and skipping normal path-component rejection in this branch.
func ValidateBaseURL(raw string) (string, error) {
	// Unsafe mode returns directly after format validation, retaining custom path
	// components that the normal branch would reject or normalize before endpoint construction.
	if AllowUnsafeURLOverrides() {
		return urlvalidator.ValidateURLFormat(raw, true)
	}
	normalized, err := urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
		AllowPrivate: false,
	})
	if err != nil {
		return "", err
	}
	return normalizeKnownBaseURLPath(normalized)
}

// ValidateTrustedBaseURL normally requires HTTPS and a recognized API or CLI
// base hostname before known-path normalization. Unsafe overrides return
// format-only validation directly, so callers choose that configuration switch
// only when its relaxed destination policy is intended.
func ValidateTrustedBaseURL(raw string) (string, error) {
	// This opt-in bypasses official-host and known-path rules for the caller's
	// development endpoint; disabled mode applies both checks before building any request URL.
	if AllowUnsafeURLOverrides() {
		return urlvalidator.ValidateURLFormat(raw, true)
	}
	normalized, err := urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
		AllowedHosts:     baseURLAllowedHosts,
		RequireAllowlist: true,
		AllowPrivate:     false,
	})
	if err != nil {
		return "", err
	}
	return normalizeKnownBaseURLPath(normalized)
}

// normalizeKnownBaseURLPath validates a parsed base URL's components and
// normalizes its decoded path. Missing scheme/host, userinfo, any query
// marker, and fragments fail. An empty path becomes /v1; recognized official
// hosts require that exact path, while custom hosts retain other configured
// prefixes.
//
// Trailing path slashes are removed and RawPath is cleared before
// serialization, so encoded spelling can change while the decoded prefix
// remains. This separates reusable custom gateway prefixes from official
// endpoint rules before a builder adds its own suffix. Scheme and host
// permission belong to the validator invoked before this helper.
func normalizeKnownBaseURLPath(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid base URL")
	}
	if parsed.User != nil {
		return "", errors.New("base URL must not include userinfo")
	}
	if parsed.ForceQuery || parsed.RawQuery != "" {
		return "", errors.New("base URL must not include a query")
	}
	if parsed.Fragment != "" {
		return "", errors.New("base URL must not include a fragment")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		parsed.Path = "/v1"
		parsed.RawPath = ""
		return strings.TrimRight(parsed.String(), "/"), nil
	}
	if path != "/v1" && IsOfficialBaseURLHost(parsed.Hostname()) {
		return "", fmt.Errorf("base URL path must be /v1")
	}
	parsed.Path = path
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

// IsOfficialBaseURLHost reports whether the trimmed, lowercased hostname
// matches a configured API or CLI host. Wildcards also match their suffix
// apex; callers pass a hostname without URL scheme or port.
func IsOfficialBaseURLHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, allowed := range baseURLAllowedHosts {
		if after, ok := strings.CutPrefix(allowed, "*."); ok {
			suffix := after
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == allowed {
			return true
		}
	}
	return false
}

// IsOfficialBaseURL recognizes configured official hostnames through URL
// parsing. Empty or invalid/hostless input returns true for callers selecting
// default endpoints; this is a classification helper, so it does not
// establish scheme, path, or destination permission.
func IsOfficialBaseURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return true
	}
	return IsOfficialBaseURLHost(parsed.Hostname())
}

// AllowUnsafeURLOverrides reports the configured relaxation of upstream URL checks.
// An absent startup snapshot leaves the flag false. Endpoint validators decide
// which checks to relax when this explicit process setting is enabled.
func AllowUnsafeURLOverrides() bool {
	return currentOverrides().AllowUnsafeURLOverrides
}

// AllowUnsafeHighConcurrency reports the configured higher-concurrency opt-in flag.
// An absent startup snapshot leaves it disabled. Provider admission owns the
// effective concurrency limit and uses this value when applying its policy.
func AllowUnsafeHighConcurrency() bool {
	return currentOverrides().UnsafeAllowConcurrencyAboveOne
}

// overrideOrDefault returns a trimmed nonblank configuration value, otherwise
// fallback as supplied. Selection happens on each call, letting effective
// OAuth and API readers observe the currently published configuration snapshot.
func overrideOrDefault(raw, fallback string) string {
	if value := strings.TrimSpace(raw); value != "" {
		return value
	}
	return fallback
}

// GenerateRandomBytes obtains n cryptographic bytes through oauthpkce.
// Zero returns an empty slice and negative n panics during allocation. The
// underlying Go crypto/rand.Read fills the slice with nil error or terminates
// the process when its random reader reports an error.
func GenerateRandomBytes(n int) ([]byte, error) {
	return oauthpkce.RandomBytes(n)
}

// GenerateState returns lowercase hexadecimal text from thirty-two random
// bytes for OAuth session correlation.
func GenerateState() (string, error) {
	bytes, err := GenerateRandomBytes(32)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// GenerateNonce returns lowercase hexadecimal text from sixteen random bytes
// for OAuth nonce correlation.
func GenerateNonce() (string, error) {
	bytes, err := GenerateRandomBytes(16)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// GenerateSessionID returns lowercase hexadecimal text from sixteen random
// bytes for authorization session identification.
func GenerateSessionID() (string, error) {
	bytes, err := GenerateRandomBytes(16)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// GenerateCodeVerifier returns an unpadded base64url PKCE verifier derived
// from thirty-two random bytes. Callers retain the verifier with the session
// for code exchange. The underlying Go crypto/rand.Read finishes with nil
// error or terminates the process when its random reader reports an error.
func GenerateCodeVerifier() (string, error) {
	return oauthpkce.Verifier()
}

// GenerateCodeChallenge returns the unpadded base64url SHA-256 digest of the
// supplied PKCE verifier text.
func GenerateCodeChallenge(verifier string) string {
	return oauthpkce.Challenge(verifier)
}

// BuildAuthorizationURL appends encoded grant parameters to the validated
// effective authorization endpoint, using the current client/scope and
// selected redirect URI. Supply an endpoint with no existing query or
// fragment: concatenation adds a literal ? and does not merge URL components.
// Callers keep state and the verifier for the S256 challenge in their session.
//
// The redirect is selected but unvalidated here. plan=generic and
// referrer=tokenrouter are encoded alongside state, nonce, and challenge.
// Provider authorization checks callback state and any submitted redirect
// against the saved session before exchange. Invalid authorize endpoints
// return an error before URL formatting.
func BuildAuthorizationURL(state, codeChallenge, redirectURI, nonce string) (string, error) {
	redirectURI = EffectiveRedirectURI(redirectURI)
	authorizeURL, err := ValidatedAuthorizeURL()
	if err != nil {
		return "", fmt.Errorf("invalid authorize url: %w", err)
	}

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", EffectiveClientID())
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", EffectiveScope())
	params.Set("state", state)
	params.Set("nonce", nonce)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	params.Set("plan", "generic")
	params.Set("referrer", "tokenrouter")

	return fmt.Sprintf("%s?%s", authorizeURL, params.Encode()), nil
}

// ParseAuthorizationInput accepts a callback URL, query string, or bare code
// and returns trimmed parsed fields. Empty input yields an empty value. A
// code found through URL/query parsing sets RequiresState even when state is
// missing, allowing callers to reject incomplete callbacks.
//
// The final bare-code path retains the trimmed input with RequiresState
// false. This source-form distinction survives parsing so authorization
// callers can apply state verification to callback representations rather
// than silently treating a missing state as an ordinary bare code. Parsing
// itself verifies no state, code validity, redirect destination, or session
// ownership.
func ParseAuthorizationInput(raw string) AuthorizationInput {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return AuthorizationInput{}
	}

	if parsed, err := url.Parse(trimmed); err == nil && parsed != nil {
		values := parsed.Query()
		if code := strings.TrimSpace(values.Get("code")); code != "" {
			return AuthorizationInput{
				Code:          code,
				State:         strings.TrimSpace(values.Get("state")),
				RequiresState: true,
			}
		}
	}

	queryCandidate := strings.TrimPrefix(trimmed, "?")
	if strings.Contains(queryCandidate, "=") {
		if values, err := url.ParseQuery(queryCandidate); err == nil {
			if code := strings.TrimSpace(values.Get("code")); code != "" {
				return AuthorizationInput{
					Code:          code,
					State:         strings.TrimSpace(values.Get("state")),
					RequiresState: true,
				}
			}
		}
	}

	return AuthorizationInput{Code: trimmed}
}

// BuildResponsesURLWithValidator appends /responses after selecting and
// validating the base URL. A nil validator uses platform policy; supplied
// validators own host/scheme permission and are followed by known-path
// normalization before the endpoint is appended.
func BuildResponsesURLWithValidator(baseURL string, validator BaseURLValidator) (string, error) {
	validatedBaseURL, err := validatedBaseURLWithValidator(baseURL, validator)
	if err != nil {
		return "", fmt.Errorf("invalid base url: %w", err)
	}
	return validatedBaseURL + "/responses", nil
}

// BuildChatCompletionsURLWithValidator appends /chat/completions after
// selecting and validating the base URL. A nil validator uses platform
// policy; supplied validators own host/scheme permission and are followed by
// known-path normalization before the endpoint is appended.
func BuildChatCompletionsURLWithValidator(baseURL string, validator BaseURLValidator) (string, error) {
	validatedBaseURL, err := validatedBaseURLWithValidator(baseURL, validator)
	if err != nil {
		return "", fmt.Errorf("invalid base url: %w", err)
	}
	return validatedBaseURL + "/chat/completions", nil
}

// BuildImagesGenerationsURLWithValidator appends /images/generations after
// selecting and validating the base URL. A nil validator uses platform
// policy; supplied validators own host/scheme permission and are followed by
// known-path normalization before the endpoint is appended.
func BuildImagesGenerationsURLWithValidator(baseURL string, validator BaseURLValidator) (string, error) {
	validatedBaseURL, err := validatedBaseURLWithValidator(baseURL, validator)
	if err != nil {
		return "", fmt.Errorf("invalid base url: %w", err)
	}
	return validatedBaseURL + "/images/generations", nil
}

// BuildImagesEditsURLWithValidator appends /images/edits after selecting and
// validating the base URL. A nil validator uses platform policy; supplied
// validators own host/scheme permission and are followed by known-path
// normalization before the endpoint is appended.
func BuildImagesEditsURLWithValidator(baseURL string, validator BaseURLValidator) (string, error) {
	validatedBaseURL, err := validatedBaseURLWithValidator(baseURL, validator)
	if err != nil {
		return "", fmt.Errorf("invalid base url: %w", err)
	}
	return validatedBaseURL + "/images/edits", nil
}

// BuildVideosGenerationsURLWithValidator appends /videos/generations after
// selecting and validating the base URL. A nil validator uses platform
// policy; supplied validators own host/scheme permission and are followed by
// known-path normalization before the endpoint is appended.
func BuildVideosGenerationsURLWithValidator(baseURL string, validator BaseURLValidator) (string, error) {
	validatedBaseURL, err := validatedBaseURLWithValidator(baseURL, validator)
	if err != nil {
		return "", fmt.Errorf("invalid base url: %w", err)
	}
	return validatedBaseURL + "/videos/generations", nil
}

// BuildVideosEditsURLWithValidator appends /videos/edits after selecting and
// validating the base URL. A nil validator uses platform policy; supplied
// validators own host/scheme permission and are followed by known-path
// normalization before the endpoint is appended.
func BuildVideosEditsURLWithValidator(baseURL string, validator BaseURLValidator) (string, error) {
	validatedBaseURL, err := validatedBaseURLWithValidator(baseURL, validator)
	if err != nil {
		return "", fmt.Errorf("invalid base url: %w", err)
	}
	return validatedBaseURL + "/videos/edits", nil
}

// BuildVideosExtensionsURLWithValidator appends /videos/extensions after
// selecting and validating the base URL. A nil validator uses platform
// policy; supplied validators own host/scheme permission and are followed by
// known-path normalization before the endpoint is appended.
func BuildVideosExtensionsURLWithValidator(baseURL string, validator BaseURLValidator) (string, error) {
	validatedBaseURL, err := validatedBaseURLWithValidator(baseURL, validator)
	if err != nil {
		return "", fmt.Errorf("invalid base url: %w", err)
	}
	return validatedBaseURL + "/videos/extensions", nil
}

// BuildVideoURLWithValidator appends a trimmed, escaped video request ID to
// the validated base. Blank IDs, dot segments, and NUL/CR/LF fail before path
// construction; the escaping keeps the ID within one suffix segment for task
// status lookup.
func BuildVideoURLWithValidator(baseURL, requestID string, validator BaseURLValidator) (string, error) {
	validatedBaseURL, err := validatedBaseURLWithValidator(baseURL, validator)
	if err != nil {
		return "", fmt.Errorf("invalid base url: %w", err)
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return "", fmt.Errorf("request id is required")
	}
	// Escape the task ID as a segment after rejecting dot segments and control
	// characters, preventing caller-supplied identifiers from changing endpoint path resolution.
	if requestID == "." || requestID == ".." || strings.ContainsAny(requestID, "\x00\r\n") {
		return "", fmt.Errorf("invalid request id")
	}
	return validatedBaseURL + "/videos/" + url.PathEscape(requestID), nil
}
