package codeassist

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/oauthpkce"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// oauth.go selects Gemini OAuth credentials and scopes and builds
// authorization URLs with PKCE parameters.
// Gemini authorization can use a configured Google OAuth client or the
// built-in Gemini CLI identity. Credential-pair presence and OAuth type
// determine which scopes are sent, and a partially configured client is
// rejected before constructing consent URLs or token requests.
//
// Configured scopes are normalized from comma or whitespace separation. The
// built-in client filters Generative Language and Drive prefixes for AI Studio
// and Google One; if filtering removes every scope, Code Assist defaults
// supply a usable configured scope set. Custom clients retain those scopes,
// and AI Studio rewrites the exact legacy Generative Language scope to the
// retriever spelling after selection.

const (
	// AIStudioBaseURL and GeminiCliBaseURL select the REST and internal account
	// endpoints used by request builders and Code Assist clients.
	AIStudioBaseURL  = "https://generativelanguage.googleapis.com"
	GeminiCliBaseURL = "https://cloudcode-pa.googleapis.com"

	// AuthorizeURL and TokenURL identify the Google consent and token-exchange
	// endpoints used by the authorization URL and OAuth client.
	AuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	TokenURL     = "https://oauth2.googleapis.com/token"

	// AIStudioOAuthRedirectURI supplies the default loopback callback for the AI
	// Studio authorization form. The selected OAuth client determines which
	// callback is registered.
	AIStudioOAuthRedirectURI = "http://localhost:1455/auth/callback"

	// DefaultCodeAssistScopes requests cloud-platform and profile scopes when
	// scope input is absent or built-in filtering leaves no scopes for the
	// selected OAuth variant.
	DefaultCodeAssistScopes = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"

	// DefaultAIStudioScopes supplies cloud-platform and retriever scope text for
	// a custom AI Studio client when callers omit their configured scopes.
	// See https://ai.google.dev/gemini-api/docs/oauth.
	DefaultAIStudioScopes = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/generative-language.retriever"

	// GeminiCLIRedirectURI names the Code Assist callback selected for the
	// built-in Gemini CLI authorization flow that returns a copyable code.
	GeminiCLIRedirectURI = "https://codeassist.google.com/authcode"

	// GeminiCLIOAuthClientID and GeminiCLIOAuthClientSecret select the built-in
	// CLI identity. EffectiveOAuthConfig applies its scope-filter policy by
	// comparing this client ID.
	GeminiCLIOAuthClientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	GeminiCLIOAuthClientSecret = "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"

	// GeminiCLIUserAgent supplies the fixed client identity sent with Code Assist
	// and Resource Manager requests and wrapped internal generation requests.
	GeminiCLIUserAgent = "GeminiCLI/0.1.5 (Windows; AMD64)"
)

// OAuthConfig carries the client credential pair and requested authorization
// scope text.
type OAuthConfig = google.OAuthConfig

// GenerateRandomBytes returns n bytes through the shared OAuth random helper.
// Zero returns an empty slice and negative n panics during allocation; the
// backend crypto/rand runtime fills the buffer or terminates on entropy
// failure.
func GenerateRandomBytes(n int) ([]byte, error) {
	return oauthpkce.RandomBytes(n)
}

// GenerateState returns unpadded base64url state derived from thirty-two
// random bytes.
func GenerateState() (string, error) {
	bytes, err := GenerateRandomBytes(32)
	if err != nil {
		return "", err
	}
	return base64URLEncode(bytes), nil
}

// GenerateSessionID returns thirty-two lowercase hexadecimal characters from
// sixteen random bytes.
func GenerateSessionID() (string, error) {
	bytes, err := GenerateRandomBytes(16)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// GenerateCodeVerifier returns the shared forty-three-character unpadded
// base64url PKCE verifier.
func GenerateCodeVerifier() (string, error) {
	return oauthpkce.Verifier()
}

// GenerateCodeChallenge returns the unpadded base64url SHA-256 digest of
// verifier text.
func GenerateCodeChallenge(verifier string) string {
	return oauthpkce.Challenge(verifier)
}

// base64URLEncode returns bytes encoded with the unpadded URL-safe base64
// alphabet.
func base64URLEncode(data []byte) string {
	return oauthpkce.Base64URL(data)
}

// EffectiveOAuthConfig resolves a trimmed credential pair and normalized
// scopes for oauthType. An empty pair selects the built-in CLI identity, a
// partial pair returns an input error, and the environment secret is
// considered only when the built-in secret is empty.
//
// Absent scopes select variant defaults, with Code Assist scopes used for an
// AI Studio built-in client. Existing scopes for that client are filtered by
// prefix for AI Studio and Google One, and an empty filtered set returns to
// Code Assist defaults. Custom clients keep those scope values; AI Studio then
// rewrites the exact legacy Generative Language token to its retriever
// spelling.
func EffectiveOAuthConfig(cfg OAuthConfig, oauthType string) (OAuthConfig, error) {
	effective := OAuthConfig{
		ClientID:     strings.TrimSpace(cfg.ClientID),
		ClientSecret: strings.TrimSpace(cfg.ClientSecret),
		Scopes:       strings.TrimSpace(cfg.Scopes),
	}

	// Normalize commas and whitespace before scope selection so both
	// configuration input forms produce the same space-separated tokens for
	// filtering and consent requests.
	if effective.Scopes != "" {
		effective.Scopes = strings.Join(strings.Fields(strings.ReplaceAll(effective.Scopes, ",", " ")), " ")
	}

	// Choose the built-in identity only for an absent credential pair; a
	// partially configured pair is rejected so configured and built-in
	// credentials cannot be mixed.
	if effective.ClientID == "" && effective.ClientSecret == "" {
		secret := strings.TrimSpace(GeminiCLIOAuthClientSecret)
		if secret == "" {
			return OAuthConfig{}, infraerrors.Newf(infraerrors.CategoryBadRequest, "GEMINI_CLI_OAUTH_CLIENT_SECRET_MISSING", "built-in Gemini CLI OAuth client_secret is not configured; provide a custom OAuth client")
		}
		effective.ClientID = GeminiCLIOAuthClientID
		effective.ClientSecret = secret
	} else if effective.ClientID == "" || effective.ClientSecret == "" {
		return OAuthConfig{}, infraerrors.New(infraerrors.CategoryBadRequest, "GEMINI_OAUTH_CLIENT_NOT_CONFIGURED", "OAuth client not configured: please set both client_id and client_secret (or leave both empty to use the built-in Gemini CLI client)")
	}

	isBuiltinClient := effective.ClientID == GeminiCLIOAuthClientID

	if effective.Scopes == "" {
		switch oauthType {
		case "ai_studio":
			// Select Code Assist scopes for the built-in AI Studio identity because
			// this client ID is the target of the local restricted-scope filter.
			if isBuiltinClient {
				effective.Scopes = DefaultCodeAssistScopes
			} else {
				effective.Scopes = DefaultAIStudioScopes
			}
		case "google_one":
			// Google One selects the local Code Assist scope defaults.
			effective.Scopes = DefaultCodeAssistScopes
		default:
			effective.Scopes = DefaultCodeAssistScopes
		}
	} else if (oauthType == "ai_studio" || oauthType == "google_one") && isBuiltinClient {
		// Apply the built-in client scope policy before deciding whether any
		// configured scopes remain; an entirely filtered list must receive the same
		// defaults as absent scope input.
		parts := strings.Fields(effective.Scopes)
		filtered := make([]string, 0, len(parts))
		for _, s := range parts {
			if hasRestrictedScope(s) {
				continue
			}
			filtered = append(filtered, s)
		}
		if len(filtered) == 0 {
			effective.Scopes = DefaultCodeAssistScopes
		} else {
			effective.Scopes = strings.Join(filtered, " ")
		}
	}

	// Rewrite the exact legacy AI Studio scope after client filtering so retained
	// custom-client requests use the retriever token while other scope spellings
	// stay intact.
	if oauthType == "ai_studio" && effective.Scopes != "" {
		parts := strings.Fields(effective.Scopes)
		for i := range parts {
			if parts[i] == "https://www.googleapis.com/auth/generative-language" {
				parts[i] = "https://www.googleapis.com/auth/generative-language.retriever"
			}
		}
		effective.Scopes = strings.Join(parts, " ")
	}

	return effective, nil
}

// hasRestrictedScope reports whether a scope begins with either built-in
// filtering prefix.
func hasRestrictedScope(scope string) bool {
	return strings.HasPrefix(scope, "https://www.googleapis.com/auth/generative-language") ||
		strings.HasPrefix(scope, "https://www.googleapis.com/auth/drive")
}

// BuildAuthorizationURL returns the Google consent URL with resolved
// credentials/scopes and S256 PKCE parameters. redirectURI is trimmed and
// required; optional projectID is trimmed, while callers supply state and
// challenge values for their authorization session.
func BuildAuthorizationURL(cfg OAuthConfig, state, codeChallenge, redirectURI, projectID, oauthType string) (string, error) {
	effectiveCfg, err := EffectiveOAuthConfig(cfg, oauthType)
	if err != nil {
		return "", err
	}
	redirectURI = strings.TrimSpace(redirectURI)
	if redirectURI == "" {
		return "", fmt.Errorf("redirect_uri is required")
	}

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", effectiveCfg.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", effectiveCfg.Scopes)
	params.Set("state", state)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	params.Set("access_type", "offline")
	params.Set("prompt", "consent")
	params.Set("include_granted_scopes", "true")
	if strings.TrimSpace(projectID) != "" {
		params.Set("project_id", strings.TrimSpace(projectID))
	}

	return fmt.Sprintf("%s?%s", AuthorizeURL, params.Encode()), nil
}
