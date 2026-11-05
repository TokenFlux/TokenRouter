package grok

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	urlvalidator "github.com/TokenFlux/TokenRouter/internal/egress/urlpolicy"
)

// TestParseAuthorizationInput compares callback URLs, query strings, and a
// bare authorization code. It asserts extracted code/state values and the
// source-form flag, including callback representations whose required state
// was omitted.
func TestParseAuthorizationInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string // authorization input case name
		raw               string // supplied callback or URL
		wantCode          string // expected parsed authorization code
		wantState         string // expected parsed callback state
		wantRequiresState bool   // expected callback state requirement
	}{
		{
			name: "full callback url",

			raw: "http://127.0.0.1:56121/callback?code=abc123&state=state456",

			wantCode: "abc123",

			wantState: "state456",

			wantRequiresState: true,
		},
		{
			name: "query string",

			raw: "?code=abc123&state=state456",

			wantCode: "abc123",

			wantState: "state456",

			wantRequiresState: true,
		},
		{
			name: "full callback url missing state",

			raw: "http://127.0.0.1:56121/callback?code=abc123",

			wantCode: "abc123",

			wantRequiresState: true,
		},
		{
			name: "query string missing state",

			raw: "code=abc123",

			wantCode: "abc123",

			wantRequiresState: true,
		},
		{
			name:     "bare code",
			raw:      "abc123",
			wantCode: "abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseAuthorizationInput(tt.raw)
			require.Equal(t, tt.wantCode, got.Code)
			require.Equal(t, tt.wantState, got.State)
			require.Equal(t, tt.wantRequiresState, got.RequiresState)
		})
	}
}

// TestBuildAuthorizationURLIncludesHermesCompatibleParameters supplies
// configured client, scope, and authorize endpoint values. It asserts the
// encoded code-flow, redirect, state, nonce, S256, plan, and referrer
// parameters are retained in the resulting URL.
func TestBuildAuthorizationURLIncludesHermesCompatibleParameters(t *testing.T) {
	setGrokTestOverride(t, "AuthorizeURL", "https://auth.example.test/oauth2/authorize")
	setGrokTestOverride(t, "ClientID", "client-id")
	setGrokTestOverride(t, "Scope", "openid profile offline_access api:access")
	setGrokTestOverride(t, "AllowUnsafeURLOverrides", "true")

	authURL, err := BuildAuthorizationURL("state", "challenge", "http://127.0.0.1:56121/callback", "nonce")
	require.NoError(t, err)
	parsed, err := url.Parse(authURL)
	require.NoError(t, err)

	values := parsed.Query()
	require.Equal(t, "https", parsed.Scheme)
	require.Equal(t, "auth.example.test", parsed.Host)
	require.Equal(t, "/oauth2/authorize", parsed.Path)
	require.Equal(t, "code", values.Get("response_type"))
	require.Equal(t, "client-id", values.Get("client_id"))
	require.Equal(t, "http://127.0.0.1:56121/callback", values.Get("redirect_uri"))
	require.Equal(t, "openid profile offline_access api:access", values.Get("scope"))
	require.Equal(t, "state", values.Get("state"))
	require.Equal(t, "nonce", values.Get("nonce"))
	require.Equal(t, "challenge", values.Get("code_challenge"))
	require.Equal(t, "S256", values.Get("code_challenge_method"))
	require.Equal(t, "generic", values.Get("plan"))
	require.Equal(t, "tokenrouter", values.Get("referrer"))
}

// TestValidateXAIURLsAllowOfficialOAuthAndGatewayHosts exercises default
// issuer, public API, and CLI URLs. It asserts those values validate, an
// empty official path becomes /v1, and Chat construction appends its endpoint
// after trimming.
func TestValidateXAIURLsAllowOfficialOAuthAndGatewayHosts(t *testing.T) {
	authorizeURL, err := ValidateOAuthEndpointURL(DefaultAuthorizeURL)
	require.NoError(t, err)
	require.Equal(t, DefaultAuthorizeURL, authorizeURL)

	tokenURL, err := ValidateOAuthEndpointURL(DefaultTokenURL)
	require.NoError(t, err)
	require.Equal(t, DefaultTokenURL, tokenURL)

	baseURL, err := ValidateBaseURL(DefaultBaseURL)
	require.NoError(t, err)
	require.Equal(t, DefaultBaseURL, baseURL)

	cliBaseURL, err := ValidateBaseURL(DefaultCLIBaseURL)
	require.NoError(t, err)
	require.Equal(t, DefaultCLIBaseURL, cliBaseURL)

	baseURLNoPath, err := ValidateBaseURL("https://api.x.ai")
	require.NoError(t, err)
	require.Equal(t, DefaultBaseURL, baseURLNoPath)

	chatURL, err := BuildChatCompletionsURLWithValidator(DefaultCLIBaseURL+"/", nil)
	require.NoError(t, err)
	require.Equal(t, DefaultCLIBaseURL+"/chat/completions", chatURL)
}

// TestBuildGrokMediaURLs builds image and video generation/edit/extension
// targets plus a video status URL. It asserts expected suffixes and escaped
// task IDs while blank, dot-segment, and control-character identifiers fail.
func TestBuildGrokMediaURLs(t *testing.T) {
	imagesURL, err := BuildImagesGenerationsURLWithValidator(DefaultBaseURL+"/", nil)
	require.NoError(t, err)
	require.Equal(t, DefaultBaseURL+"/images/generations", imagesURL)

	editsURL, err := BuildImagesEditsURLWithValidator(DefaultBaseURL, nil)
	require.NoError(t, err)
	require.Equal(t, DefaultBaseURL+"/images/edits", editsURL)

	videosURL, err := BuildVideosGenerationsURLWithValidator(DefaultBaseURL, nil)
	require.NoError(t, err)
	require.Equal(t, DefaultBaseURL+"/videos/generations", videosURL)

	videoEditsURL, err := BuildVideosEditsURLWithValidator(DefaultBaseURL, nil)
	require.NoError(t, err)
	require.Equal(t, DefaultBaseURL+"/videos/edits", videoEditsURL)

	videoExtensionsURL, err := BuildVideosExtensionsURLWithValidator(DefaultBaseURL, nil)
	require.NoError(t, err)
	require.Equal(t, DefaultBaseURL+"/videos/extensions", videoExtensionsURL)

	videoURL, err := BuildVideoURLWithValidator(DefaultBaseURL, "req 123", nil)
	require.NoError(t, err)
	require.Equal(t, DefaultBaseURL+"/videos/req%20123", videoURL)

	_, err = BuildVideoURLWithValidator(DefaultBaseURL, " ", nil)
	require.Error(t, err)

	// These task identifiers could alter endpoint resolution or header-like text
	// before escaping. Assert rejection so status URL construction retains its segment constraint.
	for _, requestID := range []string{".", "..", "req\x00id", "req\rid", "req\nid"} {
		_, err = BuildVideoURLWithValidator(DefaultBaseURL, requestID, nil)
		require.Error(t, err, "request_id=%q", requestID)
	}
}

// TestValidateXAIURLsRejectUntrustedOAuthAndUnsafeBaseURLsByDefault supplies
// an untrusted issuer, insecure loopback base, and wrong official API path.
// It asserts default validation rejects each listed input before request
// construction.
func TestValidateXAIURLsRejectUntrustedOAuthAndUnsafeBaseURLsByDefault(t *testing.T) {
	_, err := ValidateOAuthEndpointURL("https://auth.example.test/oauth2/token")
	require.Error(t, err)

	_, err = ValidateBaseURL("http://127.0.0.1:8080/v1")
	require.Error(t, err)

	_, err = ValidateBaseURL("https://api.x.ai/custom")
	require.Error(t, err)
}

// TestValidateBaseURLAllowsPublicThirdPartyGrokAPI supplies a custom HTTPS
// relay with a trailing slash. It asserts the public-base policy accepts and
// normalizes it, while trusted official-host validation rejects that same
// relay.
func TestValidateBaseURLAllowsPublicThirdPartyGrokAPI(t *testing.T) {
	baseURL, err := ValidateBaseURL("https://grok.example.test/v1/")
	require.NoError(t, err)
	require.Equal(t, "https://grok.example.test/v1", baseURL)

	_, err = ValidateTrustedBaseURL("https://grok.example.test/v1")
	require.Error(t, err)
}

// TestValidateBaseURLPathPrefixPolicy compares custom prefixes, custom empty
// paths, and wrong official paths. It asserts custom prefixes remain usable,
// empty paths become /v1, and official hosts reject other decoded prefixes.
func TestValidateBaseURLPathPrefixPolicy(t *testing.T) {
	// Custom relay deployments can place their API under different prefixes.
	// These cases assert construction retains that routing prefix after
	// trailing-slash normalization.
	prefixed, err := ValidateBaseURL("https://relay.example.test/xai/v1/")
	require.NoError(t, err)
	require.Equal(t, "https://relay.example.test/xai/v1", prefixed)

	deepPrefixed, err := ValidateBaseURL("https://relay.example.test/tenant-a/proxy")
	require.NoError(t, err)
	require.Equal(t, "https://relay.example.test/tenant-a/proxy", deepPrefixed)

	// The empty custom path receives the common API prefix so endpoint builders
	// append their suffix below /v1 rather than treating the host root as an endpoint.
	rootOnly, err := ValidateBaseURL("https://relay.example.test")
	require.NoError(t, err)
	require.Equal(t, "https://relay.example.test/v1", rootOnly)

	// Official hosts use the fixed API prefix even when a custom relay accepts
	// other prefixes; assert those unrelated path choices cannot be reused for official targets.
	_, err = ValidateBaseURL("https://api.x.ai/xai/v1")
	require.Error(t, err)
	_, err = ValidateBaseURL("https://cli-chat-proxy.grok.com/other")
	require.Error(t, err)
}

// TestIsOfficialBaseURL compares stored official URL spellings, invalid
// input, custom gateways, and lookalike host suffixes. It asserts default-
// selecting inputs and genuine official hosts qualify while each listed
// custom host does not.
func TestIsOfficialBaseURL(t *testing.T) {
	official := []string{
		"",
		"   ",
		DefaultBaseURL,
		DefaultCLIBaseURL,
		"https://api.x.ai",
		"HTTPS://API.X.AI:443/",
		"https://api.x.ai:0443/v1",
		"https://api.x.ai/%76%31",
		"https://api.x.ai:8443/v1",
		"HTTPS://CLI-CHAT-PROXY.GROK.COM:443/%76%31/",
		"::invalid::url", // Invalid URLs select the default endpoint.
	}
	for _, raw := range official {
		require.True(t, IsOfficialBaseURL(raw), "expected official: %q", raw)
	}

	custom := []string{
		"https://relay.example.test/v1",
		"https://relay.example.test/xai/v1",
		"http://relay.example.test/v1",
		"https://grok.com.evil.example.test/v1",
		"https://api.x.ai.evil.example.test/v1", // A lookalike suffix is outside *.api.x.ai.
	}
	for _, raw := range custom {
		require.False(t, IsOfficialBaseURL(raw), "expected custom: %q", raw)
	}
}

// TestRegionalAPIEndpointsAreOfficialAndTrusted supplies the listed regional
// API endpoints and a wrong regional path. It asserts each region is
// recognized and trusted while the regional /v1 path requirement still
// applies.
func TestRegionalAPIEndpointsAreOfficialAndTrusted(t *testing.T) {
	regional := []string{
		"https://us-east-1.api.x.ai/v1",
		"https://us-west-2.api.x.ai/v1",
		"https://eu-west-1.api.x.ai/v1",
	}
	for _, raw := range regional {
		require.True(t, IsOfficialBaseURL(raw), "expected official: %q", raw)

		validated, err := ValidateTrustedBaseURL(raw)
		require.NoError(t, err, "trusted validation should accept regional endpoint %q", raw)
		require.Equal(t, raw, validated)
	}

	// Regional targets share the official API-prefix rule. This case ensures
	// adding regional host recognition does not broaden their permitted path beyond that shared prefix.
	_, err := ValidateTrustedBaseURL("https://us-east-1.api.x.ai/other")
	require.Error(t, err)
}

// TestValidateBaseURLsRejectEmptyQueryDelimiter supplies otherwise usable
// custom and official bases ending with an empty query marker. It asserts
// both validation paths reject that component before endpoint suffixes are
// appended.
func TestValidateBaseURLsRejectEmptyQueryDelimiter(t *testing.T) {
	_, err := ValidateBaseURL("https://grok.example.test/v1?")
	require.Error(t, err)

	_, err = ValidateTrustedBaseURL("https://api.x.ai/v1?")
	require.Error(t, err)
}

// TestBuildResponsesURLWithValidatorUsesCallerPolicy injects an HTTP-
// permitting format validator for a custom host. It asserts Responses
// construction honors that caller policy and appends the suffix after base-
// path normalization.
func TestBuildResponsesURLWithValidatorUsesCallerPolicy(t *testing.T) {
	validator := func(raw string) (string, error) {
		return urlvalidator.ValidateURLFormat(raw, true)
	}

	target, err := BuildResponsesURLWithValidator("http://grok.example.test/v1/", validator)
	require.NoError(t, err)
	require.Equal(t, "http://grok.example.test/v1/responses", target)
}

// TestValidateTrustedBaseURLAcceptsOfficialRegionalHosts iterates the three
// configured regional default constants. It asserts trusted validation
// accepts each complete URL and returns its existing /v1 path spelling
// unchanged.
func TestValidateTrustedBaseURLAcceptsOfficialRegionalHosts(t *testing.T) {
	for _, raw := range []string{DefaultUSEast1BaseURL, DefaultUSWest2BaseURL, DefaultEUWest1BaseURL} {
		got, err := ValidateTrustedBaseURL(raw)
		require.NoError(t, err, raw)
		require.Equal(t, raw, got)
	}
}

// TestBuildResponsesURLPreservesUnsafeOverrideCustomPath enables unsafe mode
// and supplies a loopback URL with a custom path. It asserts the nil-
// validator builder keeps that path before appending Responses under format-
// only policy.
func TestBuildResponsesURLPreservesUnsafeOverrideCustomPath(t *testing.T) {
	setGrokTestOverride(t, "AllowUnsafeURLOverrides", "true")

	target, err := BuildResponsesURLWithValidator("http://localhost:8080/custom", nil)
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8080/custom/responses", target)
}

// TestBuildResponsesURLWithValidatorRejectsBaseURLComponents injects a
// permissive validator for bases containing userinfo, queries, or fragments.
// It asserts post-validation path normalization rejects those components and
// its returned errors omit the supplied secret text.
func TestBuildResponsesURLWithValidatorRejectsBaseURLComponents(t *testing.T) {
	permissive := func(raw string) (string, error) { return raw, nil }
	tests := []struct {
		name string // URL policy case name
		raw  string // supplied callback or URL
	}{
		{name: "userinfo", raw: "https://user:secret@grok.example.test/v1"},
		{name: "query", raw: "https://grok.example.test/v1?token=secret"},
		{name: "empty query delimiter", raw: "https://grok.example.test/v1?"},
		{name: "fragment", raw: "https://grok.example.test/v1#secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildResponsesURLWithValidator(tt.raw, permissive)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
		})
	}
}

// TestValidateXAIURLsAllowUnsafeDevOverride enables format-only override mode
// for loopback HTTP endpoints. It asserts the issuer and forwarding URL are
// accepted and trailing base slashes are trimmed by that selected policy.
func TestValidateXAIURLsAllowUnsafeDevOverride(t *testing.T) {
	setGrokTestOverride(t, "AllowUnsafeURLOverrides", "true")

	tokenURL, err := ValidateOAuthEndpointURL("http://127.0.0.1:8080/oauth2/token")
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:8080/oauth2/token", tokenURL)

	baseURL, err := ValidateBaseURL("http://127.0.0.1:8080/v1/")
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:8080/v1", baseURL)
}

// TestRuntimeSanityReportsSafeDefaults clears configured URL and unsafe
// switch values before building diagnostics. It asserts defaults validate,
// the base is marked default, and the fixed scope/proxy labels and disabled
// relaxations are reported.
func TestRuntimeSanityReportsSafeDefaults(t *testing.T) {
	setGrokTestOverride(t, "BaseURL", "")
	setGrokTestOverride(t, "AuthorizeURL", "")
	setGrokTestOverride(t, "TokenURL", "")
	setGrokTestOverride(t, "RedirectURI", "")
	setGrokTestOverride(t, "AllowUnsafeURLOverrides", "")
	setGrokTestOverride(t, "UnsafeAllowConcurrencyAboveOne", "")

	report := RuntimeSanity()
	require.True(t, report.BaseURL.Valid)
	require.Equal(t, DefaultBaseURL, report.BaseURL.Value)
	require.True(t, report.BaseURL.IsDefault)
	require.True(t, report.OAuthAuthorizeURL.Valid)
	require.True(t, report.OAuthTokenURL.Valid)
	require.True(t, report.OAuthRedirectURI.Valid)
	require.False(t, report.UnsafeURLOverrides)
	require.False(t, report.UnsafeHighConcurrency)
	require.Equal(t, "responses_only", report.PublicGatewayScope)
	require.Contains(t, report.ProxyPolicy, "provider_proxy_optional")
	require.Contains(t, report.ProxyPolicy, "API-key base URLs require public HTTPS")
}

// TestRuntimeSanityReportsInvalidOverridesWithoutSecrets supplies invalid URL
// overrides containing secret text and a client identifier. It asserts failed
// validation is reported while the checked display fields omit the specific
// supplied secrets.
func TestRuntimeSanityReportsInvalidOverridesWithoutSecrets(t *testing.T) {
	setGrokTestOverride(t, "BaseURL", "http://127.0.0.1:8080/v1?access_token=secret")
	setGrokTestOverride(t, "AuthorizeURL", "https://auth.example.test/oauth2/authorize")
	setGrokTestOverride(t, "TokenURL", "https://auth.example.test/oauth2/token")
	setGrokTestOverride(t, "RedirectURI", "not a url")
	setGrokTestOverride(t, "ClientID", "client-secret-like-value")
	setGrokTestOverride(t, "AllowUnsafeURLOverrides", "")

	report := RuntimeSanity()
	require.False(t, report.BaseURL.Valid)
	require.False(t, report.BaseURL.IsDefault)
	require.Contains(t, report.BaseURL.Error, "invalid url")
	require.NotContains(t, report.BaseURL.Value, "secret")
	require.False(t, report.OAuthAuthorizeURL.Valid)
	require.False(t, report.OAuthTokenURL.Valid)
	require.False(t, report.OAuthRedirectURI.Valid)
	require.NotContains(t, report.ProxyPolicy, "client-secret-like-value")
}

// TestNormalizeModelID compares omitted, whitespace-padded, aliased,
// suffixed, and vendor-qualified IDs. It asserts only blank input selects the
// Responses default and nonblank IDs retain their full spelling after
// surrounding whitespace is trimmed.
func TestNormalizeModelID(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"": DefaultResponsesModel,

		"   ": DefaultResponsesModel,

		"  grok  ": "grok",

		"grok-latest": "grok-latest",

		"grok-4.5-latest": "grok-4.5-latest",

		"grok-build": "grok-build",

		"grok-build-latest": "grok-build-latest",

		"grok-composer": "grok-composer",

		"composer-2.5": "composer-2.5",

		"grok-4.20-reasoning": "grok-4.20-reasoning",

		"grok-4.20-non-reasoning": "grok-4.20-non-reasoning",

		"grok-4.5": "grok-4.5",

		"  grok-4.3  ": "grok-4.3",

		"grok-4.20-multi-agent-0309": "grok-4.20-multi-agent-0309",

		"grok-imagine-image-quality": "grok-imagine-image-quality",

		"GROK": "GROK",

		"xai/grok": "xai/grok",

		"  custom-grok-model  ": "custom-grok-model",
	}
	for input, want := range tests {
		require.Equal(t, want, NormalizeModelID(input), input)
	}
}
