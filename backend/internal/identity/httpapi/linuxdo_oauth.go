package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/pkg/oauthpkce"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// linuxdo_oauth.go handles LinuxDo browser authorization, account
// continuation, and callback redirects.
// LinuxDo login uses the stable provider subject for account identity and a
// synthetic email for binding, while a provider email remains a compatibility
// hint. Start captures browser proof, login or current-user intent, and
// optional PKCE material; callback verifies that state before asking the
// identity flow to select an account.
//
// Existing provider ownership becomes a pending login target, and current-user
// binding retains its signed target cookie. A real-email match or local email
// policy selects account choice instead of establishing ownership from that
// hint. Eligible direct signup saves its provider binding before delivering
// credentials in a fragment; invitation-required results continue in pending
// state. Redirect helpers also retain compatibility handling for encoded local
// paths and byte-bounded fragment values.

// LinuxDo browser cookie values, intent labels, fragment bounds, and local
// defaults carry authorization state and the selected account continuation
// through provider navigation.
const (
	LinuxDoOAuthCookiePath         = "/api/v1/auth/oauth/linuxdo"
	OauthBindAccessTokenCookiePath = "/api/v1/auth/oauth"
	LinuxDoOAuthStateCookieName    = "linuxdo_oauth_state"     // LinuxDo authorization state cookie
	LinuxDoOAuthVerifierCookie     = "linuxdo_oauth_verifier"  // LinuxDo PKCE verifier cookie
	LinuxDoOAuthRedirectCookie     = "linuxdo_oauth_redirect"  // LinuxDo local return cookie
	LinuxDoOAuthIntentCookieName   = "linuxdo_oauth_intent"    // LinuxDo account intent cookie
	LinuxDoOAuthBindUserCookieName = "linuxdo_oauth_bind_user" // signed target account cookie
	OauthBindAccessTokenCookieName = "oauth_bind_access_token" // shared bind credential cookie
	LinuxDoOAuthCookieMaxAgeSec    = 10 * 60                   // seconds
	LinuxDoOAuthDefaultRedirectTo  = "/dashboard"
	LinuxDoOAuthDefaultFrontendCB  = "/auth/linuxdo/callback"

	LinuxDoOAuthMaxFragmentValueLen = 512 // bytes

	OauthIntentLogin           = "login"             // external account login intent
	OauthIntentBindCurrentUser = "bind_current_user" // authenticated account binding intent
)

// LinuxDoHandler manages subject-based LinuxDo login and browser current-user
// binding continuations.
type LinuxDoHandler struct {
	*PendingHandler                                                             // shared browser pending authentication
	binding         *OAuthBindHandler                                           // signed current-account binding helper
	client          identity.LinuxDoOAuthClient                                 // LinuxDo authorization and profile client
	loadConfig      func(context.Context) (identity.LinuxDoOAuthOptions, error) // current LinuxDo configuration callback
}

// CompleteLinuxDoOAuthRequest carries invitation and profile choices for
// legacy pending signup completion.
type CompleteLinuxDoOAuthRequest struct {
	InvitationCode string `json:"invitation_code" binding:"required"` // required registration invitation code
	AffCode        string `json:"aff_code,omitempty"`                 // optional signup affiliate code
	// AdoptDisplayName and AdoptAvatar keep omitted choices as nil and supplied
	// false values as refusals. The helper retains saved values for omitted
	// flags and otherwise defaults those flags to false before applying supplied
	// choices.
	AdoptDisplayName *bool `json:"adopt_display_name,omitempty"` // optional provider-name adoption choice
	AdoptAvatar      *bool `json:"adopt_avatar,omitempty"`       // optional provider-avatar adoption choice
}

// NewLinuxDoHandler retains shared pending and binding handlers, provider
// client, and configuration reader. Initialize dependencies for the registered
// login and binding paths before serving authorization requests.
func NewLinuxDoHandler(p *PendingHandler, b *OAuthBindHandler, client identity.LinuxDoOAuthClient, load func(context.Context) (identity.LinuxDoOAuthOptions, error)) *LinuxDoHandler {
	return &LinuxDoHandler{p, b, client, load}
}

// LinuxDoOAuthStart checks applicable action CAPTCHA and records browser state
// before returning the authorize URL. It normalizes intent, captures signup
// context, and signs the current-account target when binding is selected.
//
// A browser key replaces prior pending-session proof, and configured PKCE
// generates a verifier cookie with the matching S256 challenge. Return paths
// use the validated local default when unusable. Cookies are emitted before
// later binding, PKCE, or URL construction can fail, so a failed start can
// leave partial browser state. POST receives the authorize URL as JSON; other
// accepted methods redirect.
func (h *LinuxDoHandler) LinuxDoOAuthStart(c *gin.Context) {
	if !h.RequireActionCaptchaForOAuthLoginStart(c) {
		return
	}
	cfg, err := h.loadConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	state, err := oauthpkce.Verifier()
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_STATE_GEN_FAILED", "failed to generate oauth state").WithCause(err))
		return
	}

	redirectTo := SanitizeFrontendRedirectPath(c.Query("redirect"))
	if redirectTo == "" {
		redirectTo = LinuxDoOAuthDefaultRedirectTo
	}

	browserSessionKey, err := GenerateOAuthPendingBrowserSession()
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BROWSER_SESSION_GEN_FAILED", "failed to generate oauth browser session").WithCause(err))
		return
	}

	secureCookie := IsRequestHTTPS(c)
	// Replace browser proof and expire its prior pending token beside the new
	// LinuxDo start context. Binding and PKCE work follows, so its failures can
	// leave these already emitted cookie headers.
	LinuxDoSetCookie(c, LinuxDoOAuthStateCookieName, EncodeCookieValue(state), LinuxDoOAuthCookieMaxAgeSec, secureCookie)
	LinuxDoSetCookie(c, LinuxDoOAuthRedirectCookie, EncodeCookieValue(redirectTo), LinuxDoOAuthCookieMaxAgeSec, secureCookie)
	intent := NormalizeOAuthIntent(c.Query("intent"))
	LinuxDoSetCookie(c, LinuxDoOAuthIntentCookieName, EncodeCookieValue(intent), LinuxDoOAuthCookieMaxAgeSec, secureCookie)
	CaptureOAuthPromoCode(c, secureCookie)
	SetOAuthPendingBrowserCookie(c, browserSessionKey, secureCookie)
	ClearOAuthPendingSessionCookie(c, secureCookie)
	if intent == OauthIntentBindCurrentUser {
		bindCookieValue, err := h.binding.BuildOAuthBindUserCookieFromContext(c)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		LinuxDoSetCookie(c, LinuxDoOAuthBindUserCookieName, EncodeCookieValue(bindCookieValue), LinuxDoOAuthCookieMaxAgeSec, secureCookie)
	} else {
		LinuxDoClearCookie(c, LinuxDoOAuthBindUserCookieName, secureCookie)
	}

	codeChallenge := ""
	if cfg.UsePKCE {
		verifier, err := oauthpkce.Verifier()
		if err != nil {
			response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_PKCE_GEN_FAILED", "failed to generate pkce verifier").WithCause(err))
			return
		}
		codeChallenge = oauthpkce.Challenge(verifier)
		LinuxDoSetCookie(c, LinuxDoOAuthVerifierCookie, EncodeCookieValue(verifier), LinuxDoOAuthCookieMaxAgeSec, secureCookie)
	}

	redirectURI := strings.TrimSpace(cfg.RedirectURL)
	if redirectURI == "" {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url not configured"))
		return
	}

	authURL, err := BuildLinuxDoAuthorizeURL(cfg, state, codeChallenge, redirectURI)
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BUILD_URL_FAILED", "failed to build oauth authorization url").WithCause(err))
		return
	}

	RespondOAuthStart(c, authURL)
}

// LinuxDoOAuthCallback verifies state, browser proof, and configured PKCE
// before resolving the external subject. Required account dependencies are
// supplied by handler assembly.
//
// The provider real email is retained as compatibility data while the
// subject-derived synthetic address feeds binding identity. Signed
// current-user targets and existing provider owners become pending sessions.
// Otherwise real-email matches or local verification policies require account
// choice. Eligible direct signup can persist an account and binding before
// fragment delivery; invitation-required results become pending choice.
// Callback cleanup is registered after required code/state input, leaving
// earlier failure cookies until expiry.
func (h *LinuxDoHandler) LinuxDoOAuthCallback(c *gin.Context) {
	cfg, cfgErr := h.loadConfig(c.Request.Context())
	if cfgErr != nil {
		response.ErrorFrom(c, cfgErr)
		return
	}

	frontendCallback := strings.TrimSpace(cfg.FrontendRedirectURL)
	if frontendCallback == "" {
		frontendCallback = LinuxDoOAuthDefaultFrontendCB
	}

	if providerErr := strings.TrimSpace(c.Query("error")); providerErr != "" {
		RedirectOAuthError(c, frontendCallback, "provider_error", providerErr, c.Query("error_description"))
		return
	}

	code := strings.TrimSpace(c.Query("code"))
	state := strings.TrimSpace(c.Query("state"))
	if code == "" || state == "" {
		RedirectOAuthError(c, frontendCallback, "missing_params", "missing code/state", "")
		return
	}

	secureCookie := IsRequestHTTPS(c)
	// Once code and state are present, expire LinuxDo callback and promotion
	// cookies on every later exit. Pending browser proof remains available for
	// the continuation created by successful resolution.
	defer func() {
		LinuxDoClearCookie(c, LinuxDoOAuthStateCookieName, secureCookie)
		LinuxDoClearCookie(c, LinuxDoOAuthVerifierCookie, secureCookie)
		LinuxDoClearCookie(c, LinuxDoOAuthRedirectCookie, secureCookie)
		LinuxDoClearCookie(c, LinuxDoOAuthIntentCookieName, secureCookie)
		LinuxDoClearCookie(c, LinuxDoOAuthBindUserCookieName, secureCookie)
		ClearOAuthPromoCodeCookie(c, secureCookie)
	}()

	expectedState, err := ReadCookieDecoded(c, LinuxDoOAuthStateCookieName)
	if err != nil || expectedState == "" || state != expectedState {
		RedirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth state", "")
		return
	}

	// An unreadable return cookie contributes empty text, then local-path
	// validation selects the dashboard default. Browser proof is checked
	// separately and cannot be replaced by that return value.
	redirectTo, _ := ReadCookieDecoded(c, LinuxDoOAuthRedirectCookie)
	redirectTo = SanitizeFrontendRedirectPath(redirectTo)
	if redirectTo == "" {
		redirectTo = LinuxDoOAuthDefaultRedirectTo
	}
	// Read failures produce empty browser proof, so missing or malformed cookies
	// are rejected before provider exchange and pending state creation.
	browserSessionKey, _ := ReadOAuthPendingBrowserCookie(c)
	if strings.TrimSpace(browserSessionKey) == "" {
		RedirectOAuthError(c, frontendCallback, "missing_browser_session", "missing oauth browser session", "")
		return
	}
	// An unreadable intent has no binding label. Normalization selects login, so
	// a leftover bind-target cookie cannot by itself select the current-user
	// binding branch.
	intent, _ := ReadCookieDecoded(c, LinuxDoOAuthIntentCookieName)
	intent = NormalizeOAuthIntent(intent)

	codeVerifier := ""
	if cfg.UsePKCE {
		// A missing or malformed encoded verifier becomes empty and ends this
		// callback. Accepted code exchange receives the verifier recorded with the
		// authorization challenge.
		codeVerifier, _ = ReadCookieDecoded(c, LinuxDoOAuthVerifierCookie)
		if codeVerifier == "" {
			RedirectOAuthError(c, frontendCallback, "missing_verifier", "missing pkce verifier", "")
			return
		}
	}

	redirectURI := strings.TrimSpace(cfg.RedirectURL)
	if redirectURI == "" {
		RedirectOAuthError(c, frontendCallback, "config_error", "oauth redirect url not configured", "")
		return
	}

	tokenResp, err := h.client.ExchangeCode(c.Request.Context(), cfg, code, redirectURI, codeVerifier)
	if err != nil {
		description := ""
		var exchangeErr *identity.LinuxDoTokenExchangeError
		if errors.As(err, &exchangeErr) && exchangeErr != nil {
			// Bound response bodies to two KiB at complete UTF-8 prefixes for logs.
			logging.LegacyPrintf("handler.linuxdo_oauth",
				"[LinuxDo OAuth] token exchange failed: status=%d provider_error=%q provider_description=%q body=%s",
				exchangeErr.StatusCode,
				exchangeErr.ProviderError,
				exchangeErr.ProviderDescription,
				logredact.TruncateUTF8Value(exchangeErr.Body, 2048),
			)
			description = exchangeErr.Error()
		} else {
			logging.LegacyPrintf("handler.linuxdo_oauth", "[LinuxDo OAuth] token exchange failed: %v", err)
			description = err.Error()
		}
		RedirectOAuthError(c, frontendCallback, "token_exchange_failed", "failed to exchange oauth code", OAuthSingleLine(description))
		return
	}

	email, username, subject, displayName, avatarURL, err := h.client.FetchUserInfo(c.Request.Context(), cfg, tokenResp)
	if err != nil {
		logging.LegacyPrintf("handler.linuxdo_oauth", "[LinuxDo OAuth] userinfo fetch failed: %v", err)
		RedirectOAuthError(c, frontendCallback, "userinfo_failed", "failed to fetch user info", "")
		return
	}
	compatEmail := strings.TrimSpace(email)

	// A provider email can collide with a local account and must not establish ownership.
	// Use a stable subject-derived synthetic email for binding.
	if subject != "" {
		email = identity.OAuthLinuxDoSyntheticEmail(subject)
	}
	identityKey := identity.PendingAuthIdentityKey{
		ProviderType:    "linuxdo",
		ProviderKey:     "linuxdo",
		ProviderSubject: subject,
	}
	upstreamClaims := map[string]any{
		"email":                  email,
		"username":               username,
		"subject":                subject,
		"suggested_display_name": displayName,
		"suggested_avatar_url":   avatarURL,
	}
	if compatEmail != "" && !strings.EqualFold(strings.TrimSpace(compatEmail), strings.TrimSpace(email)) {
		upstreamClaims["compat_email"] = compatEmail
	}
	if intent == OauthIntentBindCurrentUser {
		targetUserID, err := h.binding.ReadOAuthBindUserIDFromCookie(c, LinuxDoOAuthBindUserCookieName)
		if err != nil {
			RedirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth bind target", "")
			return
		}
		if err := h.CreateOAuthPendingSession(c, OauthPendingSessionPayload{
			Intent:                 OauthIntentBindCurrentUser,
			Identity:               identityKey,
			TargetUserID:           &targetUserID,
			ResolvedEmail:          email,
			RedirectTo:             redirectTo,
			BrowserSessionKey:      browserSessionKey,
			UpstreamIdentityClaims: upstreamClaims,
			CompletionResponse: map[string]any{
				"redirect": redirectTo,
			},
		}); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth bind", "")
			return
		}
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	existingIdentityUser, err := h.flow.Database.FindOAuthIdentityUser(c.Request.Context(), identityKey)
	if err != nil {
		RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}
	if existingIdentityUser != nil {
		if err := h.CreateOAuthPendingSession(c, OauthPendingSessionPayload{
			Intent:                 OauthIntentLogin,
			Identity:               identityKey,
			TargetUserID:           &existingIdentityUser.ID,
			ResolvedEmail:          existingIdentityUser.Email,
			RedirectTo:             redirectTo,
			BrowserSessionKey:      browserSessionKey,
			UpstreamIdentityClaims: upstreamClaims,
			CompletionResponse: map[string]any{
				"redirect": redirectTo,
			},
		}); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth login", "")
			return
		}
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	compatEmailUser, err := h.flow.Database.FindLinuxDoCompatEmailUser(c.Request.Context(), compatEmail)
	if err != nil {
		RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}
	emailVerificationRequired := h != nil && h.authService != nil && h.authService.IsEmailVerifyEnabled(c.Request.Context())
	forceEmailOnSignup := h.isForceEmailOnThirdPartySignup(c.Request.Context())
	if compatEmailUser == nil && !emailVerificationRequired && !forceEmailOnSignup {
		if err := h.ensureBackendModeAllowsNewUserLogin(c.Request.Context()); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
			return
		}
		// Authentication may issue a pair before external-subject binding succeeds.
		// Keep that binding ahead of fragment delivery; failure withholds the response
		// after earlier account or credential work.
		tokenPair, user, err := h.authService.LoginOrRegisterOAuthWithTokenPairAndPromoCode(c.Request.Context(), email, username, "", "", ReadOAuthPromoCode(c), "linuxdo")
		if err == nil {
			if err := h.flow.Database.ApplyBinding(c.Request.Context(), identity.PendingBinding{Session: &identity.PendingAuthSession{
				Intent:                 OauthIntentLogin,
				ProviderType:           identityKey.ProviderType,
				ProviderKey:            identityKey.ProviderKey,
				ProviderSubject:        identityKey.ProviderSubject,
				ResolvedEmail:          email,
				UpstreamIdentityClaims: upstreamClaims,
			}, OverrideUserID: &user.ID, ForceBind: true}); err != nil {
				RedirectOAuthError(c, frontendCallback, "session_error", "failed to bind oauth identity", "")
				return
			}
			h.authService.RecordSuccessfulLogin(c.Request.Context(), user.ID)
			ClearOAuthPendingSessionCookie(c, secureCookie)
			ClearOAuthPendingBrowserCookie(c, secureCookie)
			RedirectOAuthTokenPair(c, frontendCallback, tokenPair, redirectTo)
			return
		}
		if !errors.Is(err, identity.ErrOAuthInvitationRequired) {
			RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
			return
		}
	}
	if err := h.CreateLinuxDoChoiceSession(
		c,
		identityKey,
		email,
		email,
		redirectTo,
		browserSessionKey,
		upstreamClaims,
		compatEmail,
		compatEmailUser,
		emailVerificationRequired,
		forceEmailOnSignup,
	); err != nil {
		RedirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth login", "")
		return
	}
	RedirectToFrontendCallback(c, frontendCallback)
}

// CompleteLinuxDoOAuthRegistration handles a browser-bound legacy invitation
// completion or returns updated account-choice state. The flow first
// reevaluates local email requirements and backend-mode registration
// admission.
//
// A usable synthetic email and username plus available provider identity are
// required before creating or resolving the account. Saved adoption choice
// then enters the combined adoption-and-consumption database operation.
// Credentials may already have been issued by authentication before that later
// operation fails. Successful completion records login, expires pending
// cookies, and writes a top-level token response.
func (h *LinuxDoHandler) CompleteLinuxDoOAuthRegistration(c *gin.Context) {
	var req CompleteLinuxDoOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	secureCookie := IsRequestHTTPS(c)
	sessionToken, err := ReadOAuthPendingSessionCookie(c)
	if err != nil {
		ClearOAuthPendingSessionCookie(c, secureCookie)
		ClearOAuthPendingBrowserCookie(c, secureCookie)
		response.ErrorFrom(c, identity.ErrPendingAuthSessionNotFound)
		return
	}
	browserSessionKey, err := ReadOAuthPendingBrowserCookie(c)
	if err != nil {
		ClearOAuthPendingSessionCookie(c, secureCookie)
		ClearOAuthPendingBrowserCookie(c, secureCookie)
		response.ErrorFrom(c, identity.ErrPendingAuthBrowserMismatch)
		return
	}
	pendingSvc, err := h.pendingIdentityService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	session, err := pendingSvc.GetBrowserSession(c.Request.Context(), sessionToken, browserSessionKey)
	if err != nil {
		ClearOAuthPendingSessionCookie(c, secureCookie)
		ClearOAuthPendingBrowserCookie(c, secureCookie)
		response.ErrorFrom(c, err)
		return
	}
	if err := EnsurePendingOAuthCompleteRegistrationSession(session); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	updatedSession, handled, err := h.flow.LegacyRegistrationStatus(c.Request.Context(), session, h.authService != nil && h.authService.IsEmailVerifyEnabled(c.Request.Context()), h.isForceEmailOnThirdPartySignup(c.Request.Context()))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	} else if handled {
		c.JSON(http.StatusOK, BuildPendingOAuthSessionStatusPayload(updatedSession))
		return
	}
	session = updatedSession
	if err := h.ensureBackendModeAllowsNewUserLogin(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	email := strings.TrimSpace(session.ResolvedEmail)
	username := PendingSessionStringValue(session.UpstreamIdentityClaims, "username")
	if email == "" || username == "" {
		response.ErrorFrom(c, infraerrors.BadRequest("PENDING_AUTH_SESSION_INVALID", "pending auth registration context is invalid"))
		return
	}

	if !h.flow.Available() {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready"))
		return
	}
	if err := h.flow.Database.EnsureRegistrationIdentityAvailable(c.Request.Context(), session); err != nil {
		RespondPendingOAuthBindingApplyError(c, err)
		return
	}
	decision, err := h.ensurePendingOAuthAdoptionDecision(c, session.ID, OauthAdoptionDecisionRequest{
		AdoptDisplayName: req.AdoptDisplayName,
		AdoptAvatar:      req.AdoptAvatar,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	tokenPair, user, err := h.authService.LoginOrRegisterOAuthWithTokenPairAndPromoCode(c.Request.Context(), email, username, req.InvitationCode, req.AffCode, PendingOAuthPromoCode(session), "linuxdo")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// Authentication has already produced credentials. Persist adoption and the
	// browser claim together before returning them; transaction failure leaves
	// that earlier credential issuance outside this rollback scope.
	if err := h.flow.Database.ApplyAdoptionAndConsume(c.Request.Context(), session, decision, user.ID); err != nil {
		RespondPendingOAuthBindingApplyError(c, err)
		return
	}
	h.authService.RecordSuccessfulLogin(c.Request.Context(), user.ID)
	ClearOAuthPendingSessionCookie(c, secureCookie)
	ClearOAuthPendingBrowserCookie(c, secureCookie)

	c.JSON(http.StatusOK, gin.H{
		"access_token":  tokenPair.AccessToken,
		"refresh_token": tokenPair.RefreshToken,
		"expires_in":    tokenPair.ExpiresIn,
		"token_type":    "Bearer",
	})
}

// BuildLinuxDoAuthorizeURL encodes client, redirect, state, and optional
// scopes into the configured authorize URL. A nonblank challenge adds the S256
// PKCE parameters; unrelated existing query values remain in the parsed URL.
func BuildLinuxDoAuthorizeURL(cfg identity.LinuxDoOAuthOptions, state string, codeChallenge string, redirectURI string) (string, error) {
	u, err := url.Parse(cfg.AuthorizeURL)
	if err != nil {
		return "", fmt.Errorf("parse authorize_url: %w", err)
	}

	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	if strings.TrimSpace(cfg.Scopes) != "" {
		q.Set("scope", cfg.Scopes)
	}
	q.Set("state", state)
	if strings.TrimSpace(codeChallenge) != "" {
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", "S256")
	}

	u.RawQuery = q.Encode()
	return u.String(), nil
}

// RedirectOAuthError builds a bounded error fragment and delegates frontend
// redirect writing. Blank message or description fields are omitted, while
// each supplied value passes through the shared fragment truncation rule.
func RedirectOAuthError(c *gin.Context, frontendCallback string, code string, message string, description string) {
	fragment := url.Values{}
	fragment.Set("error", TruncateOAuthFragment(code))
	if strings.TrimSpace(message) != "" {
		fragment.Set("error_message", TruncateOAuthFragment(message))
	}
	if strings.TrimSpace(description) != "" {
		fragment.Set("error_description", TruncateOAuthFragment(description))
	}
	RedirectOAuthFragment(c, frontendCallback, fragment)
}

// RedirectOAuthTokenPair builds a credential and local-return fragment for the
// frontend callback. Nil token pairs omit credentials, and supplied credential
// values pass through the shared byte truncation rule.
//
// Return paths are query-decoded at most twice to accept encoded callback
// values. A changed decoded value is used only when the local-path sanitizer
// accepts it; otherwise the original supplied text returns. Callers sanitize
// that original path before invoking this helper. Selected redirect and
// credential values are capped at the fragment limit, so oversized credential
// text can be shortened before delivery.
func RedirectOAuthTokenPair(c *gin.Context, frontendCallback string, tokenPair *identity.TokenPair, redirectTo string) {
	fragment := url.Values{}
	if tokenPair != nil {
		fragment.Set("access_token", TruncateOAuthFragment(tokenPair.AccessToken))
		fragment.Set("refresh_token", TruncateOAuthFragment(tokenPair.RefreshToken))
		fragment.Set("expires_in", strconv.Itoa(tokenPair.ExpiresIn))
		fragment.Set("token_type", "Bearer")
	}
	if redirect := strings.TrimSpace(redirectTo); redirect != "" {
		originalRedirect := redirect
		// Decode at most two query-encoding layers used by compatibility return
		// values. Retain the caller original text when a changed decoded path fails
		// the local-path check.
		for range 2 {
			decoded, err := url.QueryUnescape(redirect)
			if err != nil || decoded == redirect {
				break
			}
			redirect = decoded
		}
		if redirect != originalRedirect {
			if sanitized := SanitizeFrontendRedirectPath(redirect); sanitized != "" {
				redirect = sanitized
			} else {
				redirect = originalRedirect
			}
		}
		fragment.Set("redirect", TruncateOAuthFragment(redirect))
	}
	RedirectOAuthFragment(c, frontendCallback, fragment)
}

// RedirectOAuthFragment replaces an accepted callback URL fragment with
// encoded values and adds cache prevention headers. Parse failure or a non-HTTP
// scheme redirects directly to the local dashboard before those headers are
// set; relative and HTTP(S) callbacks remain allowed.
func RedirectOAuthFragment(c *gin.Context, frontendCallback string, fragment url.Values) {
	u, err := url.Parse(frontendCallback)
	if err != nil {
		// Invalid callback configuration cannot carry the prepared fragment. Send
		// the browser to the known local dashboard before setting fragment response
		// headers.
		c.Redirect(http.StatusFound, LinuxDoOAuthDefaultRedirectTo)
		return
	}
	// A parsed callback with another scheme bypasses fragment delivery as well.
	// The local dashboard supplies a navigation destination for this rejected
	// configuration.
	if u.Scheme != "" && !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		c.Redirect(http.StatusFound, LinuxDoOAuthDefaultRedirectTo)
		return
	}
	u.Fragment = fragment.Encode()
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Redirect(http.StatusFound, u.String())
}

// OAuthSingleLine trims text and collapses whitespace runs into one
// space-separated line.
func OAuthSingleLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.Join(strings.Fields(value), " ")
}

// LinuxDoSetCookie emits the supplied encoded value at the LinuxDo browser
// cookie path. maxAgeSec is seconds; the caller supplies HTTPS state, and
// HttpOnly/SameSite Lax support provider callback navigation.
func LinuxDoSetCookie(c *gin.Context, name string, value string, maxAgeSec int, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     LinuxDoOAuthCookiePath,
		MaxAge:   maxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// LinuxDoClearCookie emits deletion of a LinuxDo state cookie at its matching
// path. The caller supplies HTTPS state, while HttpOnly and SameSite Lax
// attributes match the browser continuation cookie.
func LinuxDoClearCookie(c *gin.Context, name string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     LinuxDoOAuthCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// TruncateOAuthFragment trims fragment text and returns at most the configured
// byte limit. Values within that limit pass through without UTF-8 validation,
// while oversized values first select a byte prefix before URL encoding.
//
// The loop removes trailing bytes until the whole prefix is valid UTF-8. A
// split final rune loses its partial bytes, and an invalid byte earlier in the
// prefix also removes every later byte, potentially yielding empty text. This
// shortening applies to credential fragments as well as error descriptions.
func TruncateOAuthFragment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > LinuxDoOAuthMaxFragmentValueLen {
		// The byte cap can split UTF-8. Back off the shortened tail until valid
		// before URL encoding; this can leave fewer bytes than the cap.
		value = value[:LinuxDoOAuthMaxFragmentValueLen]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

// NormalizeOAuthIntent returns login or bind_current_user after trimming and
// case folding. Empty, login, and unknown inputs select login; bind is
// accepted as the binding alias for provider start handlers.
func NormalizeOAuthIntent(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", OauthIntentLogin:
		return OauthIntentLogin
	case "bind", OauthIntentBindCurrentUser:
		return OauthIntentBindCurrentUser
	default:
		// Unknown labels cannot select authenticated binding. Continue as login so
		// the provider flow still establishes an owner through its normal pending
		// account checks.
		return OauthIntentLogin
	}
}

// CreateLinuxDoChoiceSession prepares account-choice state from provider and
// local email observations, then stores the pending draft. The identity helper
// owns policy precedence, and persistence errors remain visible to the
// callback caller.
func (h *LinuxDoHandler) CreateLinuxDoChoiceSession(
	c *gin.Context,
	identityKey identity.PendingAuthIdentityKey,
	suggestedEmail string,
	resolvedEmail string,
	redirectTo string,
	browserSessionKey string,
	upstreamClaims map[string]any,
	compatEmail string,
	compatEmailUser *identity.User,
	emailVerificationRequired bool,
	forceEmailOnSignup bool,
) error {
	draft := identity.PrepareLinuxDoChoice(identityKey, suggestedEmail, resolvedEmail, redirectTo, browserSessionKey, upstreamClaims, compatEmail, compatEmailUser, emailVerificationRequired, forceEmailOnSignup)
	return h.CreateOAuthPendingSession(c, draft)
}
