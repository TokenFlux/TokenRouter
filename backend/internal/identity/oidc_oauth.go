package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"strings"
)

// oidc_oauth.go builds OIDC compatibility identifiers and pending account
// choices from issuer, subject, and profile observations.
// OIDC identities retain issuer and subject as distinct observations even
// when legacy accounts use a derived placeholder email. The compatibility
// seed normalizes issuer text separately from case-sensitive subject text,
// and its hash supports that older email-based lookup without exposing the
// raw components.
//
// Pending choices keep upstream suggestions, selected local-account hints and
// browser progression separate. A verified-email constructor rewrites the
// metadata email while retaining a differing original value in its
// compatibility field, but trusts the caller's verification decision. Options
// and claim models carry the provider validation inputs; local account
// creation, binding and token issuance remain with the authentication flow.

// OIDCOAuthOptions carries provider configuration and explicit startup-policy
// markers for OIDC authorization.
type OIDCOAuthOptions struct {
	Enabled      bool   `yaml:"enabled"`       // configured OIDC login switch
	ProviderName string `yaml:"provider_name"` // displayed identity provider name
	ClientID     string `yaml:"client_id"`     // upstream authorization client identifier
	ClientSecret string `yaml:"client_secret"` // upstream authorization client credential
	IssuerURL    string `yaml:"issuer_url"`    // expected token issuer URL
	DiscoveryURL string `yaml:"discovery_url"` // provider metadata discovery endpoint
	AuthorizeURL string `yaml:"authorize_url"` // provider browser authorization endpoint
	TokenURL     string `yaml:"token_url"`     // provider token exchange endpoint
	UserInfoURL  string `yaml:"userinfo_url"`  // provider identity lookup endpoint
	JWKSURL      string `yaml:"jwks_url"`      // provider signature-key lookup endpoint
	// Scopes carries requested authorization scopes. Startup defaults use openid
	// email profile, and runtime OIDC configuration requires the openid scope.
	Scopes      string `yaml:"scopes"`
	RedirectURL string `yaml:"redirect_url"` // registered backend authorization callback
	// FrontendRedirectURL supplies the browser return route. Startup defaults
	// use /auth/oidc/callback; current OAuth settings can supply a different
	// configured destination.
	FrontendRedirectURL string `yaml:"frontend_redirect_url"`
	TokenAuthMethod     string `yaml:"token_auth_method"` // client_secret_post / client_secret_basic / none
	// UsePKCE and ValidateIDToken pair with UsePKCEExplicit and
	// ValidateIDTokenExplicit for startup compatibility defaults. The markers
	// distinguish an explicitly disabled policy from an implicit false value
	// when no stored switch overrides it.
	UsePKCE                 bool `yaml:"use_pkce"`          // authorization code proof switch
	ValidateIDToken         bool `yaml:"validate_id_token"` // provider ID-token validation switch
	UsePKCEExplicit         bool `yaml:"-"`                 // explicit PKCE startup choice
	ValidateIDTokenExplicit bool `yaml:"-"`                 // explicit ID-token startup choice
	// AllowedSigningAlgs names comma-separated JWT methods. Startup and direct
	// blank-input parsing use RS256,ES256,PS256; runtime validation requires a
	// nonblank list when ID-token checks are enabled.
	AllowedSigningAlgs string `yaml:"allowed_signing_algs"`
	// ClockSkewSeconds is token-time tolerance in seconds. Startup configuration
	// defaults to 120, while runtime settings accept explicit zero within their
	// allowed range.
	ClockSkewSeconds     int  `yaml:"clock_skew_seconds"`
	RequireEmailVerified bool `yaml:"require_email_verified"` // require verified email claims
	// UserInfoEmailPath, UserInfoIDPath and UserInfoUsernamePath identify
	// separate userinfo lookup paths. Empty configured paths use common field
	// names for their respective profile values.
	UserInfoEmailPath    string `yaml:"userinfo_email_path"`    // provider mailbox lookup path
	UserInfoIDPath       string `yaml:"userinfo_id_path"`       // provider subject lookup path
	UserInfoUsernamePath string `yaml:"userinfo_username_path"` // provider username lookup path
}

// OIDCTokenResponse contains upstream access credentials and optional OIDC
// token claims.
type OIDCTokenResponse struct {
	AccessToken  string `json:"access_token"`            // upstream bearer access credential
	TokenType    string `json:"token_type"`              // upstream token type label
	ExpiresIn    int64  `json:"expires_in"`              // seconds
	RefreshToken string `json:"refresh_token,omitempty"` // optional upstream renewal credential
	Scope        string `json:"scope,omitempty"`         // granted upstream authorization scopes
	IDToken      string `json:"id_token,omitempty"`      // upstream signed identity token
}

// OIDCTokenExchangeError retains failed exchange status, provider messages
// and diagnostic response text.
type OIDCTokenExchangeError struct {
	StatusCode          int    // token exchange HTTP status
	ProviderError       string // raw provider error code
	ProviderDescription string // raw provider error description
	// Body retains failed response text for diagnostics. Error emits the status
	// and normalized provider messages, leaving this payload out of its display
	// result.
	Body string
}

// OIDCUserInfoClaims represents identity and profile hints decoded from the
// userinfo response.
type OIDCUserInfoClaims struct {
	Email    string // upstream account email hint
	Username string // upstream username profile hint
	Subject  string // upstream identity subject identifier
	// EmailVerified distinguishes an absent verification claim from explicit
	// true or false. Callback email-policy and automatic email-binding decisions
	// inspect that distinction.
	EmailVerified *bool
	DisplayName   string // upstream display name hint
	AvatarURL     string // upstream profile image URL
}

// OIDCVerifiedClaims carries identity and profile values parsed from a
// successfully verified ID token.
type OIDCVerifiedClaims struct {
	// Issuer, Subject, Email, PreferredUsername and Name come from the same
	// verified token. Signature/issuer validation establishes their token
	// source; optional profile strings can still be empty.
	// Issuer: verified token issuer text. Subject: verified token subject text.
	// Email: signed email address claim. PreferredUsername: signed preferred
	// username claim. Name: signed display name claim.
	Issuer, Subject, Email, PreferredUsername, Name string
	// EmailVerified retains absent versus explicit verification state from that
	// token. The callback decides whether it satisfies required-email or
	// automatic-binding policy.
	EmailVerified *bool
}

// OIDCOAuthClient exchanges credentials, extracts userinfo and validates
// provider identity tokens.
type OIDCOAuthClient interface {
	// ExchangeCode requests upstream credentials using the configured provider,
	// authorization code, callback URI and PKCE verifier. The caller owns saved
	// browser state and local account decisions; provider or transport failures
	// remain errors.
	ExchangeCode(context.Context, OIDCOAuthOptions, string, string, string) (*OIDCTokenResponse, error)
	// FetchUserInfo returns decoded identity/profile hints using the supplied
	// upstream credential and lookup configuration. Callers align those
	// observations with any verified ID-token subject before local binding and
	// handle provider or decoding failures.
	FetchUserInfo(context.Context, OIDCOAuthOptions, *OIDCTokenResponse) (*OIDCUserInfoClaims, error)
	// ValidateIDToken checks the supplied JWT against configured issuer,
	// audience, signature methods and an optional expected nonce. Empty nonce
	// expectation skips that comparison in the provider adapter; local account
	// binding remains a caller responsibility.
	ValidateIDToken(context.Context, OIDCOAuthOptions, string, string) (*OIDCVerifiedClaims, error)
}

// Error renders the token-exchange status and nonblank trimmed provider
// messages. A nil error receiver yields empty text, and Body is retained
// separately rather than included in this display result.
func (e *OIDCTokenExchangeError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("token exchange status=%d", e.StatusCode)}
	if strings.TrimSpace(e.ProviderError) != "" {
		parts = append(parts, "error="+strings.TrimSpace(e.ProviderError))
	}
	if strings.TrimSpace(e.ProviderDescription) != "" {
		parts = append(parts, "error_description="+strings.TrimSpace(e.ProviderDescription))
	}
	return strings.Join(parts, " ")
}

// OIDCIdentityKey joins issuer and subject into the compatibility-email seed.
// It lowercases the entire trimmed issuer text and preserves the trimmed
// subject's case, with a raw unit-separator character between those
// components.
//
// The helper performs string normalization rather than URL canonicalization,
// and neither component is escaped. Callers supply the same verified
// issuer/subject pair used by the identity lookup. Changing this
// normalization or separator changes the derived placeholder email, which
// older-account lookup uses to locate that compatible representation.
func OIDCIdentityKey(issuer, subject string) string {
	issuer = strings.TrimSpace(strings.ToLower(issuer))
	subject = strings.TrimSpace(subject)
	// Keep subject case distinct while normalizing issuer text in this legacy
	// seed; changing that representation would change compatibility-email
	// candidates for the same supplied identity pair.
	return issuer + "\x1f" + subject
}

// OIDCSyntheticEmailFromIdentityKey derives the reserved compatibility
// address from a trimmed seed. Blank input returns empty text; nonblank input
// uses the first sixteen SHA-256 bytes, giving a deterministic placeholder
// without embedding the raw issuer and subject.
func OIDCSyntheticEmailFromIdentityKey(identityKey string) string {
	identityKey = strings.TrimSpace(identityKey)
	if identityKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(identityKey))
	// Sixteen digest bytes retain the legacy compatibility-address suffix format
	// used when looking up older accounts by their derived placeholder email.
	return "oidc-" + hex.EncodeToString(sum[:16]) + OIDCConnectSyntheticEmailDomain
}

// OIDCFallbackUsername builds a bounded display fallback from a trimmed
// subject. Blank input uses oidc_user; other input contributes twelve
// hexadecimal SHA-256 characters, so this label is a display hint rather than
// an account lookup key.
func OIDCFallbackUsername(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "oidc_user"
	}
	sum := sha256.Sum256([]byte(subject))
	// Twelve digest characters bound the display fallback independently of subject length.
	return "oidc_" + hex.EncodeToString(sum[:])[:12]
}

// PrepareOIDCChoice builds the frontend account-choice draft from supplied
// email and compatibility observations. A blank suggestion uses the resolved
// hint; a compatibility user prefills its email and can supply a positive
// target ID. Forced email changes the reason only without that match.
//
// The payload retains its original resolved_email hint, while the draft's
// ResolvedEmail follows the selected display or compatibility email. These
// separate views do not perform lookup or prove ownership. The completion map
// is new, but upstream claims and a compatibility user's ID pointer remain
// shared; callers keep their verified identity and chosen account
// observations aligned through publication.
func PrepareOIDCChoice(
	identity PendingAuthIdentityKey,
	suggestedEmail string,
	resolvedEmail string,
	redirectTo string,
	browserSessionKey string,
	upstreamClaims map[string]any,
	compatEmail string,
	compatEmailUser *User,
	forceEmailOnSignup bool,
) OAuthPendingDraft {
	suggestionEmail := strings.TrimSpace(suggestedEmail)
	canonicalEmail := strings.TrimSpace(resolvedEmail)
	// A blank provider suggestion uses the resolved hint for display;
	// compatibility-account selection can subsequently replace the draft email
	// without replacing that original payload hint.
	if suggestionEmail == "" {
		suggestionEmail = canonicalEmail
	}

	completionResponse := map[string]any{
		"step":                      OAuthPendingChoiceStep,
		"adoption_required":         true,
		"redirect":                  strings.TrimSpace(redirectTo),
		"email":                     suggestionEmail,
		"resolved_email":            canonicalEmail,
		"existing_account_email":    "",
		"existing_account_bindable": false,
		"create_account_allowed":    true,
		"force_email_on_signup":     forceEmailOnSignup,
		"choice_reason":             "third_party_signup",
	}
	if strings.TrimSpace(compatEmail) != "" {
		completionResponse["compat_email"] = strings.TrimSpace(compatEmail)
	}
	if compatEmailUser != nil {
		completionResponse["email"] = strings.TrimSpace(compatEmailUser.Email)
		completionResponse["existing_account_email"] = strings.TrimSpace(compatEmailUser.Email)
		completionResponse["existing_account_bindable"] = true
		completionResponse["choice_reason"] = "compat_email_match"
	}
	if forceEmailOnSignup && compatEmailUser == nil {
		completionResponse["choice_reason"] = "force_email_on_signup"
	}

	resolvedChoiceEmail := suggestionEmail
	if compatEmailUser != nil {
		resolvedChoiceEmail = strings.TrimSpace(compatEmailUser.Email)
	}
	var targetUserID *int64
	if compatEmailUser != nil && compatEmailUser.ID > 0 {
		targetUserID = &compatEmailUser.ID
	}

	return OAuthPendingDraft{
		Intent:                 OAuthIntentLogin,
		Identity:               identity,
		TargetUserID:           targetUserID,
		ResolvedEmail:          resolvedChoiceEmail,
		RedirectTo:             redirectTo,
		BrowserSessionKey:      browserSessionKey,
		UpstreamIdentityClaims: upstreamClaims,
		CompletionResponse:     completionResponse,
	}
}

// OIDCVerifiedEmailIdentity prepares automatic email OAuth input using a
// caller-verified mailbox. It lowercases and trims the supplied email, marks
// EmailVerified=true without performing verification, and retains the
// identity triple with trimmed profile hints.
//
// The metadata map is a new shallow copy. A different nonblank upstream email
// is retained under synthetic_email before the verified email replaces email,
// even when the original text is not itself a reserved placeholder. Nested
// metadata remains shared, so callers keep those values stable and establish
// verification before invoking this constructor.
func OIDCVerifiedEmailIdentity(identity PendingAuthIdentityKey, compatEmail, username string, upstreamClaims map[string]any) EmailOAuthIdentityInput {
	verifiedEmail := strings.TrimSpace(strings.ToLower(compatEmail))
	upstreamMetadata := make(map[string]any, len(upstreamClaims)+1)
	maps.Copy(upstreamMetadata, upstreamClaims)
	// Retain a differing original email before writing the verified mailbox, so
	// metadata preserves the prior compatibility observation alongside its new
	// automatic-binding value.
	if syntheticEmail := PendingSessionStringValue(upstreamClaims, "email"); syntheticEmail != "" && !strings.EqualFold(syntheticEmail, verifiedEmail) {
		upstreamMetadata["synthetic_email"] = syntheticEmail
	}
	upstreamMetadata["email"] = verifiedEmail
	input := EmailOAuthIdentityInput{
		ProviderType:     strings.TrimSpace(identity.ProviderType),
		ProviderKey:      strings.TrimSpace(identity.ProviderKey),
		ProviderSubject:  strings.TrimSpace(identity.ProviderSubject),
		Email:            verifiedEmail,
		EmailVerified:    true,
		Username:         strings.TrimSpace(username),
		DisplayName:      PendingSessionStringValue(upstreamClaims, "suggested_display_name"),
		AvatarURL:        PendingSessionStringValue(upstreamClaims, "suggested_avatar_url"),
		UpstreamMetadata: upstreamMetadata,
	}
	return input
}
