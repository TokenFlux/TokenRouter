package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// dingtalk_oauth.go builds DingTalk identity claims and pending account-choice
// decisions.
// DingTalk authorization uses the global unionID for the canonical identity
// while retaining organization-local staff information as separate profile
// and audit observations. A missing directory profile can therefore leave
// email or staff identifiers empty without replacing the global subject.
//
// Organization policy acceptance and actual membership lookup run at separate
// boundaries. Pending choice construction selects compatible account hints,
// then registration blocking can force the binding step regardless of those
// hints. It retains caller-owned claim storage and target ID pointers through
// publication. The first department claim records supplied data, while
// profile synchronization independently chooses its usable department path
// and applies its configured fields.

// DingTalkOAuthOptions carries application, organization and profile-sync
// configuration for provider authorization.
type DingTalkOAuthOptions struct {
	Enabled             bool   `yaml:"enabled"`               // configured DingTalk login switch
	ClientID            string `yaml:"client_id"`             // upstream application client identifier
	ClientSecret        string `yaml:"client_secret"`         // upstream application client credential
	AuthorizeURL        string `yaml:"authorize_url"`         // provider browser authorization endpoint
	TokenURL            string `yaml:"token_url"`             // provider token exchange endpoint
	UserInfoURL         string `yaml:"userinfo_url"`          // provider identity lookup endpoint
	Scopes              string `yaml:"scopes"`                // requested provider authorization scopes
	RedirectURL         string `yaml:"redirect_url"`          // registered backend authorization callback
	FrontendRedirectURL string `yaml:"frontend_redirect_url"` // browser authorization return destination
	// DingTalkAppKind must be internal_app for enabled settings under
	// authconfig.ValidateDingTalkConfig. Disabled drafts can remain incomplete
	// while the administrator configures them.
	DingTalkAppKind string `yaml:"dingtalk_app_kind"`
	// AppType selects public or internal application behavior. Configuration
	// defaults use public, while internal_only policy requires the internal
	// application type.
	AppType string `yaml:"app_type"`
	// CorpRestrictionPolicy selects none or internal_only membership handling.
	// DingTalkCorpAllowed accepts those labels without comparing corpID; the
	// callback performs the required directory lookup.
	CorpRestrictionPolicy string `yaml:"corp_restriction_policy"`
	InternalCorpID        string `yaml:"internal_corp_id"` // configured organization identifier hint
	// BypassRegistration permits the global-registration exemption only with
	// internal_only policy. Validated provider settings and the registration
	// observation still come from the calling authentication flow.
	BypassRegistration bool `yaml:"bypass_registration"`
	// SyncCorpEmail, SyncDisplayName and SyncDept select independent attribute
	// writes in DingTalkProfileSync. The OAuth settings reader disables these
	// organization-directory selections outside internal_only policy.
	SyncCorpEmail           bool   `yaml:"sync_corp_email"`             // corporate-email attribute synchronization switch
	SyncDisplayName         bool   `yaml:"sync_display_name"`           // staff-name attribute synchronization switch
	SyncDept                bool   `yaml:"sync_dept"`                   // department attribute synchronization switch
	SyncCorpEmailAttrKey    string `yaml:"sync_corp_email_attr_key"`    // corporate-email destination attribute key
	SyncDisplayNameAttrKey  string `yaml:"sync_display_name_attr_key"`  // staff-name destination attribute key
	SyncDeptAttrKey         string `yaml:"sync_dept_attr_key"`          // department destination attribute key
	SyncCorpEmailAttrName   string `yaml:"sync_corp_email_attr_name"`   // corporate-email destination display label
	SyncDisplayNameAttrName string `yaml:"sync_display_name_attr_name"` // staff-name destination display label
	SyncDeptAttrName        string `yaml:"sync_dept_attr_name"`         // department destination display label
	RequireEmail            bool   `yaml:"require_email"`               // configured upstream-email requirement flag
	UsernameOverwritePolicy string `yaml:"username_overwrite_policy"`   // configured username replacement policy
	UsernameAttributeKey    string `yaml:"username_attribute_key"`      // configured username destination attribute
	EnableAttributeMatching bool   `yaml:"enable_attribute_matching"`   // configured attribute matching switch
	EnableAttributeSync     bool   `yaml:"enable_attribute_sync"`       // configured attribute synchronization switch
	// AttributeSyncFields retains the supplied configuration slice during
	// ordinary option copying. Callers keep that shared field selection stable
	// while a consumer reads it.
	AttributeSyncFields          []string `yaml:"attribute_sync_fields"`
	AttributeSyncOverwritePolicy string   `yaml:"attribute_sync_overwrite_policy"` // configured attribute replacement policy
}

// DingTalkUserTokenResp carries upstream user credentials and an optional
// organization observation.
type DingTalkUserTokenResp struct {
	AccessToken  string `json:"accessToken"`  // upstream user access credential
	RefreshToken string `json:"refreshToken"` // upstream user renewal credential
	ExpireIn     int64  `json:"expireIn"`     // seconds
	// CorpID retains the organization hint returned by authorization and may be
	// empty. DingTalkCorpAllowed does not compare it to InternalCorpID before
	// membership lookup.
	CorpID string `json:"corpId"`
}

// DingTalkAPIError records provider failure code, message and its observed
// HTTP status.
type DingTalkAPIError struct {
	Code    string // upstream provider error code
	Message string // upstream provider error message
	HTTP    int    // upstream response HTTP status
}

// DingTalkOAuthClient exposes user-token and organization-directory
// operations through one provider instance. The provider implementation owns
// its application-token cache.
type DingTalkOAuthClient interface {
	// ExchangeCodeForUserToken exchanges a browser authorization code for
	// upstream user credentials. The caller uses that access token for global
	// unionID lookup; provider/transport errors remain errors before local
	// identity decisions.
	ExchangeCodeForUserToken(context.Context, string) (*DingTalkUserTokenResp, error)
	// GetUnionIdByUserToken returns the global unionID and OAuth nickname for a
	// user access token. The caller retains the canonical subject separately
	// from organization staff IDs and handles failed upstream identity lookup.
	GetUnionIdByUserToken(context.Context, string) (string, string, error)
	// GetUserIdByUnionId resolves a global unionID under the application-token
	// directory. The HTTP callback uses that organization-local result for
	// internal membership and staff lookup; an error remains a provider failure.
	GetUserIdByUnionId(context.Context, string) (string, error)
	// GetStaffInfoByUserId reads organization-local profile and department
	// observations for a staff identifier. The callback supplies a separate
	// global unionID for canonical binding and handles unavailable or rejected
	// directory reads before draft construction.
	GetStaffInfoByUserId(context.Context, string) (*DingTalkProfileSnapshot, error)
	DingTalkDepartmentReader // shared directory department lookups
}

// Error formats the provider code, message and HTTP status as a diagnostic
// string.
func (e *DingTalkAPIError) Error() string {
	return fmt.Sprintf("dingtalk api error code=%s msg=%s http=%d", e.Code, e.Message, e.HTTP)
}

// DingTalkSyntheticEmail formats the reserved compatibility address from
// trimmed lowercased identity text. It does not reject blank input or
// validate mailbox syntax, and callers supply the global unionID rather than
// the organization-local staff ID.
func DingTalkSyntheticEmail(userID string) string {
	return "dingtalk-" + strings.ToLower(strings.TrimSpace(userID)) + DingTalkConnectSyntheticEmailDomain
}

// DingTalkBindLoginCompletionResponse builds the existing-account binding
// step with creation disabled. It retains the supplied redirect text
// unchanged; the caller validates browser destinations and later completes
// credential or second-factor proof before binding.
func DingTalkBindLoginCompletionResponse(redirectTo string) map[string]any {
	return map[string]any{
		"step":                      "bind_login_required",
		"existing_account_bindable": true,
		"create_account_allowed":    false,
		"redirect":                  redirectTo,
	}
}

// DingTalkUpstreamClaims copies a nonnil staff snapshot into provider
// identity/profile observations. unionID supplies the canonical subject and
// its provider-specific duplicate, while staff.UserID remains separate
// organization-local diagnostic data; unavailable directory fields can come
// from an empty snapshot.
//
// The claim map records the first supplied department ID or zero when none
// exists. Profile synchronization makes its own usable-department selection,
// so this claim does not identify that later selected path. The returned map
// is new and contains scalar values read without trimming; callers establish
// consistent unionID/corpID observations before constructing it.
func DingTalkUpstreamClaims(staff *DingTalkProfileSnapshot, unionID, corpID string) map[string]any {
	primaryDeptID := int64(0)
	// Record the first supplied department for upstream diagnostics; profile
	// synchronization performs its own separate usable-department choice when
	// building the destination path.
	if len(staff.DeptIDs) > 0 {
		primaryDeptID = staff.DeptIDs[0]
	}
	return map[string]any{
		"email":           staff.Email,
		"username":        staff.Name,
		"nickname":        staff.Nickname,
		"subject":         unionID,      // canonical global provider subject
		"corp_user_id":    staff.UserID, // organization-local staff ID; empty in the unavailable-directory view
		"union_id":        unionID,
		"corp_id":         corpID,
		"primary_dept_id": primaryDeptID, // first supplied department identifier
	}
}

// DingTalkCorpAllowed accepts the recognized empty, none and internal_only
// policy labels without comparing corpID. The HTTP callback enforces internal
// membership through the application-token directory lookup, so this
// predicate alone does not establish organization membership.
func DingTalkCorpAllowed(cfg DingTalkOAuthOptions, corpID string) bool {
	switch cfg.CorpRestrictionPolicy {
	case "internal_only":
		// Accept the policy label here even with absent or different corpID text;
		// the callback's application-token lookup supplies the actual membership result.
		// Validated internal_only configuration uses the internal application type.
		return true
	case "none", "":
		return true
	default:
		return false
	}
}

// DingTalkErrorCode maps wrapped provider errors into browser redirect
// labels. Codes 60011 and 60121 become corp_rejected; other or non-DingTalk
// failures use upstream_error, preserving one redirect category without
// exposing the diagnostic error text. A matched DingTalkAPIError must be
// nonnil.
func DingTalkErrorCode(err error) string {
	var apiErr *DingTalkAPIError
	// Non-DingTalk failures have no provider membership code to classify, so
	// return the generic upstream redirect category without displaying their raw
	// diagnostic text.
	if !errors.As(err, &apiErr) {
		return "upstream_error"
	}
	switch apiErr.Code {
	case "60011", "60121":
		return "corp_rejected"
	case "40014", "50015", "88":
		return "upstream_error"
	default:
		return "upstream_error"
	}
}

// PrepareDingTalkChoice builds account-choice controls from caller-supplied
// email, compatibility and registration observations. A blank suggestion
// falls back to the resolved hint, and a compatibility user prefills email
// controls with a positive target ID when available.
//
// Signup blocking then forces the existing-account binding step and keeps
// account creation disabled, even after compatibility prefill or forced-email
// wording. The payload's original resolved_email differs from the draft's
// selected ResolvedEmail when another hint wins. Upstream claim storage and
// the compatibility user ID pointer remain shared, while the completion map
// is new; callers keep their ownership observations aligned.
func PrepareDingTalkChoice(
	identity PendingAuthIdentityKey,
	suggestedEmail string,
	resolvedEmail string,
	redirectTo string,
	browserSessionKey string,
	upstreamClaims map[string]any,
	compatEmail string,
	compatEmailUser *User,
	forceEmailOnSignup bool,
	signupBlocked bool,
) OAuthPendingDraft {
	suggestionEmail := strings.TrimSpace(suggestedEmail)
	canonicalEmail := strings.TrimSpace(resolvedEmail)
	// A missing suggestion uses the resolved hint until a compatibility account
	// supplies its email; this selects display state without another account
	// lookup.
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
		"create_account_allowed":    !signupBlocked,
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
	// Registration blocking overrides earlier choice wording and forces binding;
	// compatibility prefill remains visible, but its presence cannot enable creation in this payload.
	if signupBlocked {
		completionResponse["step"] = "bind_login_required"
		completionResponse["existing_account_bindable"] = true
		completionResponse["choice_reason"] = "signup_blocked_redirect_to_bind"
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

// DingTalkSignupBlocked reports whether the supplied global registration
// switch blocks signup under DingTalk bypass policy. Bypass works only for
// exact internal_only policy; the predicate does not read cfg.Enabled or
// credentials, and HTTP callers choose when to sample the registration
// switch.
func DingTalkSignupBlocked(cfg DingTalkOAuthOptions, enabled bool) bool {
	return !enabled && (!cfg.BypassRegistration || cfg.CorpRestrictionPolicy != "internal_only")
}
