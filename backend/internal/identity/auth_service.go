package identity

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// auth_service.go orchestrates registration, password login, CAPTCHA checks,
// and password resets.
// Email registration saves the account and applicable invitation use before
// requesting optional signup plans, affiliate attribution and promotion.
// Access-token generation follows those steps, so its failure can leave an
// account and completed signup effects in place.
//
// Runtime policy selects ordinary registration, provider signup, challenge
// admission and password-reset behavior through separate reads. Generic
// CAPTCHA accepts one selected provider, while OAuth/Passkey starts use only
// the action providers and registration can skip CAPTCHA on code presence.
// Creation snapshots current grants, whereas first-binding grants have
// separate eligibility and persistence. Reset verifies and attempts
// credential deletion before the account hash write, then diagnoses refresh
// cleanup failure after persistence.

// These authentication errors identify credential, account, token, policy and
// challenge failures for HTTP callers while preserving the shared identities
// used by wrapped-error checks.
var (
	ErrInvalidCredentials = infraerrors.Unauthorized("INVALID_CREDENTIALS", "invalid email or password")
	ErrUserNotActive      = infraerrors.Forbidden("USER_NOT_ACTIVE", "user is not active")
	ErrEmailExists        = infraerrors.Conflict("EMAIL_EXISTS", "email already exists")
	ErrEmailReserved      = infraerrors.BadRequest("EMAIL_RESERVED", "email is reserved")
	ErrInvalidToken       = infraerrors.Unauthorized("INVALID_TOKEN", "invalid token")
	ErrTokenExpired       = infraerrors.Unauthorized("TOKEN_EXPIRED", "token has expired")

	ErrTokenTooLarge       = infraerrors.BadRequest("TOKEN_TOO_LARGE", "token too large")
	ErrTokenRevoked        = infraerrors.Unauthorized("TOKEN_REVOKED", "token has been revoked")
	ErrRefreshTokenInvalid = infraerrors.Unauthorized("REFRESH_TOKEN_INVALID", "invalid refresh token")
	ErrRefreshTokenExpired = infraerrors.Unauthorized("REFRESH_TOKEN_EXPIRED", "refresh token has expired")

	ErrEmailVerifyRequired          = infraerrors.BadRequest("EMAIL_VERIFY_REQUIRED", "email verification is required")
	ErrEmailSuffixNotAllowed        = infraerrors.BadRequest("EMAIL_SUFFIX_NOT_ALLOWED", "email suffix is not allowed")
	ErrEmailDomainRegistrationLimit = infraerrors.BadRequest(
		"EMAIL_DOMAIN_REGISTRATION_LIMIT",
		"this email domain cannot register another account; use a mainstream email or contact us to add the enterprise domain",
	)
	ErrEmailChangeDisabled     = infraerrors.Forbidden("EMAIL_CHANGE_DISABLED", "email changes are disabled")
	ErrRegDisabled             = infraerrors.Forbidden("REGISTRATION_DISABLED", "registration is currently disabled")
	ErrServiceUnavailable      = infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "service temporarily unavailable")
	ErrInvitationCodeRequired  = infraerrors.BadRequest("INVITATION_CODE_REQUIRED", "invitation code is required")
	ErrInvitationCodeInvalid   = infraerrors.BadRequest("INVITATION_CODE_INVALID", "invalid or used invitation code")
	ErrOAuthInvitationRequired = infraerrors.Forbidden("OAUTH_INVITATION_REQUIRED", "invitation code required to complete oauth registration")
	ErrCaptchaProviderConflict = infraerrors.ServiceUnavailable("CAPTCHA_PROVIDER_CONFLICT", "multiple captcha providers are enabled")

	// ErrPasswordIncorrect reports a failed current-password check.
	ErrPasswordIncorrect = infraerrors.BadRequest("PASSWORD_INCORRECT", "current password is incorrect")
)

// CaptchaProof carries frontend challenge values interpreted according to the
// selected runtime provider.
type CaptchaProof struct {
	// TurnstileToken carries Cloudflare token text or Aliyun captchaVerifyParam.
	// The selected provider determines which verifier consumes this shared
	// request field.
	TurnstileToken string
	// TencentTicket and TencentRandstr come from the same frontend verification
	// result. Tencent admission passes the pair with the caller's remote IP and
	// current configured application.
	TencentTicket  string // Tencent challenge ticket text
	TencentRandstr string // Tencent challenge random string
}

// DefaultSubscriptionAssigner grants or extends a selected billing plan for
// the signup account.
type DefaultSubscriptionAssigner interface {
	// AssignOrExtendSubscription applies a required nonnil account/plan request
	// and returns the resulting entitlement plus its queued/pending flag. Signup
	// callers ignore successful return details and diagnose errors according to
	// their optional-step handling.
	AssignOrExtendSubscription(ctx context.Context, input *AssignSubscriptionInput) (*UserSubscription, bool, error)
}

// AuthSignupGrantPlan contains the amounts and plan selections observed for
// account creation.
type AuthSignupGrantPlan struct {
	Balance     float64 // initial signup balance in USD
	Concurrency int     // initial concurrent-request account limit
	// Subscriptions retains the selected plan slice from settings resolution.
	// Callers keep it stable through account creation and subsequent assignment
	// of those choices.
	Subscriptions []DefaultSubscriptionSetting
}

// SendVerifyCodeResult supplies the frontend delay shown after accepting a
// challenge request.
type SendVerifyCodeResult struct {
	Countdown int `json:"countdown"` // seconds
}

// AuthRegistrationArtifacts carries invitation observations and quota
// selection for guarded account creation.
type AuthRegistrationArtifacts struct {
	// InvitationRedeemCode holds the preflight record when policy requires an
	// invitation. Creation performs conditional use later; callers keep this
	// observation stable through that write.
	InvitationRedeemCode *RedeemCode
	// EnforceEmailDomainQuota requests creation-time domain policy and capacity
	// rechecking. Local registration and pending email creation enable it, while
	// verified-email automatic login uses strict suffix policy.
	EnforceEmailDomainQuota bool
}

// RegisterWithVerification creates an email account after current
// registration, occupancy, domain and applicable mailbox/invitation checks.
// It snapshots signup balance/concurrency/RPM, delegates guarded
// account/invitation persistence and then requests source/login bootstrap,
// plans and affiliate effects before returning an access token.
//
// Promotion failure is diagnostic and a failed promotion readback retains the
// working account. Optional post-create effects and eventual token-generation
// failure do not remove the created account here. Callers supply shaped
// email/password input and perform CAPTCHA admission at their HTTP entry;
// this core operation chooses whether a local email code is required from its
// current settings read.
func (s *AuthService) RegisterWithVerification(ctx context.Context, email, password, verifyCode, promoCode, invitationCode, affiliateCode string) (string, *User, error) {
	if s.Settings == nil || !s.Settings.IsRegistrationEnabled(ctx) {
		return "", nil, ErrRegDisabled
	}

	if IsReservedEmail(email) {
		return "", nil, ErrEmailReserved
	}
	existsEmail, err := s.AuthRegistrationEmailExists(ctx, email)
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Database error checking email exists: %v", err)
		return "", nil, ErrServiceUnavailable
	}
	if existsEmail {
		return "", nil, ErrEmailExists
	}
	if err := s.AuthValidateRegistrationEmailQuota(ctx, email); err != nil {
		return "", nil, err
	}

	artifacts, err := s.AuthResolveRegistrationArtifacts(ctx, invitationCode, ErrInvitationCodeRequired)
	if err != nil {
		return "", nil, err
	}
	artifacts.EnforceEmailDomainQuota = true

	if s.Settings != nil && s.Settings.IsEmailVerifyEnabled(ctx) {
		if s.Email == nil {
			s.Observer.Printf("service.auth", "%s", "[Auth] Email verification enabled but email service not configured, rejecting registration")
			return "", nil, ErrServiceUnavailable
		}
		if verifyCode == "" {
			return "", nil, ErrEmailVerifyRequired
		}

		if err := s.Email.VerifyCode(ctx, email, verifyCode); err != nil {
			return "", nil, fmt.Errorf("verify code: %w", err)
		}
	}

	hashedPassword, err := s.HashPassword(password)
	if err != nil {
		return "", nil, fmt.Errorf("hash password: %w", err)
	}

	grantPlan := s.AuthResolveSignupGrantPlan(ctx, "email")

	// Snapshot current RPM before creating this account; the saved limit stays
	// with the account when administrators later change the global signup
	// default.
	var defaultRPMLimit int
	if s.Settings != nil {
		defaultRPMLimit = s.Settings.GetDefaultUserRPMLimit(ctx)
	}

	user := &User{
		Email:        email,
		PasswordHash: hashedPassword,
		Role:         RoleUser,
		Balance:      grantPlan.Balance,
		Concurrency:  grantPlan.Concurrency,
		RPMLimit:     defaultRPMLimit,
		Status:       StatusActive,
	}
	if err := s.AuthCreateRegisteredUser(ctx, user, artifacts); err != nil {
		// Creation repeats guarded occupancy/domain/invitation admission after
		// earlier reads, preserving their specific rejection identities when
		// another request or current policy changed the outcome.
		switch {
		case errors.Is(err, ErrEmailExists):
			return "", nil, ErrEmailExists
		case errors.Is(err, ErrEmailDomainRegistrationLimit):
			return "", nil, ErrEmailDomainRegistrationLimit
		case errors.Is(err, ErrInvitationCodeInvalid):
			return "", nil, ErrInvitationCodeInvalid
		default:
			s.Observer.Printf("service.auth", "[Auth] Database error creating user: %v", err)
			return "", nil, ErrServiceUnavailable
		}
	}
	// Account/invitation creation has returned successfully before source, plan
	// and affiliate requests; later optional or token failures do not trigger
	// general account cleanup here.
	s.AuthPostAuthUserBootstrap(ctx, user, "email", true)
	s.AuthAssignSubscriptions(ctx, user.ID, grantPlan.Subscriptions, "auto assigned by signup defaults")
	s.AuthBindRegistrationAffiliate(ctx, user.ID, affiliateCode)

	if promoCode != "" && s.Promo != nil && s.Settings != nil && s.Settings.IsPromoCodeEnabled(ctx) {
		if err := s.Promo.ApplyPromoCode(ctx, user.ID, promoCode); err != nil {
			// The account is already created, so promotion failure is diagnostic and
			// token preparation continues with the account observation available
			// before that grant attempt.
			s.Observer.Printf("service.auth", "[Auth] Failed to apply promo code for user %d: %v", user.ID, err)
		} else {
			// A successful grant can change stored funds; use a readable current
			// account, but keep the working account when this optional readback fails.
			if updatedUser, err := s.Users.GetByID(ctx, user.ID); err == nil {
				user = updatedUser
			}
		}
	}

	token, err := s.GenerateToken(ctx, user)
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}

	return token, user, nil
}

// SendVerifyCode sends a registration mailbox challenge synchronously after
// current registration, reserved-address, occupancy and domain quota checks.
// Missing mail service is a configuration error; selected locale/site text
// goes to delivery and its failures remain errors before any account
// creation.
func (s *AuthService) SendVerifyCode(ctx context.Context, email string, locale ...string) error {
	if s.Settings == nil || !s.Settings.IsRegistrationEnabled(ctx) {
		return ErrRegDisabled
	}

	if IsReservedEmail(email) {
		return ErrEmailReserved
	}

	existsEmail, err := s.AuthRegistrationEmailExists(ctx, email)
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Database error checking email exists: %v", err)
		return ErrServiceUnavailable
	}
	if existsEmail {
		return ErrEmailExists
	}
	if err := s.AuthValidateRegistrationEmailQuota(ctx, email); err != nil {
		return err
	}

	if s.Email == nil {
		return errors.New("email service not configured")
	}

	siteName := "TokenRouter"
	if s.Settings != nil {
		siteName = s.Settings.GetSiteName(ctx)
	}

	return s.Email.SendVerifyCode(ctx, email, siteName, FirstEmailLocale(locale))
}

// SendVerifyCodeAsync queues a registration challenge after the same current
// policy/occupancy/domain preflight. Missing queue returns a configuration
// error and enqueue failure is wrapped; success returns a fixed sixty-second
// display countdown, while actual code preparation/delivery happens later in
// the worker.
func (s *AuthService) SendVerifyCodeAsync(ctx context.Context, email string, locale ...string) (*SendVerifyCodeResult, error) {
	s.Observer.Printf("service.auth", "[Auth] SendVerifyCodeAsync called for email: %s", email)

	if s.Settings == nil || !s.Settings.IsRegistrationEnabled(ctx) {
		s.Observer.Printf("service.auth", "%s", "[Auth] Registration is disabled")
		return nil, ErrRegDisabled
	}

	if IsReservedEmail(email) {
		return nil, ErrEmailReserved
	}

	existsEmail, err := s.AuthRegistrationEmailExists(ctx, email)
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Database error checking email exists: %v", err)
		return nil, ErrServiceUnavailable
	}
	if existsEmail {
		s.Observer.Printf("service.auth", "[Auth] Email already exists: %s", email)
		return nil, ErrEmailExists
	}
	if err := s.AuthValidateRegistrationEmailQuota(ctx, email); err != nil {
		return nil, err
	}

	if s.EmailQueue == nil {
		s.Observer.Printf("service.auth", "%s", "[Auth] Email queue service not configured")
		return nil, errors.New("email queue service not configured")
	}

	siteName := "TokenRouter"
	if s.Settings != nil {
		siteName = s.Settings.GetSiteName(ctx)
	}

	s.Observer.Printf("service.auth", "[Auth] Enqueueing verify code for: %s", email)
	if err := s.EmailQueue.EnqueueVerifyCode(email, siteName, FirstEmailLocale(locale)); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to enqueue: %v", err)
		return nil, fmt.Errorf("enqueue verify code: %w", err)
	}

	s.Observer.Printf("service.auth", "[Auth] Verify code enqueued successfully for: %s", email)
	return &SendVerifyCodeResult{
		// Use the registration challenge display cooldown in whole seconds.
		Countdown: 60, // seconds
	}, nil
}

// VerifyCaptchaForRegister selects registration challenge handling from the
// current email switch and submitted code presence. A nonblank code skips
// CAPTCHA verification without proving that code or an earlier challenge
// request; RegisterWithVerification separately performs its applicable
// local-code check.
func (s *AuthService) VerifyCaptchaForRegister(ctx context.Context, proof CaptchaProof, remoteIP, verifyCode string) error {
	// Code presence identifies the email-completion branch and skips another
	// CAPTCHA call; this helper does not establish proof, leaving the later
	// registration code check separate.
	if s.IsEmailVerifyEnabled(ctx) && strings.TrimSpace(verifyCode) != "" {
		s.Observer.Printf("service.auth", "%s", "[Auth] Email verify flow detected, skip duplicate captcha check on register")
		return nil
	}
	return s.VerifyCaptcha(ctx, proof, remoteIP)
}

// VerifyCaptcha verifies the runtime-selected generic challenge provider for
// a shaped proof. More than one enabled provider is a conflict; configuration
// read failure rejects admission, and an enabled provider requires its
// corresponding verifier and applicable credentials.
//
// Release-mode startup Required means a usable selected challenge is
// required, so Tencent or Aliyun can satisfy it even though the startup
// option is named Turnstile. With no settings or enabled provider, this
// requirement determines rejection versus success. The caller supplies the
// remote IP and provider-matched proof; upstream verification owns proof
// validity after this policy selection.
func (s *AuthService) VerifyCaptcha(ctx context.Context, proof CaptchaProof, remoteIP string) error {
	enabled, err := s.VerifyOptionalCaptcha(ctx, proof, remoteIP)
	if err != nil {
		return err
	}
	required := s.Options != nil && s.Options.Server.Mode == "release" && s.Options.Turnstile.Required
	if required && !enabled {
		return ErrTurnstileNotConfigured
	}
	return nil
}

// VerifyOptionalCaptcha checks the selected provider and reports whether verification is enabled.
// A disabled site succeeds without a challenge, allowing capability callers to
// decide their own admission policy. Authentication uses VerifyCaptcha to also
// enforce its startup requirement that a provider must be configured.
//
// Selection and verification consume one settings snapshot, so the returned
// enabled flag describes the configuration actually used for this attempt.
// Read failures and conflicting providers reject verification. Enabled providers
// require their verifier and credentials; their existing proof and remote-IP
// handling remains responsible for the external challenge decision.
func (s *AuthService) VerifyOptionalCaptcha(ctx context.Context, proof CaptchaProof, remoteIP string) (bool, error) {
	if s.Settings == nil {
		return false, nil
	}
	providerConfig, err := s.Settings.GetCaptchaProviderConfig(ctx)
	if err != nil {
		s.Observer.Printf("service.auth", "%s", "[Auth] Failed to read captcha provider settings")
		return false, ErrServiceUnavailable
	}
	turnstileEnabled := providerConfig.TurnstileEnabled
	tencentEnabled := providerConfig.Tencent.Enabled
	aliyunEnabled := providerConfig.Aliyun.Enabled
	if AuthCaptchaProvidersConflict(turnstileEnabled, tencentEnabled, aliyunEnabled) {
		return true, ErrCaptchaProviderConflict
	}
	if tencentEnabled {
		if s.Tencent == nil {
			return true, ErrTencentCaptchaNotConfigured
		}
		return true, s.Tencent.VerifyTicketWithConfig(ctx, providerConfig.Tencent, proof.TencentTicket, proof.TencentRandstr, remoteIP)
	}
	if aliyunEnabled {
		if s.Aliyun == nil {
			return true, ErrAliyunCaptchaNotConfigured
		}
		return true, s.Aliyun.VerifyParamWithConfig(ctx, providerConfig.Aliyun, proof.TurnstileToken)
	}
	if turnstileEnabled {
		if s.Turnstile == nil || strings.TrimSpace(providerConfig.TurnstileSecretKey) == "" {
			return true, ErrTurnstileNotConfigured
		}
		return true, s.Turnstile.VerifyTokenWithSecret(ctx, providerConfig.TurnstileSecretKey, proof.TurnstileToken, remoteIP)
	}
	return false, nil
}

// AuthCaptchaProvidersConflict reports whether multiple supplied provider
// switches are true.
func AuthCaptchaProvidersConflict(enabled ...bool) bool {
	count := 0
	for _, e := range enabled {
		if e {
			count++
		}
	}
	return count > 1
}

// VerifyActionCaptchaIfEnabled selects Tencent or Aliyun proof for
// OAuth/Passkey login starts. Missing service/settings or a failed
// configuration read rejects admission; when neither action provider is
// enabled, it returns success before the generic conflict check.
//
// Turnstile alone therefore does not guard these action starts. Once an
// action provider is enabled, combined-provider configuration is rejected and
// the selected verifier is required. Callers supply the proof generated for
// that action and remote IP, while protected current-user binding starts use
// their own authenticated-flow admission rather than this anonymous action
// gate.
func (s *AuthService) VerifyActionCaptchaIfEnabled(ctx context.Context, proof CaptchaProof, remoteIP string) error {
	if s == nil || s.Settings == nil {
		return ErrServiceUnavailable
	}

	providerConfig, err := s.Settings.GetCaptchaProviderConfig(ctx)
	if err != nil {
		s.Observer.Printf("service.auth", "%s", "[Auth] Failed to read captcha provider settings")
		return ErrServiceUnavailable
	}
	tencentEnabled := providerConfig.Tencent.Enabled
	aliyunEnabled := providerConfig.Aliyun.Enabled
	// Only the action providers gate these starts; without either, return before
	// generic conflict handling so Turnstile-only configuration leaves this
	// action path available.
	if !tencentEnabled && !aliyunEnabled {
		return nil
	}
	if AuthCaptchaProvidersConflict(providerConfig.TurnstileEnabled, tencentEnabled, aliyunEnabled) {
		return ErrCaptchaProviderConflict
	}
	if aliyunEnabled {
		if s.Aliyun == nil {
			return ErrAliyunCaptchaNotConfigured
		}
		return s.Aliyun.VerifyParamWithConfig(ctx, providerConfig.Aliyun, proof.TurnstileToken)
	}
	if s.Tencent == nil {
		return ErrTencentCaptchaNotConfigured
	}
	return s.Tencent.VerifyTicketWithConfig(
		ctx,
		providerConfig.Tencent,
		proof.TencentTicket,
		proof.TencentRandstr,
		remoteIP,
	)
}

// IsTurnstileEnabled reports the configured Turnstile service's runtime
// enablement when present. A missing verifier returns false; generic/action
// CAPTCHA admission additionally checks current provider conflicts and the
// dependencies needed for the selected proof.
func (s *AuthService) IsTurnstileEnabled(ctx context.Context) bool {
	if s.Turnstile == nil {
		return false
	}
	return s.Turnstile.IsEnabled(ctx)
}

// IsRegistrationEnabled reports the current ordinary-registration switch from
// assembled settings. Missing settings returns false, allowing entry points
// to reject creation before user writes; provider-specific bypass selection
// belongs to its separate predicate.
func (s *AuthService) IsRegistrationEnabled(ctx context.Context) bool {
	if s.Settings == nil {
		return false // Missing settings disable registration.
	}
	return s.Settings.IsRegistrationEnabled(ctx)
}

// IsEmailVerifyEnabled reports whether ordinary registration requests a local
// mailbox challenge. Missing settings returns false; pending OAuth email
// completion and verified email binding request their own proof independently
// of this switch.
func (s *AuthService) IsEmailVerifyEnabled(ctx context.Context) bool {
	if s.Settings == nil {
		return false
	}
	return s.Settings.IsEmailVerifyEnabled(ctx)
}

// Login checks the supplied password against a current active account and
// returns its access token. Missing accounts and wrong passwords become
// invalid-credentials, while other lookup failures become
// service-unavailable; surrounding HTTP flows own CAPTCHA, second-factor and
// login-recording decisions.
func (s *AuthService) Login(ctx context.Context, email, password string) (string, *User, error) {
	user, err := s.Users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return "", nil, ErrInvalidCredentials
		}
		// A failed account read supplies no credentials to check; diagnose that
		// failure and return service-unavailable, while missing accounts retain the
		// invalid-credentials response.
		s.Observer.Printf("service.auth", "[Auth] Database error during login: %v", err)
		return "", nil, ErrServiceUnavailable
	}

	if !s.CheckPassword(password, user.PasswordHash) {
		return "", nil, ErrInvalidCredentials
	}

	if !user.IsActive() {
		return "", nil, ErrUserNotActive
	}

	token, err := s.GenerateToken(ctx, user)
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}

	return token, user, nil
}

// AuthCanBypassRegistrationDisabledForOAuth recognizes the DingTalk
// closed-registration exception from current provider configuration.
// signupSource must equal dingtalk exactly; an unreadable, disabled or
// non-bypassing provider returns false, and only internal_only policy permits
// bypass.
//
// The settings reader can validate provider configuration before this result,
// but this predicate does not establish directory membership or
// provider-token proof. Callers perform those checks in the upstream flow and
// pass the normalized source before testing the exception. Later source
// normalization cannot retroactively change this earlier exact-label
// admission decision.
func (s *AuthService) AuthCanBypassRegistrationDisabledForOAuth(ctx context.Context, signupSource string) bool {
	if signupSource != "dingtalk" {
		return false
	}
	cfg, err := s.Settings.GetDingTalkConnectOAuthConfig(ctx)
	// Unreadable or non-bypassing configuration supplies no exception to closed
	// registration; preserve the denied result while provider membership proof
	// remains in the upstream flow.
	if err != nil || !cfg.Enabled || !cfg.BypassRegistration {
		return false
	}
	return cfg.CorpRestrictionPolicy == "internal_only"
}

// LoginOrRegisterOAuthWithTokenPair delegates OAuth email-based login or
// creation without a promotion code. The caller supplies verified provider
// context; current registration/invitation policy controls missing accounts,
// and signupSource selects grant defaults and the DingTalk exception.
func (s *AuthService) LoginOrRegisterOAuthWithTokenPair(ctx context.Context, email, username, invitationCode, affiliateCode, signupSource string) (*TokenPair, *User, error) {
	return s.AuthLoginOrRegisterOAuthWithTokenPair(ctx, email, username, invitationCode, affiliateCode, "", signupSource)
}

// LoginOrRegisterOAuthWithTokenPairAndPromoCode delegates OAuth email-based
// login or creation with promotion text. Promotion applies after this generic
// path actually creates an account; existing accounts and uniqueness-conflict
// recovery keep their separate existing-account behavior.
func (s *AuthService) LoginOrRegisterOAuthWithTokenPairAndPromoCode(ctx context.Context, email, username, invitationCode, affiliateCode, promoCode, signupSource string) (*TokenPair, *User, error) {
	return s.AuthLoginOrRegisterOAuthWithTokenPair(ctx, email, username, invitationCode, affiliateCode, promoCode, signupSource)
}

// AuthLoginOrRegisterOAuthWithTokenPair resolves a supplied OAuth mailbox to
// an active account or creates one under current registration/invitation
// policy. It requires refresh storage, validates trimmed email text within
// 255 bytes without extracting parsed.Address, and bounds the supplied
// username to one hundred complete runes.
//
// Creation snapshots provider/global grants and RPM, then runs
// source/plan/affiliate work. An email conflict reloads the current account
// and keeps created=false, so those successful-creation effects and later
// promotion are skipped. An optional name save can fail diagnostically after
// the working name changed. Token-pair generation occurs last and does not
// roll back completed account or signup effects; stable provider identity
// handling belongs to the surrounding OAuth flow.
func (s *AuthService) AuthLoginOrRegisterOAuthWithTokenPair(ctx context.Context, email, username, invitationCode, affiliateCode, promoCode, signupSource string) (*TokenPair, *User, error) {
	if s.RefreshTokens == nil {
		return nil, nil, errors.New("refresh token cache not configured")
	}

	email = strings.TrimSpace(email)
	// Apply the mailbox byte budget before parsing the supplied provider hint.
	if email == "" || len(email) > 255 {
		return nil, nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}

	username = strings.TrimSpace(username)
	// Retain complete characters within the account display-name rune budget.
	if len([]rune(username)) > 100 {
		username = string([]rune(username)[:100])
	}

	user, err := s.Users.GetByEmail(ctx, email)
	created := false
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			if s.Settings == nil || (!s.Settings.IsRegistrationEnabled(ctx) && !s.AuthCanBypassRegistrationDisabledForOAuth(ctx, signupSource)) {
				return nil, nil, ErrRegDisabled
			}

			artifacts, err := s.AuthResolveRegistrationArtifacts(ctx, invitationCode, ErrOAuthInvitationRequired)
			if err != nil {
				return nil, nil, err
			}

			randomPassword, err := RandomHexString(32)
			if err != nil {
				s.Observer.Printf("service.auth", "[Auth] Failed to generate random password for oauth signup: %v", err)
				return nil, nil, ErrServiceUnavailable
			}
			hashedPassword, err := s.HashPassword(randomPassword)
			if err != nil {
				return nil, nil, fmt.Errorf("hash password: %w", err)
			}

			// An omitted provider label uses legacy synthetic-address inference; a
			// supplied source remains authoritative for grants even when this mailbox
			// is a real address.
			if strings.TrimSpace(signupSource) == "" {
				signupSource = AuthInferLegacySignupSource(email)
			}
			grantPlan := s.AuthResolveSignupGrantPlan(ctx, signupSource)
			var defaultRPMLimit int
			if s.Settings != nil {
				defaultRPMLimit = s.Settings.GetDefaultUserRPMLimit(ctx)
			}

			newUser := &User{
				Email:        email,
				Username:     username,
				PasswordHash: hashedPassword,
				Role:         RoleUser,
				Balance:      grantPlan.Balance,
				Concurrency:  grantPlan.Concurrency,
				RPMLimit:     defaultRPMLimit,
				Status:       StatusActive,
				SignupSource: signupSource,
			}
			if err := s.AuthCreateRegisteredUser(ctx, newUser, artifacts); err != nil {
				// A create conflict can follow an earlier missing-account read; reload
				// the winner while leaving created false so this path skips
				// successful-creation signup effects.
				if errors.Is(err, ErrEmailExists) {
					user, err = s.Users.GetByEmail(ctx, email)
					if err != nil {
						if errors.Is(err, ErrUserNotFound) {
							return nil, nil, ErrEmailExists
						}
						s.Observer.Printf("service.auth", "[Auth] Database error getting user after conflict: %v", err)
						return nil, nil, ErrServiceUnavailable
					}
				} else if errors.Is(err, ErrInvitationCodeInvalid) {
					return nil, nil, ErrInvitationCodeInvalid
				} else {
					s.Observer.Printf("service.auth", "[Auth] Database error creating oauth user: %v", err)
					return nil, nil, ErrServiceUnavailable
				}
			} else {
				user = newUser
				created = true
				s.AuthPostAuthUserBootstrap(ctx, user, signupSource, false)
				s.AuthAssignSubscriptions(ctx, user.ID, grantPlan.Subscriptions, "auto assigned by signup defaults")
				s.AuthBindRegistrationAffiliate(ctx, user.ID, affiliateCode)
			}
		} else {
			s.Observer.Printf("service.auth", "[Auth] Database error during oauth login: %v", err)
			return nil, nil, ErrServiceUnavailable
		}
	}

	if !user.IsActive() {
		return nil, nil, ErrUserNotActive
	}

	if user.Username == "" && username != "" {
		user.Username = username
		// Name hydration is optional after account resolution; diagnose a failed
		// save and continue issuing tokens even though the working account now
		// carries that hint.
		if err := s.Users.Update(ctx, user, UserUpdateFields{Username: true}); err != nil {
			s.Observer.Printf("service.auth", "[Auth] Failed to update username after oauth login: %v", err)
		}
	}
	if created {
		user = s.AuthApplyOAuthSignupPromoCode(ctx, user, promoCode)
	}
	tokenPair, err := s.GenerateTokenPair(ctx, user, "")
	if err != nil {
		return nil, nil, fmt.Errorf("generate token pair: %w", err)
	}
	return tokenPair, user, nil
}

// ApplyOAuthSignupPromoCode requests promotion application for a positive
// account ID and discards the returned refreshed account. Invalid IDs skip
// work; the delegated helper checks policy, diagnoses grant failure and
// retains working account data when its readback fails.
func (s *AuthService) ApplyOAuthSignupPromoCode(ctx context.Context, userID int64, promoCode string) {
	if userID <= 0 {
		return
	}
	s.AuthApplyOAuthSignupPromoCode(ctx, &User{ID: userID}, promoCode)
}

// AuthApplyOAuthSignupPromoCode applies an eligible promotion and returns a
// refreshed account when its readback succeeds. Missing account/dependencies,
// invalid ID, blank trimmed code or disabled policy returns the supplied
// pointer.
//
// Grant failure is diagnosed and retains that pointer; successful grant
// followed by read failure also retains it even though the grant can already
// be saved. The returned account is therefore a best available observation,
// not a promotion-completion receipt. Callers retain creation/finalization
// state separately and do not infer that a reused pointer means no persistent
// grant happened.
func (s *AuthService) AuthApplyOAuthSignupPromoCode(ctx context.Context, user *User, promoCode string) *User {
	promoCode = strings.TrimSpace(promoCode)
	if user == nil || user.ID <= 0 || promoCode == "" || s.Promo == nil || s.Settings == nil || !s.Settings.IsPromoCodeEnabled(ctx) {
		return user
	}
	// Promotion failure does not remove the signup account; diagnose it and
	// retain the supplied account while the caller continues its authentication
	// result.
	if err := s.Promo.ApplyPromoCode(ctx, user.ID, promoCode); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to apply promo code for oauth user %d: %v", user.ID, err)
		return user
	}
	// The promotion may already be stored; prefer current account readback and
	// otherwise retain the earlier working data rather than infer that the grant
	// was absent.
	if updatedUser, err := s.Users.GetByID(ctx, user.ID); err == nil {
		return updatedUser
	}
	return user
}

// AuthAssignSubscriptions requests each configured signup plan for a positive
// account with settings and an assigner. Missing dependencies or unusable
// account ID skips work; every plan receives the supplied notes and account
// identifier in its own storage helper call.
//
// AuthRunFailOpenDBStep can use a savepoint inside an existing PostgreSQL
// transaction to handle an optional failed assignment. Returned assignment or
// savepoint errors are diagnosed and iteration continues. Successful earlier
// plans can remain applied when a later request fails, and the enclosing
// transaction still owns eventual commit or rollback; this helper returns no
// aggregate grant result.
func (s *AuthService) AuthAssignSubscriptions(ctx context.Context, userID int64, items []DefaultSubscriptionSetting, notes string) {
	if s.Settings == nil || s.DefaultSubscriptions == nil || userID <= 0 {
		return
	}
	// Attempt each optional plan through its own savepoint helper; diagnose
	// failures and continue, while completed earlier plans and the surrounding
	// transaction keep their own outcomes.
	for _, item := range items {
		err := s.AuthRunFailOpenDBStep(ctx, "auth_default_subscription", func(stepCtx context.Context) error {
			_, _, err := s.DefaultSubscriptions.AssignOrExtendSubscription(stepCtx, &AssignSubscriptionInput{
				UserID: userID,
				PlanID: item.PlanID,
				Notes:  notes,
			})
			return err
		})
		if err != nil {
			s.Observer.Printf("service.auth", "[Auth] Failed to assign default subscription: user_id=%d plan_id=%d err=%v", userID, item.PlanID, err)
		}
	}
}

// AuthResolveRegistrationArtifacts returns invitation data selected by
// current invitation policy. Missing settings or a disabled invitation switch
// produces empty artifacts; an enabled switch requires the untrimmed code, rejects
// lookup or unusable state and retains its record pointer for later
// conditional use during creation.
func (s *AuthService) AuthResolveRegistrationArtifacts(ctx context.Context, invitationCode string, missingInvitationErr error) (*AuthRegistrationArtifacts, error) {
	artifacts := &AuthRegistrationArtifacts{}

	if s.Settings != nil && s.Settings.IsInvitationCodeEnabled(ctx) {
		if invitationCode == "" {
			return nil, missingInvitationErr
		}
		redeemCode, err := s.Redeem.GetByCode(ctx, invitationCode)
		if err != nil {
			s.Observer.Printf("service.auth", "[Auth] Invalid invitation code: %s, error: %v", invitationCode, err)
			return nil, ErrInvitationCodeInvalid
		}
		if redeemCode.Type != RedeemTypeInvitation || !redeemCode.CanUse() {
			s.Observer.Printf("service.auth", "[Auth] Invitation code invalid: type=%s, status=%s", redeemCode.Type, redeemCode.Status)
			return nil, ErrInvitationCodeInvalid
		}
		artifacts.InvitationRedeemCode = redeemCode
	}

	return artifacts, nil
}

// AuthBindRegistrationAffiliate establishes optional affiliate data for an
// already created positive account. Missing service/affiliate or invalid ID
// skips work; the profile initialization request precedes any nonblank
// trimmed inviter-code binding.
//
// Both requests use the optional database-step helper, but profile failure is
// diagnosed and skips inviter binding, while inviter failure is diagnosed
// after the profile step. Signup continues because the method returns no
// error. Callers keep account creation and enclosing transaction completion
// separate from proof that either optional affiliate effect succeeded.
func (s *AuthService) AuthBindRegistrationAffiliate(ctx context.Context, userID int64, affiliateCode string) {
	if s == nil || s.Affiliate == nil || userID <= 0 {
		return
	}
	// Initialize affiliate profile before binding an inviter; a failed optional
	// profile step is diagnosed and skips binding without changing the
	// created-account result.
	if err := s.AuthRunFailOpenDBStep(ctx, "auth_affiliate_profile", func(stepCtx context.Context) error {
		err := s.Affiliate.EnsureUserAffiliate(stepCtx, userID)
		return err
	}); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to initialize affiliate profile for user %d: %v", userID, err)
		return
	}
	if code := strings.TrimSpace(affiliateCode); code != "" {
		if err := s.AuthRunFailOpenDBStep(ctx, "auth_affiliate_bind_inviter", func(stepCtx context.Context) error {
			return s.Affiliate.BindInviterByCode(stepCtx, userID, code)
		}); err != nil {
			// The profile step already returned successfully; diagnose inviter-binding
			// failure and keep signup progressing with the account and any completed
			// affiliate profile effect.
			s.Observer.Printf("service.auth", "[Auth] Failed to bind affiliate inviter for user %d: %v", userID, err)
		}
	}
}

// AuthResolveSignupGrantPlan chooses account-creation amounts and plans from
// startup, global and provider settings. Missing service/settings retains
// startup balance/concurrency and no startup plan list; configured global
// reads replace those values before provider resolution.
//
// A provider read error is diagnosed and retains the observed global plan.
// Only an enabled readable signup result replaces all selected fields; the
// settings reader owns its provider/global value merge. These reads need not
// share one snapshot, so callers use the returned plan as the creation
// observation and keep later first-binding grants as a separate action.
func (s *AuthService) AuthResolveSignupGrantPlan(ctx context.Context, signupSource string) AuthSignupGrantPlan {
	plan := AuthSignupGrantPlan{}
	if s != nil && s.Options != nil {
		plan.Balance = s.Options.Default.UserBalance
		plan.Concurrency = s.Options.Default.UserConcurrency
	}
	if s == nil || s.Settings == nil {
		return plan
	}

	plan.Balance = s.Settings.GetDefaultBalance(ctx)
	plan.Concurrency = s.Settings.GetDefaultConcurrency(ctx)
	plan.Subscriptions = s.Settings.GetDefaultSubscriptions(ctx)

	// Global values have already been observed; provider read failure or
	// disabled signup retains that plan, while a usable enabled result replaces
	// its selected values.
	resolved, enabled, err := s.Settings.ResolveAuthSourceGrantSettings(ctx, signupSource, false)
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to load auth source signup defaults for %s: %v", signupSource, err)
		return plan
	}
	if !enabled {
		return plan
	}

	plan.Balance = resolved.Balance
	plan.Concurrency = resolved.Concurrency
	plan.Subscriptions = resolved.Subscriptions

	return plan
}

// AuthAuthSourceSignupSettings selects one recognized lowercased/trimmed
// provider view from supplied settings. Nil settings or unknown labels
// returns a zero value and false; returned plan slices remain shared with
// that settings view and are kept stable by consumers.
func AuthAuthSourceSignupSettings(defaults *AuthSourceDefaultSettings, signupSource string) (ProviderDefaultGrantSettings, bool) {
	if defaults == nil {
		return ProviderDefaultGrantSettings{}, false
	}

	switch strings.ToLower(strings.TrimSpace(signupSource)) {
	case "email":
		return defaults.Email, true
	case "linuxdo":
		return defaults.LinuxDo, true
	case "oidc":
		return defaults.OIDC, true
	case "wechat":
		return defaults.WeChat, true
	case "github":
		return defaults.GitHub, true
	case "google":
		return defaults.Google, true
	case "dingtalk":
		return defaults.DingTalk, true
	default:
		return ProviderDefaultGrantSettings{}, false
	}
}

// AuthPostAuthUserBootstrap requests source recording and optional login
// timestamps for a created positive account. Missing or nonpositive account
// skips work; blank trimmed source becomes email, while nonblank supplied
// source text passes through unchanged.
//
// Source recording runs first and exposes no error result, so optional
// touchLogin work can follow a diagnosed or skipped source update. The two
// storage operations have their own completion and current-client behavior.
// This helper establishes post-create requests, while the caller owns token
// issuance and any surrounding transaction; it does not produce a combined
// persisted-bootstrap result.
func (s *AuthService) AuthPostAuthUserBootstrap(ctx context.Context, user *User, signupSource string, touchLogin bool) {
	if user == nil || user.ID <= 0 {
		return
	}

	if strings.TrimSpace(signupSource) == "" {
		signupSource = "email"
	}
	// Request source recording before optional login timestamps; its void
	// storage helper can diagnose failure while the following timestamp request
	// still runs.
	s.AuthUpdateUserSignupSource(ctx, user.ID, signupSource)

	if touchLogin {
		s.AuthTouchUserLogin(ctx, user.ID)
	}
}

// AuthBackfillEmailIdentityOnSuccessfulLogin requests email identity backfill
// and eligible first-binding grants for a positive account. Storage uses
// identity source and grant-record observations to select eligibility; fresh
// login-backfill source is skipped, while compatible existing source can
// select an unrecorded grant.
//
// A returned grant failure is diagnosed after backfill. Storage can also
// return nil identity on skipped or failed backfill, and PostgreSQL
// eligibility reads identity.Metadata before its later nil guard. This
// wrapper passes that result through unchanged, so unsuccessful backfill can
// panic instead of becoming a skipped grant; callers cannot infer failure
// isolation from its initial account checks.
func (s *AuthService) AuthBackfillEmailIdentityOnSuccessfulLogin(ctx context.Context, user *User) {
	if s == nil || user == nil || user.ID <= 0 {
		return
	}
	identity, created := s.AuthEnsureEmailAuthIdentity(ctx, user, "auth_service_login_backfill")
	if s.AuthShouldApplyEmailFirstBindDefaults(ctx, user.ID, identity, created) {
		// An eligible stored identity selects idempotent grants; a returned grant
		// failure is diagnostic after login proof and does not undo completed
		// backfill.
		if err := s.ApplyProviderDefaultSettingsOnFirstBind(ctx, user.ID, "email"); err != nil {
			s.Observer.Printf("service.auth", "[Auth] Failed to apply email first bind defaults: user_id=%d err=%v", user.ID, err)
		}
	}
}

// AuthEmailAuthIdentitySource returns trimmed fmt.Sprint text for a present
// metadata source. Missing map/key returns empty text; values are not
// restricted to strings and a present nil becomes <nil>, so source predicates
// compare the resulting representation.
func AuthEmailAuthIdentitySource(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	raw, ok := metadata["source"]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

// AuthInferLegacySignupSource identifies four reserved provider-email
// suffixes after trimming/lowercasing. Other text becomes email; legacy OAuth
// callers use this only when source was omitted, keeping a supplied provider
// source authoritative for grant selection.
func AuthInferLegacySignupSource(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	// Reserved compatibility addresses retain their provider grant namespace
	// when a caller omitted source; ordinary or unrecognized mailbox text
	// selects email for that legacy inference.
	switch {
	case strings.HasSuffix(normalized, DingTalkConnectSyntheticEmailDomain):
		return "dingtalk"
	case strings.HasSuffix(normalized, LinuxDoConnectSyntheticEmailDomain):
		return "linuxdo"
	case strings.HasSuffix(normalized, OIDCConnectSyntheticEmailDomain):
		return "oidc"
	case strings.HasSuffix(normalized, WeChatConnectSyntheticEmailDomain):
		return "wechat"
	default:
		return "email"
	}
}

// AuthValidateRegistrationEmailPolicy applies strict current mailbox suffix
// policy for binding and verified-provider login. Missing settings permits
// input and an empty whitelist allows any suffix; registration-domain quota
// is a separate policy used by creation/challenge flows.
func (s *AuthService) AuthValidateRegistrationEmailPolicy(ctx context.Context, email string) error {
	if s.Settings == nil {
		return nil
	}
	whitelist := s.Settings.GetRegistrationEmailSuffixWhitelist(ctx)
	if !IsRegistrationEmailSuffixAllowed(email, whitelist) {
		return AuthBuildEmailSuffixNotAllowedError(whitelist)
	}
	return nil
}

// AuthValidateRegistrationEmailQuota admits whitelisted domains and checks
// custom-domain capacity when current policy allows it. Missing
// service/settings or an unrestricted whitelist skips quota work; a limited
// suffix requires enabled quota and a usable registrable domain.
//
// Configured capacity lookup errors reject admission, and an occupied domain
// returns the domain-limit error. Missing guard capability is unavailable for
// database-backed services but accepted by database-free fixtures. These are
// preflight observations, not a reserved slot: guarded creation rechecks
// policy and capacity inside its persistence sequence before committing the
// account.
func (s *AuthService) AuthValidateRegistrationEmailQuota(ctx context.Context, email string) error {
	if s == nil || s.Settings == nil {
		return nil
	}
	whitelist := s.Settings.GetRegistrationEmailSuffixWhitelist(ctx)
	if !IsRegistrationEmailSuffixLimited(email, whitelist) {
		return nil
	}
	if !s.Settings.IsRegistrationEmailDomainQuotaEnabled(ctx) {
		return AuthBuildEmailSuffixNotAllowedError(whitelist)
	}
	domain, err := s.AuthRegistrationEmailDomainLimit(ctx, email)
	if err != nil || domain == "" {
		return err
	}
	quotaRepo := s.DomainRegistration
	ok := quotaRepo != nil
	if !ok {
		// Without a domain guard, database-backed creation cannot enforce its quota
		// sequence; reject this missing capability, while database-free fixtures
		// retain their optional-check behavior.
		if s.HasDatabase() {
			return ErrServiceUnavailable
		}
		return nil
	}
	count, err := quotaRepo.CountUsersByEmailDomain(ctx, domain)
	if err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to count registration email domain %s: %v", domain, err)
		return ErrServiceUnavailable
	}
	if count > 0 {
		return ErrEmailDomainRegistrationLimit
	}
	return nil
}

// AuthRegistrationEmailDomainLimit returns a limited registrable domain for
// guarded creation, or empty text when quota does not apply. It rereads
// current whitelist and quota enablement; a disallowed limited address or
// unusable registrable domain returns the suffix-policy error.
//
// The preflight and creation calls can observe different policy states, so
// this result selects the guard for the current operation rather than
// retaining an earlier authorization snapshot. Empty whitelist/whitelisted
// suffixes remain unrestricted. The caller holds the selected domain/address
// stable through the storage lock and capacity recheck that finally admits
// account creation.
func (s *AuthService) AuthRegistrationEmailDomainLimit(ctx context.Context, email string) (string, error) {
	if s == nil || s.Settings == nil {
		return "", nil
	}
	whitelist := s.Settings.GetRegistrationEmailSuffixWhitelist(ctx)
	if !IsRegistrationEmailSuffixLimited(email, whitelist) {
		return "", nil
	}
	// Creation calls this policy selector again inside its guarded sequence;
	// changed quota settings can reject an address that an earlier preflight
	// admitted.
	if !s.Settings.IsRegistrationEmailDomainQuotaEnabled(ctx) {
		return "", AuthBuildEmailSuffixNotAllowedError(whitelist)
	}
	domain := RegistrationEmailDomain(email)
	if domain == "" {
		return "", AuthBuildEmailSuffixNotAllowedError(whitelist)
	}
	return domain, nil
}

// AuthRegistrationEmailExists checks ordinary email occupancy first and adds
// mailbox-normalized occupancy when its current switch is enabled. Read
// errors remain errors; an occupied ordinary result stops the extra query,
// while creation separately rechecks destination uniqueness.
func (s *AuthService) AuthRegistrationEmailExists(ctx context.Context, email string) (bool, error) {
	exists, err := s.Users.ExistsByEmail(ctx, email)
	if err != nil || exists {
		return exists, err
	}
	if s.Settings != nil && s.Settings.IsRegistrationEmailNormalizationEnabled(ctx) {
		normalizedEmail := NormalizeRegistrationEmailAddress(email)
		if normalizedEmail != "" {
			return s.Users.ExistsByNormalizedEmail(ctx, normalizedEmail)
		}
	}
	return false, nil
}

// AuthBuildEmailSuffixNotAllowedError returns the shared suffix rejection for
// an empty list or a detailed new error otherwise. The detailed message and
// metadata retain supplied suffix order/text and count; callers normalize
// policy entries before constructing it.
func AuthBuildEmailSuffixNotAllowedError(whitelist []string) error {
	if len(whitelist) == 0 {
		return ErrEmailSuffixNotAllowed
	}

	allowed := strings.Join(whitelist, ", ")
	return infraerrors.BadRequest(
		"EMAIL_SUFFIX_NOT_ALLOWED",
		fmt.Sprintf("email suffix is not allowed, allowed suffixes: %s", allowed),
	).WithMetadata(map[string]string{
		"allowed_suffixes":     strings.Join(whitelist, ","),
		"allowed_suffix_count": strconv.Itoa(len(whitelist)),
	})
}

// IsPasswordResetEnabled reports whether both mailbox verification and
// password reset are currently enabled. Missing settings or disabled email
// verification returns false; public reset entry points use this combined
// policy before preparing any credential mail.
func (s *AuthService) IsPasswordResetEnabled(ctx context.Context) bool {
	if s.Settings == nil {
		return false
	}

	if !s.Settings.IsEmailVerifyEnabled(ctx) {
		return false
	}
	return s.Settings.IsPasswordResetEnabled(ctx)
}

// AuthPreparePasswordReset returns site/link text and whether an active
// account can receive reset mail. Missing accounts, lookup failures and
// inactive accounts are diagnosed and return false, allowing the caller's
// silent response while no mail is queued.
//
// Readable active accounts use current site settings or the built-in brand
// when settings are absent. The link appends reset-password after removing
// one trailing slash from the caller's frontend base. Preparation performs no
// token generation or send, and its false result does not describe global
// feature/queue readiness checked separately by RequestPasswordResetAsync.
func (s *AuthService) AuthPreparePasswordReset(ctx context.Context, email, frontendBaseURL string) (string, string, bool) {
	user, err := s.Users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// An absent account cannot receive reset mail; return the silent
			// preparation result while recording the observation, keeping mailbox
			// enumeration out of the public response.
			s.Observer.Printf("service.auth", "[Auth] Password reset requested for non-existent email: %s", email)
			return "", "", false
		}
		// Unavailable account data also yields silent preparation failure; no
		// credential mail is queued, while diagnostics retain the read error for
		// operators.
		s.Observer.Printf("service.auth", "[Auth] Database error checking email for password reset: %v", err)
		return "", "", false
	}

	if !user.IsActive() {
		s.Observer.Printf("service.auth", "[Auth] Password reset requested for inactive user: %s", email)
		return "", "", false
	}

	siteName := "TokenRouter"
	if s.Settings != nil {
		siteName = s.Settings.GetSiteName(ctx)
	}

	resetURL := fmt.Sprintf("%s/reset-password", strings.TrimSuffix(frontendBaseURL, "/"))

	return siteName, resetURL, true
}

// RequestPasswordResetAsync queues reset mail for a prepared active account
// under current reset policy. Disabled policy and missing queue return errors
// before preparation; otherwise missing/inactive account, lookup failure and
// enqueue failure return success to limit mailbox enumeration.
//
// The worker later prepares or reuses the reset credential and delivers its
// message. A diagnosed enqueue failure leaves worker admission unconfirmed
// despite the silent result. Callers distinguish policy/service admission
// errors from this enumeration response and do not interpret nil as proof
// that reset mail reached the mailbox.
func (s *AuthService) RequestPasswordResetAsync(ctx context.Context, email, frontendBaseURL string, locale ...string) error {
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}
	if s.EmailQueue == nil {
		return ErrServiceUnavailable
	}

	siteName, resetURL, shouldProceed := s.AuthPreparePasswordReset(ctx, email, frontendBaseURL)
	if !shouldProceed {
		return nil // Silent success to prevent enumeration
	}

	// Queue failure prevents confirmed worker admission; diagnose it but return
	// the same public success as an unusable account to limit mailbox
	// enumeration.
	if err := s.EmailQueue.EnqueuePasswordReset(email, siteName, resetURL, FirstEmailLocale(locale)); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to enqueue password reset email for %s: %v", email, err)
		return nil // Silent success to prevent enumeration
	}

	s.Observer.Printf("service.auth", "[Auth] Password reset email enqueued for: %s", email)
	return nil
}

// ResetPassword verifies the reset credential and attempts its deletion
// before loading an active account and saving the replacement hash. The email
// service can return success after deletion failure, and a later
// account/read/hash/write rejection can follow already removed credential
// state.
//
// The working password hash and transient TokenVersion are changed before the
// masked write. Password persistence changes the credential fingerprint
// derived by ResolvedTokenVersion; TokenVersion itself has no account column.
// Refresh-session revocation is attempted afterward and failure is diagnosed
// while reset succeeds. Callers retain any earlier failure and do not treat
// credential deletion, password persistence and refresh cleanup as one atomic
// transaction.
func (s *AuthService) ResetPassword(ctx context.Context, email, token, newPassword string) error {
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}

	if s.Email == nil {
		return ErrServiceUnavailable
	}

	// Verify and attempt reset-credential deletion before account lookup or
	// password persistence; later account/hash/write failure can follow a
	// consumed challenge with no rollback here.
	if err := s.Email.ConsumePasswordResetToken(ctx, email, token); err != nil {
		return err
	}

	user, err := s.Users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrInvalidResetToken // Token was valid but user was deleted
		}
		s.Observer.Printf("service.auth", "[Auth] Database error getting user for password reset: %v", err)
		return ErrServiceUnavailable
	}

	if !user.IsActive() {
		return ErrUserNotActive
	}

	hashedPassword, err := s.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	user.PasswordHash = hashedPassword
	user.TokenVersion++

	// Persist the selected password hash to change the credential fingerprint
	// used by token validation; the incremented in-memory TokenVersion is not an
	// account database column.
	if err := s.Users.Update(ctx, user, UserUpdateFields{PasswordHash: true}); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Database error updating password for user %d: %v", user.ID, err)
		return ErrServiceUnavailable
	}

	// Password persistence already succeeded before refresh cleanup; diagnose
	// revocation failure while retaining reset success and the changed
	// fingerprint checked by existing access tokens.
	if err := s.RevokeAllUserSessions(ctx, user.ID); err != nil {
		s.Observer.Printf("service.auth", "[Auth] Failed to revoke refresh tokens for user %d: %v", user.ID, err)
	}

	s.Observer.Printf("service.auth", "[Auth] Password reset successful for user: %s", email)
	return nil
}
