package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/pkg/oauthpkce"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// oidc_oauth.go handles OIDC authorization redirects, callback identity
// resolution, and pending registration completion.
// OIDC subjects belong to an issuer namespace, so callback resolution combines
// the selected issuer with the stable subject instead of using provider email
// as account ownership. Browser state carries login or current-user binding
// intent, optional PKCE verifier, and nonce when ID-token validation is
// enabled.
//
// The callback can combine ID-token and userinfo observations but rejects
// conflicting nonblank subjects. Userinfo email-verification state takes
// precedence when present, even when false. Existing subject ownership or
// signed binding targets enter pending state; an eligible verified real email
// can use direct signup, while account matches and local policies require
// choice. Legacy invitation completion persists adoption and consumption after
// authentication has already produced credentials.

// OIDC cookie names, provider path, lifetime, and frontend defaults carry
// authorization state, nonce, PKCE material, and account intent through
// browser callback navigation.
const (
	OidcOAuthCookiePath         = "/api/v1/auth/oauth/oidc" // OIDC browser cookie path
	OidcOAuthStateCookieName    = "oidc_oauth_state"        // OIDC authorization state cookie
	OidcOAuthVerifierCookie     = "oidc_oauth_verifier"     // OIDC PKCE verifier cookie
	OidcOAuthRedirectCookie     = "oidc_oauth_redirect"     // OIDC local return cookie
	OidcOAuthNonceCookie        = "oidc_oauth_nonce"        // OIDC token nonce cookie
	OidcOAuthIntentCookieName   = "oidc_oauth_intent"       // OIDC account intent cookie
	OidcOAuthBindUserCookieName = "oidc_oauth_bind_user"    // signed target account cookie
	OidcOAuthCookieMaxAgeSec    = 10 * 60                   // seconds
	OidcOAuthDefaultRedirectTo  = "/dashboard"              // default local login destination
	OidcOAuthDefaultFrontendCB  = "/auth/oidc/callback"     // default OIDC browser callback
)

// OIDCHandler resolves issuer-scoped provider identities into panel login and
// binding continuations.
type OIDCHandler struct {
	*PendingHandler                                                          // shared browser pending authentication
	binding         *OAuthBindHandler                                        // signed current-account binding helper
	client          identity.OIDCOAuthClient                                 // OIDC authorization and claims client
	loadConfig      func(context.Context) (identity.OIDCOAuthOptions, error) // current issuer configuration callback
}

// CompleteOIDCOAuthRequest carries invitation and profile choices for legacy
// OIDC pending signup.
type CompleteOIDCOAuthRequest struct {
	InvitationCode string `json:"invitation_code" binding:"required"` // required registration invitation code
	AffCode        string `json:"aff_code,omitempty"`                 // optional signup affiliate code
	// AdoptDisplayName and AdoptAvatar keep omitted choices as nil and supplied
	// false values as refusals. The helper retains saved values for omitted
	// flags and otherwise defaults those flags to false before applying supplied
	// choices.
	AdoptDisplayName *bool `json:"adopt_display_name,omitempty"` // optional provider-name adoption choice
	AdoptAvatar      *bool `json:"adopt_avatar,omitempty"`       // optional provider-avatar adoption choice
}

// NewOIDCHandler retains shared pending and binding handlers, OIDC client, and
// configuration reader. Initialize dependencies for the exposed issuer login
// and current-user binding paths before serving requests.
func NewOIDCHandler(p *PendingHandler, b *OAuthBindHandler, client identity.OIDCOAuthClient, load func(context.Context) (identity.OIDCOAuthOptions, error)) *OIDCHandler {
	return &OIDCHandler{p, b, client, load}
}

// OIDCOAuthStart checks action CAPTCHA and stores issuer browser state before
// returning its authorize URL. It captures intent and local return path,
// replacing earlier pending-session proof with a new browser key.
//
// Current-user intent signs the target account cookie. PKCE and ID-token
// validation independently create verifier and nonce cookies with their
// matching authorization parameters. Cookie emission precedes later
// configuration or URL errors, so a failed start can leave partial browser
// state. POST writes an authorize URL envelope, while accepted navigation
// starts redirect.
func (h *OIDCHandler) OIDCOAuthStart(c *gin.Context) {
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
		redirectTo = OidcOAuthDefaultRedirectTo
	}

	browserSessionKey, err := GenerateOAuthPendingBrowserSession()
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BROWSER_SESSION_GEN_FAILED", "failed to generate oauth browser session").WithCause(err))
		return
	}

	secureCookie := IsRequestHTTPS(c)
	// Publish issuer start context beside replacement browser proof before target,
	// verifier, nonce, and URL preparation. A failure in those later stages leaves
	// the earlier cookie headers while the prior pending token is expired.
	OIDCSetCookie(c, OidcOAuthStateCookieName, EncodeCookieValue(state), OidcOAuthCookieMaxAgeSec, secureCookie)
	OIDCSetCookie(c, OidcOAuthRedirectCookie, EncodeCookieValue(redirectTo), OidcOAuthCookieMaxAgeSec, secureCookie)
	intent := NormalizeOAuthIntent(c.Query("intent"))
	OIDCSetCookie(c, OidcOAuthIntentCookieName, EncodeCookieValue(intent), OidcOAuthCookieMaxAgeSec, secureCookie)
	CaptureOAuthPromoCode(c, secureCookie)
	SetOAuthPendingBrowserCookie(c, browserSessionKey, secureCookie)
	ClearOAuthPendingSessionCookie(c, secureCookie)
	if intent == OauthIntentBindCurrentUser {
		bindCookieValue, err := h.binding.BuildOAuthBindUserCookieFromContext(c)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		OIDCSetCookie(c, OidcOAuthBindUserCookieName, EncodeCookieValue(bindCookieValue), OidcOAuthCookieMaxAgeSec, secureCookie)
	} else {
		OIDCClearCookie(c, OidcOAuthBindUserCookieName, secureCookie)
	}

	codeChallenge := ""
	if cfg.UsePKCE {
		verifier, genErr := oauthpkce.Verifier()
		if genErr != nil {
			response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_PKCE_GEN_FAILED", "failed to generate pkce verifier").WithCause(genErr))
			return
		}
		codeChallenge = oauthpkce.Challenge(verifier)
		OIDCSetCookie(c, OidcOAuthVerifierCookie, EncodeCookieValue(verifier), OidcOAuthCookieMaxAgeSec, secureCookie)
	}

	nonce := ""
	if cfg.ValidateIDToken {
		nonce, err = oauthpkce.Verifier()
		if err != nil {
			response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_NONCE_GEN_FAILED", "failed to generate oauth nonce").WithCause(err))
			return
		}
		OIDCSetCookie(c, OidcOAuthNonceCookie, EncodeCookieValue(nonce), OidcOAuthCookieMaxAgeSec, secureCookie)
	}

	redirectURI := strings.TrimSpace(cfg.RedirectURL)
	if redirectURI == "" {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url not configured"))
		return
	}

	authURL, err := BuildOIDCAuthorizeURL(cfg, state, nonce, codeChallenge, redirectURI)
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BUILD_URL_FAILED", "failed to build oauth authorization url").WithCause(err))
		return
	}

	RespondOAuthStart(c, authURL)
}

// OIDCOAuthCallback verifies state, browser proof, configured PKCE, and
// optional ID-token nonce before issuer account resolution. The configured
// provider client performs token and claim validation.
//
// ID-token subject and issuer are preferred, with userinfo subject and
// configured issuer supplying missing values; conflicting nonblank subjects
// reject the callback. Present userinfo verification state wins over ID-token
// verification state. Existing identity owners and signed current-user targets
// enter pending state. Otherwise configured verification, real-email lookup,
// and local signup policy decide direct verified-email signup or account
// choice. Cookie cleanup starts after required code/state input, and later
// errors can follow completed provider or account work.
func (h *OIDCHandler) OIDCOAuthCallback(c *gin.Context) {
	cfg, cfgErr := h.loadConfig(c.Request.Context())
	if cfgErr != nil {
		response.ErrorFrom(c, cfgErr)
		return
	}

	frontendCallback := strings.TrimSpace(cfg.FrontendRedirectURL)
	if frontendCallback == "" {
		frontendCallback = OidcOAuthDefaultFrontendCB
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
	// Expire issuer state, verifier, nonce, intent, target, redirect, and
	// promotion cookies on exits after code/state validation. Pending browser
	// proof remains for any successful continuation draft.
	defer func() {
		OIDCClearCookie(c, OidcOAuthStateCookieName, secureCookie)
		OIDCClearCookie(c, OidcOAuthVerifierCookie, secureCookie)
		OIDCClearCookie(c, OidcOAuthRedirectCookie, secureCookie)
		OIDCClearCookie(c, OidcOAuthNonceCookie, secureCookie)
		OIDCClearCookie(c, OidcOAuthIntentCookieName, secureCookie)
		OIDCClearCookie(c, OidcOAuthBindUserCookieName, secureCookie)
		ClearOAuthPromoCodeCookie(c, secureCookie)
	}()

	expectedState, err := ReadCookieDecoded(c, OidcOAuthStateCookieName)
	if err != nil || expectedState == "" || state != expectedState {
		RedirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth state", "")
		return
	}

	// An unreadable return cookie contributes empty text, and local-path
	// validation selects the dashboard destination. The required browser proof
	// below remains a separate continuation check.
	redirectTo, _ := ReadCookieDecoded(c, OidcOAuthRedirectCookie)
	redirectTo = SanitizeFrontendRedirectPath(redirectTo)
	if redirectTo == "" {
		redirectTo = OidcOAuthDefaultRedirectTo
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
	intent, _ := ReadCookieDecoded(c, OidcOAuthIntentCookieName)
	intent = NormalizeOAuthIntent(intent)

	codeVerifier := ""
	if cfg.UsePKCE {
		// A missing or malformed encoded verifier becomes empty and ends this
		// callback. Accepted code exchange receives the verifier recorded with the
		// authorization challenge.
		codeVerifier, _ = ReadCookieDecoded(c, OidcOAuthVerifierCookie)
		if codeVerifier == "" {
			RedirectOAuthError(c, frontendCallback, "missing_verifier", "missing pkce verifier", "")
			return
		}
	}

	expectedNonce := ""
	if cfg.ValidateIDToken {
		// Unreadable nonce proof becomes empty and rejects this configured ID-token
		// callback before exchange. A present value reaches provider validation for
		// the authorization nonce comparison.
		expectedNonce, _ = ReadCookieDecoded(c, OidcOAuthNonceCookie)
		if expectedNonce == "" {
			RedirectOAuthError(c, frontendCallback, "missing_nonce", "missing oauth nonce", "")
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
		var exchangeErr *identity.OIDCTokenExchangeError
		if errors.As(err, &exchangeErr) && exchangeErr != nil {
			// Bound response bodies to two KiB at complete UTF-8 prefixes for logs.
			logging.LegacyPrintf("handler.oidc_oauth",
				"[OIDC OAuth] token exchange failed: status=%d provider_error=%q provider_description=%q body=%s",
				exchangeErr.StatusCode,
				exchangeErr.ProviderError,
				exchangeErr.ProviderDescription,
				logredact.TruncateUTF8Value(exchangeErr.Body, 2048),
			)
			description = exchangeErr.Error()
		} else {
			logging.LegacyPrintf("handler.oidc_oauth", "[OIDC OAuth] token exchange failed: %v", err)
			description = err.Error()
		}
		RedirectOAuthError(c, frontendCallback, "token_exchange_failed", "failed to exchange oauth code", OAuthSingleLine(description))
		return
	}

	var idClaims *identity.OIDCVerifiedClaims
	if cfg.ValidateIDToken {
		if strings.TrimSpace(tokenResp.IDToken) == "" {
			RedirectOAuthError(c, frontendCallback, "missing_id_token", "missing id_token", "")
			return
		}

		idClaims, err = h.client.ValidateIDToken(c.Request.Context(), cfg, tokenResp.IDToken, expectedNonce)
		if err != nil {
			logging.LegacyPrintf("handler.oidc_oauth", "[OIDC OAuth] id_token validation failed: %v", err)
			RedirectOAuthError(c, frontendCallback, "invalid_id_token", "failed to validate id_token", "")
			return
		}
	}

	userInfoClaims, err := h.client.FetchUserInfo(c.Request.Context(), cfg, tokenResp)
	if err != nil {
		logging.LegacyPrintf("handler.oidc_oauth", "[OIDC OAuth] userinfo fetch failed: %v", err)
		RedirectOAuthError(c, frontendCallback, "userinfo_failed", "failed to fetch user info", "")
		return
	}

	// Combine subject and issuer observations after configured token validation.
	// A nonblank ID-token subject must agree with userinfo before either source
	// can identify the local provider tuple.
	subject := ""
	if idClaims != nil {
		subject = strings.TrimSpace(idClaims.Subject)
	}
	if subject == "" {
		subject = strings.TrimSpace(userInfoClaims.Subject)
	}
	if subject == "" {
		RedirectOAuthError(c, frontendCallback, "missing_subject", "missing subject claim", "")
		return
	}
	issuer := ""
	if idClaims != nil {
		issuer = strings.TrimSpace(idClaims.Issuer)
	}
	if issuer == "" {
		issuer = strings.TrimSpace(cfg.IssuerURL)
	}
	if issuer == "" {
		RedirectOAuthError(c, frontendCallback, "missing_issuer", "missing issuer claim", "")
		return
	}

	// A present userinfo verification value, including false, takes precedence.
	// Consult ID-token verification only when userinfo omitted that claim so
	// policy sees the selected observation accurately.
	emailVerified := userInfoClaims.EmailVerified
	if emailVerified == nil && idClaims != nil {
		emailVerified = idClaims.EmailVerified
	}
	if idClaims != nil && userInfoClaims.Subject != "" && idClaims.Subject != "" && strings.TrimSpace(userInfoClaims.Subject) != strings.TrimSpace(idClaims.Subject) {
		RedirectOAuthError(c, frontendCallback, "subject_mismatch", "userinfo subject does not match id_token", "")
		return
	}

	identityKey := identity.OIDCIdentityKey(issuer, subject)
	compatEmail := strings.TrimSpace(userInfoClaims.Email)
	if compatEmail == "" && idClaims != nil {
		compatEmail = strings.TrimSpace(idClaims.Email)
	}
	email := identity.OIDCSyntheticEmailFromIdentityKey(identityKey)
	username := identity.OAuthFirstNonEmpty(
		userInfoClaims.Username,
		func() string {
			if idClaims != nil {
				return idClaims.PreferredUsername
			}
			return ""
		}(),
		func() string {
			if idClaims != nil {
				return idClaims.Name
			}
			return ""
		}(),
		identity.OIDCFallbackUsername(subject),
	)
	identityRef := identity.PendingAuthIdentityKey{
		ProviderType:    "oidc",
		ProviderKey:     issuer,
		ProviderSubject: subject,
	}
	upstreamClaims := map[string]any{
		"email":             email,
		"username":          username,
		"subject":           subject,
		"issuer":            issuer,
		"email_verified":    emailVerified != nil && *emailVerified,
		"provider_fallback": strings.TrimSpace(cfg.ProviderName),
		"suggested_display_name": identity.OAuthFirstNonEmpty(userInfoClaims.DisplayName, func() string {
			if idClaims != nil {
				return idClaims.Name
			}
			return ""
		}(), username),
		"suggested_avatar_url": userInfoClaims.AvatarURL,
	}
	if compatEmail != "" && !strings.EqualFold(strings.TrimSpace(compatEmail), strings.TrimSpace(email)) {
		upstreamClaims["compat_email"] = compatEmail
	}
	if intent == OauthIntentBindCurrentUser {
		targetUserID, err := h.binding.ReadOAuthBindUserIDFromCookie(c, OidcOAuthBindUserCookieName)
		if err != nil {
			RedirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth bind target", "")
			return
		}
		if err := h.CreateOAuthPendingSession(c, OauthPendingSessionPayload{
			Intent:                 OauthIntentBindCurrentUser,
			Identity:               identityRef,
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

	existingIdentityUser, err := h.flow.Database.FindOAuthIdentityUser(c.Request.Context(), identityRef)
	if err != nil {
		RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}
	if existingIdentityUser != nil {
		if err := h.CreateOAuthPendingSession(c, OauthPendingSessionPayload{
			Intent:                 OauthIntentLogin,
			Identity:               identityRef,
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

	compatEmailUser, err := h.flow.Database.FindOIDCCompatEmailUser(c.Request.Context(), compatEmail)
	if err != nil {
		RedirectOAuthError(c, frontendCallback, "session_error", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}

	if cfg.RequireEmailVerified {
		if emailVerified == nil || !*emailVerified {
			RedirectOAuthError(c, frontendCallback, "email_not_verified", "email is not verified", "")
			return
		}
	}

	// Try direct signup for a verified provider email with no matching local account.
	// The fast-path helper checks whether local policy requires additional confirmation.
	if compatEmailUser == nil &&
		strings.TrimSpace(compatEmail) != "" &&
		emailVerified != nil && *emailVerified {
		if handled := h.TryOIDCVerifiedEmailFastPath(
			c,
			frontendCallback,
			redirectTo,
			identityRef,
			compatEmail,
			username,
			upstreamClaims,
		); handled {
			return
		}
	}

	if h.isForceEmailOnThirdPartySignup(c.Request.Context()) {
		if err := h.CreateOIDCChoiceSession(
			c,
			identityRef,
			email,
			email,
			redirectTo,
			browserSessionKey,
			upstreamClaims,
			compatEmail,
			compatEmailUser,
			true,
		); err != nil {
			RedirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth login", "")
			return
		}
		RedirectToFrontendCallback(c, frontendCallback)
		return
	}

	if err := h.CreateOIDCChoiceSession(
		c,
		identityRef,
		email,
		email,
		redirectTo,
		browserSessionKey,
		upstreamClaims,
		compatEmail,
		compatEmailUser,
		h.isForceEmailOnThirdPartySignup(c.Request.Context()),
	); err != nil {
		RedirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth login", "")
		return
	}
	RedirectToFrontendCallback(c, frontendCallback)
}

// CompleteOIDCOAuthRegistration completes legacy browser OIDC signup after
// reevaluating email and backend policy. It returns account-choice progression
// when the pending flow still requires additional local proof.
//
// Usable stored email and username plus available issuer identity are required
// before invitation-based authentication. Saved profile choices enter the
// combined adoption-and-consumption database operation afterward, so
// credentials can be issued before its failure. Success records login, clears
// pending cookies, and returns the legacy top-level token shape.
func (h *OIDCHandler) CompleteOIDCOAuthRegistration(c *gin.Context) {
	var req CompleteOIDCOAuthRequest
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
	tokenPair, user, err := h.authService.LoginOrRegisterOAuthWithTokenPairAndPromoCode(c.Request.Context(), email, username, req.InvitationCode, req.AffCode, PendingOAuthPromoCode(session), "oidc")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// The returned pair precedes this adoption and browser claim transaction.
	// Commit those pending writes before response delivery; their rollback cannot
	// compensate the earlier authentication operation.
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

// BuildOIDCAuthorizeURL encodes client, redirect, state, and optional scopes
// into the issuer URL. Nonblank nonce and challenge add their parameters,
// including S256 PKCE; unrelated existing query values remain.
func BuildOIDCAuthorizeURL(cfg identity.OIDCOAuthOptions, state, nonce, codeChallenge, redirectURI string) (string, error) {
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
	if strings.TrimSpace(nonce) != "" {
		q.Set("nonce", nonce)
	}
	if strings.TrimSpace(codeChallenge) != "" {
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", "S256")
	}

	u.RawQuery = q.Encode()
	return u.String(), nil
}

// OIDCSetCookie emits a supplied encoded value at the OIDC browser path with a
// seconds-based lifetime. The caller supplies HTTPS state, while HttpOnly and
// SameSite Lax support issuer callback navigation.
func OIDCSetCookie(c *gin.Context, name, value string, maxAgeSec int, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     OidcOAuthCookiePath,
		MaxAge:   maxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// OIDCClearCookie expires a named OIDC browser cookie at its matching provider
// path. The caller supplies HTTPS state, and HttpOnly/SameSite Lax match the
// stored callback value.
func OIDCClearCookie(c *gin.Context, name string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     OidcOAuthCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// TryOIDCVerifiedEmailFastPath attempts direct authentication for a
// caller-validated verified real email with no matched local account. True
// means a redirect was written; false asks the callback to prepare account
// choice.
//
// Missing dependencies, forced email collection, or invitation policy skip
// this attempt. Backend-mode refusal is handled with a redirect and
// pending-cookie cleanup. Authentication failures are logged and return false,
// while success delivers issued credentials through the frontend fragment and
// clears pending proof. The caller provides the issuer subject, profile
// claims, and sanitized return path before selecting this path.
func (h *OIDCHandler) TryOIDCVerifiedEmailFastPath(
	c *gin.Context,
	frontendCallback string,
	redirectTo string,
	identityKey identity.PendingAuthIdentityKey,
	compatEmail string,
	username string,
	upstreamClaims map[string]any,
) bool {
	if h == nil || h.authService == nil || h.settingSvc == nil {
		return false
	}
	ctx := c.Request.Context()
	if h.isForceEmailOnThirdPartySignup(ctx) {
		return false
	}
	if h.settingSvc.IsInvitationCodeEnabled(ctx) {
		return false
	}
	if err := h.ensureBackendModeAllowsNewUserLogin(ctx); err != nil {
		logging.LegacyPrintf("handler.oidc_oauth", "[OIDC OAuth] verified-email fast path blocked by backend mode: reason=%s", infraerrors.Reason(err))
		ClearOAuthPendingSessionCookie(c, IsRequestHTTPS(c))
		ClearOAuthPendingBrowserCookie(c, IsRequestHTTPS(c))
		RedirectOAuthError(c, frontendCallback, "login_blocked", infraerrors.Reason(err), infraerrors.Message(err))
		return true
	}

	input := identity.OIDCVerifiedEmailIdentity(identityKey, compatEmail, username, upstreamClaims)

	tokenPair, _, err := h.authService.LoginOrRegisterVerifiedEmailOAuthWithSignupCodes(ctx, input, "", "", ReadOAuthPromoCode(c))
	if err != nil {
		// A direct authentication error leaves account-choice handling to the
		// callback. Log the reason and report unhandled status instead of writing a
		// second error redirect here.
		logging.LegacyPrintf("handler.oidc_oauth", "[OIDC OAuth] verified-email fast path skipped: reason=%s", infraerrors.Reason(err))
		return false
	}

	fragment := url.Values{}
	fragment.Set("access_token", tokenPair.AccessToken)
	fragment.Set("refresh_token", tokenPair.RefreshToken)
	fragment.Set("expires_in", fmt.Sprintf("%d", tokenPair.ExpiresIn))
	fragment.Set("token_type", "Bearer")
	fragment.Set("redirect", redirectTo)
	ClearOAuthPendingSessionCookie(c, IsRequestHTTPS(c))
	ClearOAuthPendingBrowserCookie(c, IsRequestHTTPS(c))
	RedirectOAuthFragment(c, frontendCallback, fragment)
	return true
}

// CreateOIDCChoiceSession prepares issuer account-choice data and stores its
// browser pending draft. The identity helper owns local email and
// matched-account policy selection, and persistence errors remain visible to
// the callback.
func (h *OIDCHandler) CreateOIDCChoiceSession(
	c *gin.Context,
	identityKey identity.PendingAuthIdentityKey,
	suggestedEmail string,
	resolvedEmail string,
	redirectTo string,
	browserSessionKey string,
	upstreamClaims map[string]any,
	compatEmail string,
	compatEmailUser *identity.User,
	forceEmailOnSignup bool,
) error {
	draft := identity.PrepareOIDCChoice(identityKey, suggestedEmail, resolvedEmail, redirectTo, browserSessionKey, upstreamClaims, compatEmail, compatEmailUser, forceEmailOnSignup)
	return h.CreateOAuthPendingSession(c, draft)
}
