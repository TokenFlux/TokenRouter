package identity

import (
	"context"
	"fmt"
	"strings"
)

// linuxdo_oauth.go builds LinuxDo pending account choices and defines the
// provider exchange inputs and results.
// LinuxDo pending choices distinguish provider email suggestions from the
// email associated with a compatible local account. A compatibility match can
// establish a target identifier, while a new account requiring local email
// verification clears the supplied hints and requests the creation step.
//
// The builder selects those controls from caller observations without loading
// or mutating accounts. It retains upstream claim storage and a supplied
// compatibility user's ID pointer, so publication requires stable matching
// data. Token and profile interfaces keep upstream credentials separate from
// platform sessions, and token-exchange errors expose status/messages while
// retaining raw response text only in their diagnostic field.

// LinuxDoOAuthOptions supplies provider endpoints, credentials and userinfo
// extraction hints for authorization.
type LinuxDoOAuthOptions struct {
	Enabled      bool   `yaml:"enabled"`       // configured LinuxDo login switch
	ClientID     string `yaml:"client_id"`     // upstream authorization client identifier
	ClientSecret string `yaml:"client_secret"` // upstream authorization client credential
	AuthorizeURL string `yaml:"authorize_url"` // provider browser authorization endpoint
	TokenURL     string `yaml:"token_url"`     // provider token exchange endpoint
	UserInfoURL  string `yaml:"userinfo_url"`  // provider identity lookup endpoint
	Scopes       string `yaml:"scopes"`        // requested provider authorization scopes
	RedirectURL  string `yaml:"redirect_url"`  // registered backend authorization callback
	// FrontendRedirectURL selects the browser return destination. OAuth settings
	// can override the built-in /auth/linuxdo/callback route through startup or
	// stored configuration.
	FrontendRedirectURL string `yaml:"frontend_redirect_url"`
	TokenAuthMethod     string `yaml:"token_auth_method"` // client_secret_post / client_secret_basic / none
	UsePKCE             bool   `yaml:"use_pkce"`          // authorization code proof switch
	// UserInfoEmailPath, UserInfoIDPath and UserInfoUsernamePath select dotted
	// object lookups for their separate profile values. Empty paths use common
	// provider field names during profile extraction.
	UserInfoEmailPath    string `yaml:"userinfo_email_path"`    // provider mailbox lookup path
	UserInfoIDPath       string `yaml:"userinfo_id_path"`       // provider subject lookup path
	UserInfoUsernamePath string `yaml:"userinfo_username_path"` // provider username lookup path
}

// LinuxDoTokenResponse holds provider access credentials and their returned
// token metadata.
type LinuxDoTokenResponse struct {
	AccessToken  string `json:"access_token"`            // upstream bearer access credential
	TokenType    string `json:"token_type"`              // upstream token type label
	ExpiresIn    int64  `json:"expires_in"`              // seconds
	RefreshToken string `json:"refresh_token,omitempty"` // optional upstream renewal credential
	Scope        string `json:"scope,omitempty"`         // granted upstream authorization scopes
}

// LinuxDoTokenExchangeError retains failed exchange status, provider messages
// and diagnostic response text.
type LinuxDoTokenExchangeError struct {
	StatusCode          int    // token exchange HTTP status
	ProviderError       string // raw provider error code
	ProviderDescription string // raw provider error description
	// Body retains failed response text for diagnostics. Error emits the status
	// and normalized provider messages, leaving this payload out of its display
	// result.
	Body string
}

// LinuxDoOAuthClient exchanges authorization codes and extracts upstream
// identity and profile values.
type LinuxDoOAuthClient interface {
	// ExchangeCode uses the supplied options, authorization code, callback URI
	// and PKCE verifier to request upstream credentials. The caller retains
	// browser state and local account decisions; provider or transport failures
	// remain errors.
	ExchangeCode(context.Context, LinuxDoOAuthOptions, string, string, string) (*LinuxDoTokenResponse, error)
	// FetchUserInfo uses the upstream token and extraction options to return
	// email, username, subject, display name and avatar URL in that order. The
	// caller associates those hints with local identity state and handles
	// provider failures.
	FetchUserInfo(context.Context, LinuxDoOAuthOptions, *LinuxDoTokenResponse) (string, string, string, string, string, error)
}

// Error renders the token-exchange status and nonblank trimmed provider
// messages. A nil error receiver yields empty text, and Body is retained
// separately rather than included in this display result.
func (e *LinuxDoTokenExchangeError) Error() string {
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

// PrepareLinuxDoChoice builds a pending account-choice draft from suggested
// and resolved email hints. A compatibility user overrides the visible email
// and can supply a positive target ID; otherwise the suggested email falls
// back to the resolved hint.
//
// When email verification or forced email is required without a compatibility
// user, the payload switches to account creation and removes both email
// hints, clearing the draft's resolved email. Verification takes precedence
// in the displayed reason. Caller-owned upstream claims and any compatibility
// user ID pointer remain shared, while the completion map is newly
// constructed.
func PrepareLinuxDoChoice(
	identity PendingAuthIdentityKey,
	suggestedEmail string,
	resolvedEmail string,
	redirectTo string,
	browserSessionKey string,
	upstreamClaims map[string]any,
	compatEmail string,
	compatEmailUser *User,
	emailVerificationRequired bool,
	forceEmailOnSignup bool,
) OAuthPendingDraft {
	suggestionEmail := strings.TrimSpace(suggestedEmail)
	canonicalEmail := strings.TrimSpace(resolvedEmail)
	// A missing suggestion uses the resolved hint for display; a compatibility
	// match or required local-email step can later replace or clear that choice.
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
	resolvedChoiceEmail := suggestionEmail
	if compatEmailUser != nil {
		completionResponse["email"] = strings.TrimSpace(compatEmailUser.Email)
		completionResponse["existing_account_email"] = strings.TrimSpace(compatEmailUser.Email)
		completionResponse["existing_account_bindable"] = true
		completionResponse["choice_reason"] = "compat_email_match"
		resolvedChoiceEmail = strings.TrimSpace(compatEmailUser.Email)
	}
	if forceEmailOnSignup && compatEmailUser == nil {
		completionResponse["choice_reason"] = "force_email_on_signup"
	}
	// Without a compatible account, required local email proof discards upstream
	// email hints from both response and draft state so completion requests a
	// locally verified address.
	if (emailVerificationRequired || forceEmailOnSignup) && compatEmailUser == nil {
		completionResponse["step"] = "create_account_required"
		completionResponse["email_binding_required"] = true
		completionResponse["force_email_on_signup"] = true
		if emailVerificationRequired {
			completionResponse["choice_reason"] = "email_verification_required"
		}
		delete(completionResponse, "email")
		delete(completionResponse, "resolved_email")
		resolvedChoiceEmail = ""
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
