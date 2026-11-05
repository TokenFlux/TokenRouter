package identityhttp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/ent/enttest"
	"github.com/TokenFlux/TokenRouter/ent/redeemcode"
	"github.com/TokenFlux/TokenRouter/ent/redeemcodeusage"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity/provider"
	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/notification/smtp"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// OAuth completion combines persisted browser state with configurable account,
// invitation and provider decisions. Pending fixtures create grant and avatar
// tables before Ent schema setup, then assemble real handlers around SQLite
// users and in-memory settings, email, TOTP, refresh and referral substitutes.
// Each named shared-memory DSN can share tables across overlapping fixture
// lifetimes, so the cases use sequential construction.
//
// Handler rebinding selects runtime settings before static provider
// configuration and connects pending completion to session cleanup and profile
// callbacks. Background work is registered with lifecycle.Tasks; testing
// cleanup bounds its stop wait, while callbacks retain their own contexts.
// Callers can close a client before those cleanup callbacks run. Cache and
// referral substitutes expose their pointer sharing, ignored lifetimes and
// unsupported operations at their types and methods so assertions can be read
// within those limits.

var (
	// pendingOAuthCreateAccountPreCommitHook injects a precommit failure into
	// pending account creation. It is package-global; the injecting case resets it
	// during cleanup.
	pendingOAuthCreateAccountPreCommitHook func(context.Context, *dbent.PendingAuthSession) error

	// WeChat token and userinfo endpoints default to provider URLs. Sequential
	// local-server cases replace these package-global values and restore them at
	// cleanup.
	wechatOAuthAccessTokenURL = provider.DefaultWeChatTokenURL
	wechatOAuthUserInfoURL    = provider.DefaultWeChatUserInfoURL
)

// oauthPendingFlowTestHandlerOptions selects settings and optional
// collaborators for pending HTTP cases.
type oauthPendingFlowTestHandlerOptions struct {
	invitationEnabled  bool // base invitation setting
	emailVerifyEnabled bool // base email-verification setting
	// emailCache is retained when supplied and enables EmailChallenges
	// construction. Nil leaves email verification services absent even when
	// emailVerifyEnabled is true.
	emailCache identitycore.EmailCache // supplied email challenge cache
	// settingValues override the base registration, invitation and email settings.
	// Entries are copied into the fixture's new map before settings readers are
	// built.
	settingValues map[string]string // runtime setting overrides
	// defaultSubAssigner is passed into authentication dependencies as supplied.
	// Tests inspect captured assignment inputs when they provide the recording
	// fixture.
	defaultSubAssigner identitycore.DefaultSubscriptionAssigner
	// affiliateRepo is retained when supplied; nil selects a fresh in-memory
	// affiliate fixture used by the authentication service's registration effects.
	affiliateRepo promotion.AffiliateRepository // supplied referral repository
	// totpCache and totpEncryptor jointly select TOTP service construction. If
	// either is supplied, the missing partner receives the map-cache or plaintext
	// encryptor fixture; omitting both leaves the service absent.
	totpCache       identitycore.TotpCache          // TOTP session cache
	totpEncryptor   identitycore.SecretEncryptor    // TOTP secret converter
	userRepoOptions oauthPendingFlowUserRepoOptions // user deletion controls
}

// oauthPendingFlowSettingRepoStub reads supplied settings and accepts writes
// without retaining their values.
type oauthPendingFlowSettingRepoStub struct {
	// values is borrowed by scalar reads. Bulk reads copy entries into fresh maps,
	// and the fixture has no synchronization for concurrent map mutation.
	values map[string]string // supplied runtime setting values
}

// oauthPendingFlowRefreshTokenCacheStub accepts cache writes while retaining no
// refresh credentials.
type oauthPendingFlowRefreshTokenCacheStub struct{}

// oauthPendingFlowEmailCacheStub retains verification pointers and leaves other
// email-cache operations empty.
type oauthPendingFlowEmailCacheStub struct {
	// verificationCodes stores borrowed records by email without enforcing TTL or
	// locking. EmailChallenges.VerifyCode mutates Attempts on mismatches and
	// checks expiry in that path; a matching code can succeed for an expired
	// retained record because the fixture has not removed it.
	verificationCodes map[string]*identitycore.VerificationCodeData
}

// oauthPendingFlowRedeemCodeRepo provides the SQLite invitation operations used
// by authentication fixtures.
type oauthPendingFlowRedeemCodeRepo struct {
	// client is borrowed for Ent builders and remains on this fixture for every
	// operation. Its builders continue using this client when context carries an
	// Ent transaction.
	client *dbent.Client // retained invitation database client
}

// oauthPendingFlowUserRepo maps SQLite user rows into the authentication
// service's test inputs.
type oauthPendingFlowUserRepo struct {
	// client is retained for ordinary Ent builders. Avatar SQL methods separately
	// select an Ent transaction from context when one is available.
	client  *dbent.Client                   // retained user database client
	options oauthPendingFlowUserRepoOptions // configured user deletion guard
}

// oauthPendingFlowUserRepoOptions controls the injected user-deletion condition
// for rollback assertions.
type oauthPendingFlowUserRepoOptions struct {
	// rejectDeleteWhileAuthIdentityExists makes Delete count the user's identities
	// before its user write, exposing cleanup order when a binding still exists.
	rejectDeleteWhileAuthIdentityExists bool // identity deletion guard
}

// oauthPendingFlowDefaultSubAssignerStub records assignment inputs while
// returning an empty subscription result.
type oauthPendingFlowDefaultSubAssignerStub struct {
	// calls keeps shallow copies of nonnil inputs in invocation order. Quota and
	// source-order pointers remain shared with inputs, and append observations are
	// unsynchronized.
	calls []billing.AssignSubscriptionInput // captured subscription inputs
}

// oauthPendingFlowAffiliateRepo records referral ownership and invite counts in
// unsynchronized maps.
type oauthPendingFlowAffiliateRepo struct {
	// profiles and byCode share each profile's current code-to-user relationship.
	// Profile reads return shallow copies; setCode removes the old lookup entry
	// before registering its replacement. Callers use this fixture sequentially.
	profiles map[int64]*promotion.AffiliateSummary // stored profiles by user
	byCode   map[string]int64                      // code owner lookup
}

// oauthPendingFlowTotpCacheStub records TOTP sessions and attempts without TTL
// enforcement or locking.
type oauthPendingFlowTotpCacheStub struct {
	// setupSessions and loginSessions retain borrowed session pointers under their
	// respective user and temporary-token keys. Set methods allocate missing maps
	// and ignore TTL, so callers control session mutation and fixture lifetime.
	//
	// setup sessions by user
	setupSessions map[int64]*identitycore.TotpSetupSession
	// login sessions by temporary token
	loginSessions map[string]*identitycore.TotpLoginSession
	// verifyAttempts retains each user's synchronous attempt count until clear.
	// The fixture applies no expiration window and supplies no concurrent-access
	// protection.
	verifyAttempts map[int64]int // verification attempts by user
}

// oauthPendingFlowTotpEncryptorStub passes secret text through unchanged for
// local TOTP fixtures.
type oauthPendingFlowTotpEncryptorStub struct{}

// wechatOAuthSettingRepoStub reads supplied WeChat settings and accepts writes
// without retaining values.
type wechatOAuthSettingRepoStub struct {
	// values is borrowed for scalar reads and copied for bulk results. The fixture
	// expects sequential access because it has no map synchronization.
	values map[string]string // supplied WeChat setting values
}

// wechatOAuthRefreshTokenCacheStub accepts session-cache operations while
// retaining no refresh credentials.
type wechatOAuthRefreshTokenCacheStub struct{}

// authHTTPFixture holds borrowed dependencies and the assembled authentication
// and payment handlers.
type authHTTPFixture struct {
	// AuthenticationHandler is replaced by bindAuthHTTPFixture after construction
	// or dependency changes. Its promoted methods dispatch to the assembled
	// authentication handlers.
	*identityhttp.AuthenticationHandler
	// WeChatPaymentHandler is rebuilt with the authentication handlers and calls
	// paymentResume at request time, so later signing-config changes are observed.
	*paymenthttp.WeChatPaymentHandler // assembled WeChat payment handlers
	// cfg, authDB, authService, userService, settingSvc, promoService,
	// redeemService, totpService and userAttributeService are borrowed inputs for
	// handler reconstruction. Rebind after changing inputs retained by
	// constructors; the Google verifier and payment signing inputs are resolved at
	// request time.
	//
	// cfg supplies static provider values and the bind-secret input. Static
	// loaders need it when settingSvc is nil; a nil cfg leaves the binding secret
	// empty.
	cfg *config.Config // static HTTP configuration defaults
	// authDB supplies pending storage only when authService is present. A fixture
	// with no authentication service builds pending collaborators with a nil
	// client even when authDB was supplied.
	authDB      *dbent.Client             // retained authentication database client
	authService *identitycore.AuthService // authentication service instance
	userService *identitycore.UserService // user profile service instance
	// settingSvc selects runtime provider configuration when present. Nil selects
	// the static LinuxDo, OIDC and DingTalk loaders and leaves the WeChat
	// LoadConfig callback unset during assembly.
	settingSvc    *authSettingsFixture      // runtime setting readers
	promoService  *promotion.PromoService   // promotion preview service
	redeemService *billing.RedeemService    // invitation redemption service
	totpService   *identitycore.TotpService // TOTP authentication service
	// DingTalk attribute synchronization
	userAttributeService *identitycore.UserAttributeService
	// googleIDTokenVerifier is read through googleVerifierFixture on each call. A
	// nil interface selects GoogleAPIIDTokenVerifier; tests can install an
	// override after construction and before request dispatch.
	googleIDTokenVerifier provider.GoogleIDTokenVerifier
}

// googleVerifierFixture resolves the fixture's current Google verifier at each
// verification call.
type googleVerifierFixture struct {
	// h is borrowed and required. Verify reads its current verifier field so tests
	// can install an injected implementation after HTTP handler construction.
	h *authHTTPFixture // retained authentication HTTP fixture
}

// authSettingsFixture shares one supplied settings store across authentication
// and public-settings readers.
type authSettingsFixture struct {
	// RuntimeSettings, GrantSettings, DisplaySettings, oauth, promotion, backend,
	// public and composite share the store built by newAuthSettingsFixture.
	// Callers construct these readers together before using the fixture; the zero
	// value leaves its reader pointers absent.
	//
	// runtime authentication settings reader
	*identitycore.RuntimeSettings
	// source-default grant settings reader
	*identitycore.GrantSettings
	// site display settings reader
	*site.DisplaySettings
	// OAuth provider settings reader
	oauth *identitycore.OAuthSettings
	// referral and invitation settings reader
	promotion *promotion.RuntimeSettings
	// backend mode settings reader
	backend *admission.BackendMode
	// public HTTP settings reader
	public *site.PublicService
	// aggregate administrative settings reader
	composite *composite.Runtime
}

// newOAuthPendingFlowTestHandler builds pending HTTP dependencies with the
// requested invitation setting and email verification disabled. It returns the
// handler and shared SQLite client; t owns the registered pool and task-stop
// cleanups.
func newOAuthPendingFlowTestHandler(t *testing.T, invitationEnabled bool) (*authHTTPFixture, *dbent.Client) {
	t.Helper()

	return newOAuthPendingFlowTestHandlerWithOptions(t, invitationEnabled, false, nil)
}

// newOAuthPendingFlowTestHandlerWithEmailVerification seeds one email/code
// record in the borrowed-pointer cache and enables email verification. The
// cache ignores TTL; the record's future ExpiresAt supplies remaining lifetime
// when VerifyCode updates a mismatched attempt.
func newOAuthPendingFlowTestHandlerWithEmailVerification(
	t *testing.T,
	invitationEnabled bool,
	email string,
	code string,
) (*authHTTPFixture, *dbent.Client) {
	t.Helper()

	cache := &oauthPendingFlowEmailCacheStub{
		verificationCodes: map[string]*identitycore.VerificationCodeData{
			email: {
				Code:      code,
				Attempts:  0,
				CreatedAt: time.Now().UTC(),
				// Give mismatched attempts a future expiry for their remaining-lifetime
				// update.
				ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
			},
		},
	}
	return newOAuthPendingFlowTestHandlerWithOptions(t, invitationEnabled, true, cache)
}

// newOAuthPendingFlowTestHandlerWithOptions forwards invitation,
// email-verification and cache choices to the full fixture constructor. A nil
// cache leaves EmailChallenges absent, and pool/task cleanup follows the
// returned fixture's constructor rules.
func newOAuthPendingFlowTestHandlerWithOptions(
	t *testing.T,
	invitationEnabled bool,
	emailVerifyEnabled bool,
	emailCache identitycore.EmailCache,
) (*authHTTPFixture, *dbent.Client) {
	return newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		invitationEnabled:  invitationEnabled,
		emailVerifyEnabled: emailVerifyEnabled,
		emailCache:         emailCache,
	})
}

// newOAuthPendingFlowTestHandlerWithDependencies builds the shared SQLite
// pending-flow fixture and assembles handlers around supplied options. It
// registers pool closure on t and creates the raw grant/avatar tables before
// Ent schema setup, so first-bind and profile SQL have their required tables.
//
// Supplied settings override base values, and optional email, referral,
// subscription and TOTP dependencies are retained as described by
// oauthPendingFlowTestHandlerOptions. Later handler construction registers
// task-stop cleanup after the pool callback, but caller-registered or deferred
// client closes can run earlier. Overlapping lifetimes using the same DSN can
// share tables; these cases construct fixtures sequentially.
func newOAuthPendingFlowTestHandlerWithDependencies(
	t *testing.T,
	options oauthPendingFlowTestHandlerOptions,
) (*authHTTPFixture, *dbent.Client) {
	t.Helper()

	db, err := sql.Open("sqlite", "file:auth_oauth_pending_flow_handler?mode=memory&cache=shared")
	require.NoError(t, err)
	// Register pool closure before later task-stop callbacks so LIFO cleanup
	// reaches their waits first. Any explicit or later caller client close can
	// still run earlier; pool close errors are discarded.
	t.Cleanup(func() { _ = db.Close() })

	// Issue SQLite foreign-key setup before schema creation. This call configures
	// the connection used by Exec and does not establish the setting on every
	// connection the pool could open.
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	// Create grant and avatar tables before Ent setup: first-bind and profile
	// assertions query these raw-SQL tables, which the fixture adds alongside the
	// generated schema.
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS user_provider_default_grants (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	provider_type TEXT NOT NULL,
	grant_reason TEXT NOT NULL DEFAULT 'first_bind',
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(user_id, provider_type, grant_reason)
)`)
	require.NoError(t, err)
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS user_avatars (
	user_id INTEGER PRIMARY KEY,
	storage_provider TEXT NOT NULL,
	storage_key TEXT NOT NULL DEFAULT '',
	url TEXT NOT NULL,
	content_type TEXT NOT NULL DEFAULT '',
	byte_size INTEGER NOT NULL DEFAULT 0,
	sha256 TEXT NOT NULL DEFAULT '',
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`)
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))

	cfg := &config.Config{
		Auth: config.AuthConfig{JWT: config.JWTConfig{
			Secret:                "test-secret",
			AccessTokenTTLMinutes: 60,
			RefreshTokenTTLDays:   7,
		}},
		UserDefaults: config.UserDefaultsConfig{
			Balance:     0,
			Concurrency: 1,
		},
	}
	settingValues := map[string]string{
		identitycore.SettingKeyRegistrationEnabled: "true",
		promotion.SettingKeyInvitationCodeEnabled:  boolSettingValue(options.invitationEnabled),
		identitycore.SettingKeyEmailVerifyEnabled:  boolSettingValue(options.emailVerifyEnabled),
	}
	maps.Copy(settingValues, options.settingValues)
	settingSvc := newAuthSettingsFixture(&oauthPendingFlowSettingRepoStub{values: settingValues}, cfg)
	userRepo := &oauthPendingFlowUserRepo{
		client:  client,
		options: options.userRepoOptions,
	}
	redeemRepo := &oauthPendingFlowRedeemCodeRepo{client: client}
	var emailService *identitycore.EmailChallenges
	if options.emailCache != nil {
		emailService = identitycore.NewEmailChallenges(options.emailCache, notification.NewMailer(&oauthPendingFlowSettingRepoStub{
			values: map[string]string{
				identitycore.SettingKeyEmailVerifyEnabled: boolSettingValue(options.emailVerifyEnabled),
			},
		}, smtp.New()))
	}
	affiliateRepo := options.affiliateRepo
	if affiliateRepo == nil {
		affiliateRepo = newOAuthPendingFlowAffiliateRepo()
	}
	affiliateSvc := promotion.NewAffiliateService(affiliateRepo, settingSvc.promotion, nil, nil, promotion.Runtime{})
	authSvc := identitytestkit.Auth(
		client, &identitycore.AuthDependencies{Users: userRepo, Redeem: redeemRepo, RefreshTokens: &oauthPendingFlowRefreshTokenCacheStub{}, Options: identitytestkit.AuthOptions(cfg), Settings: authContractSettings(settingSvc), Email: identitytestkit.Email(emailService), DefaultSubscriptions: options.defaultSubAssigner, Affiliate: identitytestkit.Affiliate(affiliateSvc)},
	)
	userSvc := identitycore.NewUserService(userRepo, nil, nil, nil, authBackgroundFixture(t))
	var totpSvc *identitycore.TotpService
	// Supplying either TOTP collaborator selects the service path. Complete its
	// cache/encryptor pair with fixture defaults so those login handlers can
	// invoke both interfaces.
	if options.totpCache != nil || options.totpEncryptor != nil {
		totpCache := options.totpCache
		if totpCache == nil {
			totpCache = &oauthPendingFlowTotpCacheStub{}
		}
		totpEncryptor := options.totpEncryptor
		if totpEncryptor == nil {
			totpEncryptor = oauthPendingFlowTotpEncryptorStub{}
		}
		totpSvc = identitycore.NewTotpService(userRepo, totpEncryptor, totpCache, settingSvc, nil, nil)
	}

	return newAuthHTTPFixture(t, &authHTTPFixture{
		authDB:      client,
		authService: authSvc,
		userService: userSvc,
		settingSvc:  settingSvc,
		totpService: totpSvc,
	}), client
}

// boolSettingValue returns the lowercase settings text for the supplied
// boolean.
func boolSettingValue(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// boolPtr returns a pointer to a distinct supplied boolean value.
func boolPtr(v bool) *bool {
	return &v
}

// Get returns ErrSettingNotFound and no Setting record for every supplied key.
// Use GetValue or bulk repository reads for the values installed by fixture
// setup; this method ignores context and its lookup input.
func (s *oauthPendingFlowSettingRepoStub) Get(context.Context, string) (*settings.Setting, error) {
	return nil, settings.ErrSettingNotFound
}

// GetValue returns the configured string or ErrSettingNotFound when key is
// absent. Context is ignored, and a present empty string counts as a successful
// read from the supplied values map.
func (s *oauthPendingFlowSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return value, nil
}

// Set reports success while discarding the supplied key and value. GetValue and
// bulk repository reads continue to use the setup values map, so this call does
// not update the fixture's stored observations.
func (s *oauthPendingFlowSettingRepoStub) Set(context.Context, string, string) error {
	return nil
}

// GetMultiple copies requested keys that exist in the supplied values map.
// Missing keys are omitted, and callers can mutate the result independently of
// the fixture's string-valued map; context is ignored.
func (s *oauthPendingFlowSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

// SetMultiple reports success while discarding the supplied entry map.
// Subsequent GetValue and bulk repository reads continue to use setup values,
// and this fixture retains none of the batch's supplied string entries.
func (s *oauthPendingFlowSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}

// GetAll returns a fresh map containing every configured string entry. Callers
// can mutate that result independently of values; the fixture ignores context
// and provides no injected repository read error.
func (s *oauthPendingFlowSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	result := make(map[string]string, len(s.values))
	maps.Copy(result, s.values)
	return result, nil
}

// Delete reports success for every key while retaining the supplied values map.
// Later GetValue and bulk repository reads therefore continue to observe the
// entry installed by setup, if that key exists.
func (s *oauthPendingFlowSettingRepoStub) Delete(context.Context, string) error {
	return nil
}

// GetVerificationCode returns the retained verification record for email, or
// nil for a missing entry, map or receiver. It ignores context and expiry;
// callers share the record that VerifyCode mutates when counting attempts.
func (s *oauthPendingFlowEmailCacheStub) GetVerificationCode(_ context.Context, email string) (*identitycore.VerificationCodeData, error) {
	if s == nil || s.verificationCodes == nil {
		return nil, nil
	}
	return s.verificationCodes[email], nil
}

// SetVerificationCode retains data under email, allocating the map when needed,
// and returns success. It ignores the TTL and context inputs; callers keep the
// supplied record stable except for the mutations intentionally observed by
// verification tests.
func (s *oauthPendingFlowEmailCacheStub) SetVerificationCode(_ context.Context, email string, data *identitycore.VerificationCodeData, _ time.Duration) error {
	if s.verificationCodes == nil {
		s.verificationCodes = map[string]*identitycore.VerificationCodeData{}
	}
	s.verificationCodes[email] = data
	return nil
}

// DeleteVerificationCode removes email from the verification map and returns
// success even when no entry exists. It requires a nonnil receiver; callers use
// this unsynchronized fixture sequentially with verification reads and writes.
func (s *oauthPendingFlowEmailCacheStub) DeleteVerificationCode(_ context.Context, email string) error {
	// Remove the retained signup challenge so the next read observes absence after
	// successful verification; the fixture supplies no TTL-driven removal.
	delete(s.verificationCodes, email)
	return nil
}

// GetNotifyVerifyCode reports no notification-email challenge for every
// supplied address and returns nil error. Notification challenge writes retain
// no records in this fixture, so only the separate verification-code map
// supplies challenge data.
func (s *oauthPendingFlowEmailCacheStub) GetNotifyVerifyCode(context.Context, string) (*identitycore.VerificationCodeData, error) {
	return nil, nil
}

// SetNotifyVerifyCode reports success while discarding its address, record and
// lifetime inputs. Subsequent notification-challenge reads remain empty; this
// fixture's retained verificationCodes map belongs to the ordinary signup
// verification methods.
func (s *oauthPendingFlowEmailCacheStub) SetNotifyVerifyCode(context.Context, string, *identitycore.VerificationCodeData, time.Duration) error {
	return nil
}

// DeleteNotifyVerifyCode reports success for every supplied address while
// notification challenge state stays empty. Context is ignored, and the
// operation leaves ordinary signup records in verificationCodes available to
// their separate verification methods.
func (s *oauthPendingFlowEmailCacheStub) DeleteNotifyVerifyCode(context.Context, string) error {
	return nil
}

// GetPasswordResetToken returns no reset credential and nil error for every
// supplied address. Reset writes retain no records in this fixture;
// authentication scenarios use its separate signup verification-code methods.
func (s *oauthPendingFlowEmailCacheStub) GetPasswordResetToken(context.Context, string) (*identitycore.PasswordResetTokenData, error) {
	return nil, nil
}

// SetPasswordResetToken returns success while discarding the reset record,
// email and lifetime inputs. Later reset reads remain empty, and the ordinary
// verification-code map is retained for its separate signup operations.
func (s *oauthPendingFlowEmailCacheStub) SetPasswordResetToken(context.Context, string, *identitycore.PasswordResetTokenData, time.Duration) error {
	return nil
}

// DeletePasswordResetToken returns success for every email while reset
// credential state stays empty. It ignores context and leaves the ordinary
// verification-code map untouched for the separate signup-verification methods.
func (s *oauthPendingFlowEmailCacheStub) DeletePasswordResetToken(context.Context, string) error {
	return nil
}

// IsPasswordResetEmailInCooldown returns false for every email because this
// fixture retains no cooldown state. Calling SetPasswordResetEmailCooldown does
// not change that result, so these cases provide no cooldown-window enforcement
// evidence.
func (s *oauthPendingFlowEmailCacheStub) IsPasswordResetEmailInCooldown(context.Context, string) bool {
	return false
}

// SetPasswordResetEmailCooldown returns success while discarding email and
// duration inputs. IsPasswordResetEmailInCooldown remains false afterward; this
// fixture applies no expiration window or retained cooldown record to
// password-reset operations.
func (s *oauthPendingFlowEmailCacheStub) SetPasswordResetEmailCooldown(context.Context, string, time.Duration) error {
	return nil
}

// IncrNotifyCodeUserRate returns zero and nil error for every supplied user and
// window. It records no increment, so a later rate read also observes zero
// regardless of earlier calls.
func (s *oauthPendingFlowEmailCacheStub) IncrNotifyCodeUserRate(context.Context, int64, time.Duration) (int64, error) {
	return 0, nil
}

// GetNotifyCodeUserRate returns zero and nil error for every supplied user ID.
// Notification-rate increments retain no counter in this fixture, and these
// reads ignore context rather than consulting an expiration window.
func (s *oauthPendingFlowEmailCacheStub) GetNotifyCodeUserRate(context.Context, int64) (int64, error) {
	return 0, nil
}

// StoreRefreshToken reports success while discarding the credential, record and
// lifetime inputs. Later GetRefreshToken calls still report the credential
// missing; the fixture permits token-response assertions but retains no
// server-side refresh session.
func (s *oauthPendingFlowRefreshTokenCacheStub) StoreRefreshToken(context.Context, string, *identitycore.RefreshTokenData, time.Duration) error {
	return nil
}

// GetRefreshToken returns ErrRefreshTokenNotFound and no record for every
// credential hash. Successful store calls retain no credentials in this
// fixture, so this read never recovers a session issued earlier in a test.
func (s *oauthPendingFlowRefreshTokenCacheStub) GetRefreshToken(context.Context, string) (*identitycore.RefreshTokenData, error) {
	return nil, identitycore.ErrRefreshTokenNotFound
}

// DeleteRefreshToken reports success for every credential hash and ignores
// context. The fixture retains no refresh records before or after deletion, so
// handler success does not provide an observation of stored credential removal.
func (s *oauthPendingFlowRefreshTokenCacheStub) DeleteRefreshToken(context.Context, string) error {
	return nil
}

// DeleteUserRefreshTokens reports success for every user ID and ignores
// context. User-token indexes are empty in this fixture; the call supplies no
// count or retained evidence of credentials removed for that user.
func (s *oauthPendingFlowRefreshTokenCacheStub) DeleteUserRefreshTokens(context.Context, int64) error {
	return nil
}

// DeleteTokenFamily reports success for every supplied family ID and ignores
// context. Family-token indexes remain empty in this fixture, so callers
// receive successful cleanup while no stored family membership is inspected or
// changed.
func (s *oauthPendingFlowRefreshTokenCacheStub) DeleteTokenFamily(context.Context, string) error {
	return nil
}

// AddToUserTokenSet reports success while discarding user, hash and lifetime
// inputs. The fixture keeps no user-token index, so subsequent
// GetUserTokenHashes results remain empty despite accepted registration calls.
func (s *oauthPendingFlowRefreshTokenCacheStub) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}

// AddToFamilyTokenSet reports success while discarding family, hash and
// lifetime inputs. It retains no membership state, so later family-hash reads
// and membership checks stay empty and false respectively.
func (s *oauthPendingFlowRefreshTokenCacheStub) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

// GetUserTokenHashes returns a nil slice with nil error for every user ID. User
// registration and credential stores retain no index in this fixture, so prior
// successful writes do not populate that result.
func (s *oauthPendingFlowRefreshTokenCacheStub) GetUserTokenHashes(context.Context, int64) ([]string, error) {
	return nil, nil
}

// GetFamilyTokenHashes returns a nil slice with nil error for every family ID.
// Family registration calls retain no hashes in this fixture, so earlier
// successful writes do not populate the returned credential list.
func (s *oauthPendingFlowRefreshTokenCacheStub) GetFamilyTokenHashes(context.Context, string) ([]string, error) {
	return nil, nil
}

// IsTokenInFamily reports false and nil error for every family and hash pair.
// Family registration retains no membership in this fixture, so the result
// stays false after successful AddToFamilyTokenSet calls.
func (s *oauthPendingFlowRefreshTokenCacheStub) IsTokenInFamily(context.Context, string, string) (bool, error) {
	return false, nil
}

// Create rejects unsupported invitation creation by panicking in this test
// fixture.
func (r *oauthPendingFlowRedeemCodeRepo) Create(context.Context, *billing.RedeemCode) error {
	panic("unexpected Create call")
}

// CreateBatch rejects unsupported batch invitation creation by panicking in
// this test fixture.
func (r *oauthPendingFlowRedeemCodeRepo) CreateBatch(context.Context, []billing.RedeemCode) error {
	panic("unexpected CreateBatch call")
}

// GetByID rejects unsupported ID-based invitation reads by panicking in this
// test fixture.
func (r *oauthPendingFlowRedeemCodeRepo) GetByID(context.Context, int64) (*billing.RedeemCode, error) {
	panic("unexpected GetByID call")
}

// GetByIDForUpdate rejects unsupported ID-based locking invitation reads by
// panicking in this test fixture.
func (r *oauthPendingFlowRedeemCodeRepo) GetByIDForUpdate(context.Context, int64) (*billing.RedeemCode, error) {
	panic("unexpected GetByIDForUpdate call")
}

// GetByCode maps the matching SQLite invitation into a new service value or
// returns ErrRedeemCodeNotFound. Notes defaults to empty when absent; optional
// expiry, usage and plan pointers are retained from the loaded Ent entity.
func (r *oauthPendingFlowRedeemCodeRepo) GetByCode(ctx context.Context, code string) (*billing.RedeemCode, error) {
	entity, err := r.client.RedeemCode.Query().Where(redeemcode.CodeEQ(code)).Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, billing.ErrRedeemCodeNotFound
		}
		return nil, err
	}
	notes := ""
	if entity.Notes != nil {
		notes = *entity.Notes
	}
	return &billing.RedeemCode{
		ID:        entity.ID,
		Code:      entity.Code,
		Type:      entity.Type,
		Value:     entity.Value,
		Status:    entity.Status,
		MaxUses:   entity.MaxUses,
		UsedCount: entity.UsedCount,
		ExpiresAt: entity.ExpiresAt,
		UsedBy:    entity.UsedBy,
		UsedAt:    entity.UsedAt,
		Notes:     notes,
		CreatedAt: entity.CreatedAt,
		PlanID:    entity.PlanID,
	}, nil
}

// GetByCodeForUpdate delegates to GetByCode and inherits its results and
// optional-pointer sharing. It adds no row-lock operation to the fixture's
// ordinary read, so callers serialize the tested invitation flow.
func (r *oauthPendingFlowRedeemCodeRepo) GetByCodeForUpdate(ctx context.Context, code string) (*billing.RedeemCode, error) {
	return r.GetByCode(ctx, code)
}

// Update writes the supplied invitation snapshot through the retained client
// and returns success for nil input. Nil optional expiry, used-by, used-at and
// plan fields clear their stored columns; scalar fields are written as
// supplied.
func (r *oauthPendingFlowRedeemCodeRepo) Update(ctx context.Context, code *billing.RedeemCode) error {
	if code == nil {
		return nil
	}
	update := r.client.RedeemCode.UpdateOneID(code.ID).
		SetCode(code.Code).
		SetType(code.Type).
		SetValue(code.Value).
		SetStatus(code.Status).
		SetMaxUses(code.MaxUses).
		SetUsedCount(code.UsedCount).
		SetNotes(code.Notes)
	if code.ExpiresAt != nil {
		update = update.SetExpiresAt(*code.ExpiresAt)
	} else {
		update = update.ClearExpiresAt()
	}
	if code.UsedBy != nil {
		update = update.SetUsedBy(*code.UsedBy)
	} else {
		update = update.ClearUsedBy()
	}
	if code.UsedAt != nil {
		update = update.SetUsedAt(*code.UsedAt)
	} else {
		update = update.ClearUsedAt()
	}
	if code.PlanID != nil {
		update = update.SetPlanID(*code.PlanID)
	} else {
		update = update.ClearPlanID()
	}
	_, err := update.Save(ctx)
	return err
}

// BatchUpdate rejects unsupported batch invitation updates by panicking in this
// test fixture.
func (r *oauthPendingFlowRedeemCodeRepo) BatchUpdate(context.Context, []int64, billing.RedeemCodeBatchUpdateFields) (int64, error) {
	panic("unexpected BatchUpdate call")
}

// Delete rejects unsupported invitation deletion by panicking in this test
// fixture.
func (r *oauthPendingFlowRedeemCodeRepo) Delete(context.Context, int64) error {
	panic("unexpected Delete call")
}

// Use marks an unused row used, writes used_count=1, and records the user and
// current UTC time. A missing ID or non-unused row returns ErrRedeemCodeUsed;
// this fixture update does not evaluate MaxUses or expiry.
func (r *oauthPendingFlowRedeemCodeRepo) Use(ctx context.Context, id, userID int64) error {
	// Match ID and unused status in the same write so a stale invitation read
	// cannot overwrite a row already marked used by an earlier request.
	affected, err := r.client.RedeemCode.Update().
		Where(redeemcode.IDEQ(id), redeemcode.StatusEQ(billing.StatusUnused)).
		SetStatus(billing.StatusUsed).
		SetUsedCount(1).
		SetUsedBy(userID).
		SetUsedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return billing.ErrRedeemCodeUsed
	}
	return nil
}

// CreateUsage inserts the required usage pointer and publishes its generated ID
// after a successful write. A zero UsedAt is replaced before persistence, so
// that caller-visible timestamp remains changed even when the insert fails.
func (r *oauthPendingFlowRedeemCodeRepo) CreateUsage(ctx context.Context, usage *billing.RedeemCodeUsage) error {
	if usage.UsedAt.IsZero() {
		usage.UsedAt = time.Now().UTC()
	}
	entity, err := r.client.RedeemCodeUsage.Create().
		SetRedeemCodeID(usage.RedeemCodeID).
		SetUserID(usage.UserID).
		SetUsedAt(usage.UsedAt).
		Save(ctx)
	if err != nil {
		return err
	}
	// Publish the generated usage ID after insert success so the caller can
	// identify the row read by later invitation-usage assertions.
	usage.ID = entity.ID
	return nil
}

// GetUsageByRedeemCodeAndUser returns a fresh mapped usage value for the
// supplied invitation and user. An absent matching row returns nil with nil
// error; other database errors propagate from the retained client's query.
func (r *oauthPendingFlowRedeemCodeRepo) GetUsageByRedeemCodeAndUser(ctx context.Context, redeemCodeID, userID int64) (*billing.RedeemCodeUsage, error) {
	entity, err := r.client.RedeemCodeUsage.Query().
		Where(
			redeemcodeusage.RedeemCodeIDEQ(redeemCodeID),
			redeemcodeusage.UserIDEQ(userID),
		).
		Only(ctx)
	if err != nil {
		// A missing usage row is a normal optional result for this pair. Translate
		// Ent's not-found error to nil,nil so callers can distinguish absent usage
		// data from a failed query.
		if dbent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &billing.RedeemCodeUsage{
		ID:           entity.ID,
		RedeemCodeID: entity.RedeemCodeID,
		UserID:       entity.UserID,
		UsedAt:       entity.UsedAt,
	}, nil
}

// List rejects unsupported invitation pagination by panicking in this test
// fixture.
func (r *oauthPendingFlowRedeemCodeRepo) List(context.Context, pagination.PaginationParams) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

// ListWithFilters rejects unsupported filtered invitation pagination by
// panicking in this test fixture.
func (r *oauthPendingFlowRedeemCodeRepo) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

// ListByUser rejects unsupported user invitation listing by panicking in this
// test fixture.
func (r *oauthPendingFlowRedeemCodeRepo) ListByUser(context.Context, int64, int) ([]billing.RedeemCode, error) {
	panic("unexpected ListByUser call")
}

// ListByUserPaginated rejects unsupported paginated user invitation listing by
// panicking in this test fixture.
func (r *oauthPendingFlowRedeemCodeRepo) ListByUserPaginated(context.Context, int64, pagination.PaginationParams, string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserPaginated call")
}

// SumPositiveBalanceByUser rejects unsupported user redemption balance totals
// by panicking in this test fixture.
func (r *oauthPendingFlowRedeemCodeRepo) SumPositiveBalanceByUser(context.Context, int64) (float64, error) {
	panic("unexpected SumPositiveBalanceByUser call")
}

// decodeJSONResponseData decodes the response envelope and returns its data
// map, failing t on malformed JSON. Missing or null data returns nil; this
// helper does not assert the handler's HTTP or envelope status.
func decodeJSONResponseData(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var envelope struct {
		Data map[string]any `json:"data"` // decoded envelope data
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	return envelope.Data
}

// decodeJSONBody decodes the entire response into a string-keyed map, failing t
// on malformed JSON. JSON null returns nil; callers separately assert the
// expected HTTP status and response fields.
func decodeJSONBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload
}

// Create persists the listed user fields and writes ID plus creation/update
// times back after a successful save. The user pointer is required, and Ent
// builders use this fixture's retained client for the operation.
func (r *oauthPendingFlowUserRepo) Create(ctx context.Context, user *identitycore.User) error {
	entity, err := r.client.User.Create().
		SetEmail(user.Email).
		SetUsername(user.Username).
		SetNotes(user.Notes).
		SetPasswordHash(user.PasswordHash).
		SetRole(user.Role).
		SetBalance(user.Balance).
		SetConcurrency(user.Concurrency).
		SetStatus(user.Status).
		SetNillableTotpSecretEncrypted(user.TotpSecretEncrypted).
		SetTotpEnabled(user.TotpEnabled).
		SetNillableTotpEnabledAt(user.TotpEnabledAt).
		SetTotalRecharged(user.TotalRecharged).
		SetSignupSource(user.SignupSource).
		SetNillableLastLoginAt(user.LastLoginAt).
		SetNillableLastActiveAt(user.LastActiveAt).
		Save(ctx)
	if err != nil {
		return err
	}
	user.ID = entity.ID
	user.CreatedAt = entity.CreatedAt
	user.UpdatedAt = entity.UpdatedAt
	return nil
}

// CreateWithNormalizedEmailGuard delegates to Create while ignoring the
// supplied normalized email. It inherits Create's input mutations and errors;
// this fixture adds no registration-email lock or normalized uniqueness check
// around that write.
func (r *oauthPendingFlowUserRepo) CreateWithNormalizedEmailGuard(ctx context.Context, user *identitycore.User, _ string) error {
	return r.Create(ctx, user)
}

// GetByID loads a visible user row and maps the fixture's selected fields into
// a fresh service value. Missing rows become ErrUserNotFound; optional pointers
// follow oauthPendingFlowServiceUser's sharing rule.
func (r *oauthPendingFlowUserRepo) GetByID(ctx context.Context, id int64) (*identitycore.User, error) {
	entity, err := r.client.User.Get(ctx, id)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, identitycore.ErrUserNotFound
		}
		return nil, err
	}
	return oauthPendingFlowServiceUser(entity), nil
}

// GetByEmail queries the stored email exactly and returns the fixture's mapped
// user fields. Missing rows become ErrUserNotFound; normalization and
// compatibility matching belong to callers using the separate normalized-email
// lookup.
func (r *oauthPendingFlowUserRepo) GetByEmail(ctx context.Context, email string) (*identitycore.User, error) {
	entity, err := r.client.User.Query().Where(dbuser.EmailEQ(email)).Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, identitycore.ErrUserNotFound
		}
		return nil, err
	}
	return oauthPendingFlowServiceUser(entity), nil
}

// GetFirstAdmin rejects unsupported administrator lookup by panicking in this
// test fixture.
func (r *oauthPendingFlowUserRepo) GetFirstAdmin(context.Context) (*identitycore.User, error) {
	panic("unexpected GetFirstAdmin call")
}

// Update ignores fields and writes the listed scalar snapshot values plus
// supplied nonnil optional fields. Nil optional timestamps and TOTP-secret
// pointers leave their stored columns unchanged; a successful save updates
// user.UpdatedAt through the required input pointer.
func (r *oauthPendingFlowUserRepo) Update(ctx context.Context, user *identitycore.User, fields identitycore.UserUpdateFields) error {
	// Even a narrow fields mask reaches this snapshot write. Generated SetNillable
	// setters skip absent pointers, so those inputs do not clear saved optional
	// values.
	entity, err := r.client.User.UpdateOneID(user.ID).
		SetEmail(user.Email).
		SetUsername(user.Username).
		SetNotes(user.Notes).
		SetPasswordHash(user.PasswordHash).
		SetRole(user.Role).
		SetBalance(user.Balance).
		SetConcurrency(user.Concurrency).
		SetStatus(user.Status).
		SetNillableTotpSecretEncrypted(user.TotpSecretEncrypted).
		SetTotpEnabled(user.TotpEnabled).
		SetNillableTotpEnabledAt(user.TotpEnabledAt).
		SetTotalRecharged(user.TotalRecharged).
		SetSignupSource(user.SignupSource).
		SetNillableLastLoginAt(user.LastLoginAt).
		SetNillableLastActiveAt(user.LastActiveAt).
		Save(ctx)
	if err != nil {
		return err
	}
	user.UpdatedAt = entity.UpdatedAt
	return nil
}

// UpdateWithNormalizedEmailGuard delegates to Update and ignores the supplied
// normalized-email string. The fields selector remains ignored by that
// delegate, and this fixture adds no lock or normalized-email guard around its
// retained-client write.
func (r *oauthPendingFlowUserRepo) UpdateWithNormalizedEmailGuard(ctx context.Context, user *identitycore.User, _ string, fields identitycore.UserUpdateFields) error {
	return r.Update(ctx, user, fields)
}

// UpdateUserLastActiveAt writes the supplied activity time through the retained
// client. It returns the Ent write error, while timestamp selection and any
// background scheduling remain with the user-service caller.
func (r *oauthPendingFlowUserRepo) UpdateUserLastActiveAt(ctx context.Context, userID int64, activeAt time.Time) error {
	return r.client.User.UpdateOneID(userID).SetLastActiveAt(activeAt).Exec(ctx)
}

// Delete applies the configured identity-count guard before issuing the
// retained client's user deletion. When the guard is enabled, any remaining
// identity rejects deletion; the count and delete are separate operations, and
// query or write errors propagate.
func (r *oauthPendingFlowUserRepo) Delete(ctx context.Context, id int64) error {
	// When enabled, the guard rejects cleanup while a binding still names the
	// user. Count identities before issuing deletion to expose that ordering in
	// failure tests.
	if r.options.rejectDeleteWhileAuthIdentityExists {
		count, err := r.client.AuthIdentity.Query().Where(authidentity.UserIDEQ(id)).Count(ctx)
		if err != nil {
			return err
		}
		if count > 0 {
			return errors.New("cannot delete user while auth identities still exist")
		}
	}
	return r.client.User.DeleteOneID(id).Exec(ctx)
}

// GetUserAvatar reads the first avatar row or returns nil when that row is
// absent. It selects the contextual Ent transaction driver when present, checks
// query/scan/iteration errors, and discards the result-set close error after
// inspection.
func (r *oauthPendingFlowUserRepo) GetUserAvatar(ctx context.Context, userID int64) (*identitycore.UserAvatar, error) {
	// Avatar operations participate in the pending-flow transaction when context
	// supplies one. Select its driver here; the fixture's ordinary user builders
	// retain their original client.
	driver := r.client.Driver()
	if tx := dbent.TxFromContext(ctx); tx != nil {
		driver = tx.Client().Driver()
	}

	var rows entsql.Rows
	if err := driver.Query(
		ctx,
		`SELECT storage_provider, storage_key, url, content_type, byte_size, sha256 FROM user_avatars WHERE user_id = ?`,
		[]any{userID},
		&rows,
	); err != nil {
		return nil, err
	}
	// Release the avatar result set after inspection. Close errors are discarded
	// here while query, scan and observed iteration errors determine the returned
	// result.
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return nil, rows.Err()
	}

	var avatar identitycore.UserAvatar
	if err := rows.Scan(
		&avatar.StorageProvider,
		&avatar.StorageKey,
		&avatar.URL,
		&avatar.ContentType,
		&avatar.ByteSize,
		&avatar.SHA256,
	); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &avatar, nil
}

// UpsertUserAvatar writes the supplied avatar columns and current SQL timestamp
// for userID, then returns a new value containing those input fields. A
// contextual Ent transaction selects its driver; other calls use the retained
// client driver.
func (r *oauthPendingFlowUserRepo) UpsertUserAvatar(ctx context.Context, userID int64, input identitycore.UpsertUserAvatarInput) (*identitycore.UserAvatar, error) {
	// Avatar operations participate in the pending-flow transaction when context
	// supplies one. Select its driver here; the fixture's ordinary user builders
	// retain their original client.
	driver := r.client.Driver()
	if tx := dbent.TxFromContext(ctx); tx != nil {
		driver = tx.Client().Driver()
	}

	var result entsql.Result
	if err := driver.Exec(
		ctx,
		`INSERT INTO user_avatars (user_id, storage_provider, storage_key, url, content_type, byte_size, sha256, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(user_id) DO UPDATE SET
	storage_provider = excluded.storage_provider,
	storage_key = excluded.storage_key,
	url = excluded.url,
	content_type = excluded.content_type,
	byte_size = excluded.byte_size,
	sha256 = excluded.sha256,
	updated_at = CURRENT_TIMESTAMP`,
		[]any{
			userID,
			input.StorageProvider,
			input.StorageKey,
			input.URL,
			input.ContentType,
			input.ByteSize,
			input.SHA256,
		},
		&result,
	); err != nil {
		return nil, err
	}

	return &identitycore.UserAvatar{
		StorageProvider: input.StorageProvider,
		StorageKey:      input.StorageKey,
		URL:             input.URL,
		ContentType:     input.ContentType,
		ByteSize:        input.ByteSize,
		SHA256:          input.SHA256,
	}, nil
}

// DeleteUserAvatar deletes the selected user's avatar row and returns the
// driver's execution error. A contextual Ent transaction selects its driver; an
// absent matching row follows the underlying SQL delete's successful zero-row
// behavior.
func (r *oauthPendingFlowUserRepo) DeleteUserAvatar(ctx context.Context, userID int64) error {
	// Avatar operations participate in the pending-flow transaction when context
	// supplies one. Select its driver here; the fixture's ordinary user builders
	// retain their original client.
	driver := r.client.Driver()
	if tx := dbent.TxFromContext(ctx); tx != nil {
		driver = tx.Client().Driver()
	}

	var result entsql.Result
	return driver.Exec(ctx, `DELETE FROM user_avatars WHERE user_id = ?`, []any{userID}, &result)
}

// List rejects unsupported user pagination by panicking in this test fixture.
func (r *oauthPendingFlowUserRepo) List(context.Context, pagination.PaginationParams) ([]identitycore.User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

// ListWithFilters rejects unsupported filtered user pagination by panicking in
// this test fixture.
func (r *oauthPendingFlowUserRepo) ListWithFilters(context.Context, pagination.PaginationParams, identitycore.UserListFilters) ([]identitycore.User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

// UpdateBalance rejects unsupported balance-delta updates by panicking in this
// test fixture.
func (r *oauthPendingFlowUserRepo) UpdateBalance(context.Context, int64, float64) error {
	panic("unexpected UpdateBalance call")
}

// AddBalance adds amount to the selected user through the retained client and
// returns the write error. Negative amounts reduce the balance; this fixture
// adds no ledger or funding check around the Ent update.
func (r *oauthPendingFlowUserRepo) AddBalance(ctx context.Context, id int64, amount float64) error {
	return r.client.User.UpdateOneID(id).AddBalance(amount).Exec(ctx)
}

// DeductBalance subtracts amount and returns that same amount after a
// successful retained-client write. Zero returns immediately, and negative
// inputs add balance; this fixture applies no insufficient-funds check before
// the update.
func (r *oauthPendingFlowUserRepo) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	if amount == 0 {
		return 0, nil
	}
	if err := r.client.User.UpdateOneID(id).AddBalance(-amount).Exec(ctx); err != nil {
		return 0, err
	}
	return amount, nil
}

// AdjustBalance rejects unsupported reported balance adjustments by panicking
// in this test fixture.
func (r *oauthPendingFlowUserRepo) AdjustBalance(ctx context.Context, id int64, delta float64) (identitycore.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

// SetBalance rejects unsupported reported absolute balance writes by panicking
// in this test fixture.
func (r *oauthPendingFlowUserRepo) SetBalance(ctx context.Context, id int64, value float64) (identitycore.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

// UpdateConcurrency rejects unsupported single-user concurrency updates by
// panicking in this test fixture.
func (r *oauthPendingFlowUserRepo) UpdateConcurrency(context.Context, int64, int) error {
	panic("unexpected UpdateConcurrency call")
}

// BatchSetConcurrency rejects unsupported batch concurrency replacement by
// panicking in this test fixture.
func (r *oauthPendingFlowUserRepo) BatchSetConcurrency(context.Context, []int64, int) (int, error) {
	panic("unexpected BatchSetConcurrency call")
}

// BatchAddConcurrency rejects unsupported batch concurrency increments by
// panicking in this test fixture.
func (r *oauthPendingFlowUserRepo) BatchAddConcurrency(context.Context, []int64, int) (int, error) {
	panic("unexpected BatchAddConcurrency call")
}

// BatchUpdateLimits rejects unsupported batch user limit updates by panicking
// in this test fixture.
func (r *oauthPendingFlowUserRepo) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	panic("unexpected BatchUpdateLimits call")
}

// GetLatestUsedAtByUserIDs returns a fresh empty map and nil error for any ID
// list. This fixture has no activity-summary data, so callers observe no usage
// timestamps regardless of previously created users.
func (r *oauthPendingFlowUserRepo) GetLatestUsedAtByUserIDs(context.Context, []int64) (map[int64]*time.Time, error) {
	return map[int64]*time.Time{}, nil
}

// GetLatestUsedAtByUserID returns nil time with nil error for every user ID.
// The fixture keeps no activity-summary observation, so even an existing user
// is reported as having no latest usage timestamp.
func (r *oauthPendingFlowUserRepo) GetLatestUsedAtByUserID(context.Context, int64) (*time.Time, error) {
	return nil, nil
}

// ExistsByEmail counts visible rows matching the exact stored email and returns
// whether that count is positive. It returns the query error with the boolean;
// callers check the error before treating the count as an answer.
func (r *oauthPendingFlowUserRepo) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	count, err := r.client.User.Query().Where(dbuser.EmailEQ(email)).Count(ctx)
	return count > 0, err
}

// ExistsByNormalizedEmail scans visible user rows and compares their normalized
// emails to the supplied string. The argument is expected to be already
// normalized; the fixture returns at the first match and propagates the initial
// query error.
func (r *oauthPendingFlowUserRepo) ExistsByNormalizedEmail(ctx context.Context, normalizedEmail string) (bool, error) {
	users, err := r.client.User.Query().All(ctx)
	if err != nil {
		return false, err
	}
	for _, user := range users {
		if identitycore.NormalizeRegistrationEmailAddress(user.Email) == normalizedEmail {
			return true, nil
		}
	}
	return false, nil
}

// LockRegistrationEmail reports success while ignoring context and the supplied
// email. It acquires no registration lock in this fixture, so these SQLite
// scenarios do not establish the production registration-lock behavior.
func (r *oauthPendingFlowUserRepo) LockRegistrationEmail(context.Context, string) error {
	return nil
}

// RemoveGroupFromAllowedGroups rejects unsupported global allowed-group removal
// by panicking in this test fixture.
func (r *oauthPendingFlowUserRepo) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	panic("unexpected RemoveGroupFromAllowedGroups call")
}

// AddGroupToAllowedGroups rejects unsupported user allowed-group additions by
// panicking in this test fixture.
func (r *oauthPendingFlowUserRepo) AddGroupToAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected AddGroupToAllowedGroups call")
}

// RemoveGroupFromUserAllowedGroups rejects unsupported user allowed-group
// removal by panicking in this test fixture.
func (r *oauthPendingFlowUserRepo) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected RemoveGroupFromUserAllowedGroups call")
}

// ListUserAuthIdentities maps the selected user's loaded identities into a
// fresh record slice and skips nil entities. Metadata maps and optional
// identity pointers are retained from those entities; database read errors
// propagate.
func (r *oauthPendingFlowUserRepo) ListUserAuthIdentities(ctx context.Context, userID int64) ([]identitycore.UserAuthIdentityRecord, error) {
	identities, err := r.client.AuthIdentity.Query().
		Where(authidentity.UserIDEQ(userID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	records := make([]identitycore.UserAuthIdentityRecord, 0, len(identities))
	for _, identity := range identities {
		if identity == nil {
			continue
		}
		records = append(records, identitycore.UserAuthIdentityRecord{
			ProviderType:    identity.ProviderType,
			ProviderKey:     identity.ProviderKey,
			ProviderSubject: identity.ProviderSubject,
			VerifiedAt:      identity.VerifiedAt,
			Issuer:          identity.Issuer,
			Metadata:        identity.Metadata,
			CreatedAt:       identity.CreatedAt,
			UpdatedAt:       identity.UpdatedAt,
		})
	}
	return records, nil
}

// UnbindUserAuthProvider rejects unsupported authentication provider unbinding
// by panicking in this test fixture.
func (r *oauthPendingFlowUserRepo) UnbindUserAuthProvider(context.Context, int64, string) error {
	panic("unexpected UnbindUserAuthProvider call")
}

// UpdateTotpSecret writes the supplied encrypted-secret text, or clears the
// stored field when the pointer is nil. It returns the retained client's write
// error; the other TOTP fields remain governed by their separate update
// methods.
func (r *oauthPendingFlowUserRepo) UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error {
	update := r.client.User.UpdateOneID(userID)
	if encryptedSecret == nil {
		update = update.ClearTotpSecretEncrypted()
	} else {
		update = update.SetTotpSecretEncrypted(*encryptedSecret)
	}
	return update.Exec(ctx)
}

// EnableTotp sets the selected user's enabled flag and current UTC enablement
// time in one Ent update. It returns the retained-client execution error;
// callers prepare secret storage separately from this flag-and-time write.
func (r *oauthPendingFlowUserRepo) EnableTotp(ctx context.Context, userID int64) error {
	return r.client.User.UpdateOneID(userID).
		SetTotpEnabled(true).
		SetTotpEnabledAt(time.Now().UTC()).
		Exec(ctx)
}

// DisableTotp clears the selected user's enabled flag, secret and enablement
// timestamp in one Ent update. It returns the retained-client execution error;
// caller cache cleanup and challenge policy remain outside this fixture method.
func (r *oauthPendingFlowUserRepo) DisableTotp(ctx context.Context, userID int64) error {
	return r.client.User.UpdateOneID(userID).
		SetTotpEnabled(false).
		ClearTotpSecretEncrypted().
		ClearTotpEnabledAt().
		Exec(ctx)
}

// GetByIDIncludeDeleted delegates to the ordinary GetByID and inherits its
// mapping, error and visibility behavior. The fixture adds no separate bypass
// of user query filters merely because this method's name mentions deleted
// rows.
func (r *oauthPendingFlowUserRepo) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identitycore.User, error) {
	return r.GetByID(ctx, id)
}

// oauthPendingFlowServiceUser maps the fixture's selected user columns into a
// fresh service value and returns nil for a nil entity. Optional timestamps and
// encrypted-secret pointers are retained from the entity; callers treat those
// referenced values as shared.
func oauthPendingFlowServiceUser(entity *dbent.User) *identitycore.User {
	if entity == nil {
		return nil
	}
	return &identitycore.User{
		ID:                  entity.ID,
		Email:               entity.Email,
		Username:            entity.Username,
		Notes:               entity.Notes,
		PasswordHash:        entity.PasswordHash,
		Role:                entity.Role,
		Balance:             entity.Balance,
		Concurrency:         entity.Concurrency,
		Status:              entity.Status,
		SignupSource:        entity.SignupSource,
		LastLoginAt:         entity.LastLoginAt,
		LastActiveAt:        entity.LastActiveAt,
		TotpSecretEncrypted: entity.TotpSecretEncrypted,
		TotpEnabled:         entity.TotpEnabled,
		TotpEnabledAt:       entity.TotpEnabledAt,
		TotalRecharged:      entity.TotalRecharged,
		CreatedAt:           entity.CreatedAt,
		UpdatedAt:           entity.UpdatedAt,
	}
}

// newOAuthPendingFlowAffiliateRepo allocates independent empty profile and
// code-index maps for sequential referral assertions. Returned profile copies
// remain shallow, and the repository supplies no synchronization around its
// mutations.
func newOAuthPendingFlowAffiliateRepo() *oauthPendingFlowAffiliateRepo {
	return &oauthPendingFlowAffiliateRepo{
		profiles: make(map[int64]*promotion.AffiliateSummary),
		byCode:   make(map[string]int64),
	}
}

// EnsureUserAffiliate returns a shallow profile copy, creating its AFF-prefixed
// code and index entry when absent. Nonpositive user IDs return
// ErrUserNotFound; the receiver needs initialized maps, and callers serialize
// mutations of this fixture.
func (r *oauthPendingFlowAffiliateRepo) EnsureUserAffiliate(_ context.Context, userID int64) (*promotion.AffiliateSummary, error) {
	if userID <= 0 {
		return nil, identitycore.ErrUserNotFound
	}
	if profile := r.profiles[userID]; profile != nil {
		cloned := *profile
		return &cloned, nil
	}
	code := "AFF" + strconv.FormatInt(userID, 10)
	profile := &promotion.AffiliateSummary{
		UserID:    userID,
		AffCode:   code,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	// Register the profile and its generated code together before returning the
	// shallow copy, so the next referral lookup can find this newly ensured user.
	r.profiles[userID] = profile
	r.byCode[code] = userID
	cloned := *profile
	return &cloned, nil
}

// GetAffiliateByCode trims and uppercases code before looking up its indexed
// user and returning a shallow profile copy. Missing or nonpositive owners
// become ErrAffiliateProfileNotFound; the supplied context is ignored by this
// in-memory fixture.
func (r *oauthPendingFlowAffiliateRepo) GetAffiliateByCode(_ context.Context, code string) (*promotion.AffiliateSummary, error) {
	userID := r.byCode[strings.ToUpper(strings.TrimSpace(code))]
	if userID <= 0 {
		return nil, promotion.ErrAffiliateProfileNotFound
	}
	return r.EnsureUserAffiliate(context.Background(), userID)
}

// BindInviter ensures both profiles and records inviterID when the target has
// no inviter, returning whether it changed that relationship. Either
// nonpositive ID fails through EnsureUserAffiliate. An invalid inviter can
// leave a newly ensured target profile behind. The inviter profile is ensured
// before the existing-inviter check, so an unchanged binding can create that
// profile.
//
// The target's inviter pointer and update time change before the inviter's
// count is written from its loaded copy. These synchronous map writes have no
// lock or rollback, and the fixture accepts self-binding if called directly.
// Service validation and sequential fixture use govern the tested
// relationships.
func (r *oauthPendingFlowAffiliateRepo) BindInviter(_ context.Context, userID, inviterID int64) (bool, error) {
	profile, err := r.EnsureUserAffiliate(context.Background(), userID)
	if err != nil {
		return false, err
	}
	inviter, err := r.EnsureUserAffiliate(context.Background(), inviterID)
	if err != nil {
		return false, err
	}
	if profile.InviterID != nil {
		return false, nil
	}
	// The target has no inviter at this point. Record its relationship before
	// updating the inviter counter so subsequent sequential fixture reads observe
	// both changes.
	stored := r.profiles[userID]
	stored.InviterID = &inviterID
	stored.UpdatedAt = time.Now().UTC()
	r.profiles[inviterID].AffCount = inviter.AffCount + 1
	return true, nil
}

// setCode replaces a stored profile's referral code with trimmed uppercase text
// and marks it custom, failing t when the user cannot be ensured. It removes
// the preceding code lookup and writes the new profile/index relationship used
// by subsequent referral reads.
//
// The code index accepts replacement directly. A colliding supplied code can
// overwrite another indexed owner's lookup while leaving that owner's profile
// unchanged, so callers choose unique codes for these fixtures. The repository
// maps require sequential access during this change.
func (r *oauthPendingFlowAffiliateRepo) setCode(t *testing.T, userID int64, code string) {
	t.Helper()
	profile, err := r.EnsureUserAffiliate(context.Background(), userID)
	require.NoError(t, err)
	// Remove the preceding code lookup before changing the profile and new lookup,
	// so a later referral read does not still resolve the stale alias.
	delete(r.byCode, profile.AffCode)
	normalized := strings.ToUpper(strings.TrimSpace(code))
	stored := r.profiles[userID]
	stored.AffCode = normalized
	stored.AffCodeCustom = true
	r.byCode[normalized] = userID
}

// inviterIDFor returns the stored inviter ID and fails t when either the
// profile or inviter pointer is missing. It reads the current unsynchronized
// map value, so callers inspect it after the fixture's binding operation
// completes.
func (r *oauthPendingFlowAffiliateRepo) inviterIDFor(t *testing.T, userID int64) int64 {
	t.Helper()
	profile := r.profiles[userID]
	require.NotNil(t, profile)
	require.NotNil(t, profile.InviterID)
	return *profile.InviterID
}

// affCountFor returns the stored profile's invite counter and fails t when the
// profile is missing. It copies the current integer observation; callers use it
// after sequential binding or rejected-registration operations have completed.
func (r *oauthPendingFlowAffiliateRepo) affCountFor(t *testing.T, userID int64) int {
	t.Helper()
	profile := r.profiles[userID]
	require.NotNil(t, profile)
	return profile.AffCount
}

// AccrueQuota rejects unsupported referral quota accrual by panicking in this
// test fixture.
func (r *oauthPendingFlowAffiliateRepo) AccrueQuota(context.Context, int64, int64, float64, int, *int64) (bool, error) {
	panic("unexpected AccrueQuota call")
}

// GetAccruedRebateFromInvitee rejects unsupported invitee rebate lookup by
// panicking in this test fixture.
func (r *oauthPendingFlowAffiliateRepo) GetAccruedRebateFromInvitee(context.Context, int64, int64) (float64, error) {
	panic("unexpected GetAccruedRebateFromInvitee call")
}

// ThawFrozenQuota returns zero and nil error for every supplied user ID. It
// changes no profile quota in this fixture, so callers receive successful empty
// work while later summary reads keep their configured values.
func (r *oauthPendingFlowAffiliateRepo) ThawFrozenQuota(context.Context, int64) (float64, error) {
	return 0, nil
}

// TransferQuotaToBalance rejects unsupported referral balance transfers by
// panicking in this test fixture.
func (r *oauthPendingFlowAffiliateRepo) TransferQuotaToBalance(context.Context, int64) (float64, float64, error) {
	panic("unexpected TransferQuotaToBalance call")
}

// ListInvitees returns a nil slice and nil error for every supplied user and
// limit. It performs no profile traversal in this fixture; tests inspect
// recorded inviter IDs and counters through their separate helpers.
func (r *oauthPendingFlowAffiliateRepo) ListInvitees(context.Context, int64, int) ([]promotion.AffiliateInvitee, error) {
	return nil, nil
}

// UpdateUserAffCode rejects unsupported service referral-code updates by
// panicking in this test fixture.
func (r *oauthPendingFlowAffiliateRepo) UpdateUserAffCode(context.Context, int64, string) error {
	panic("unexpected UpdateUserAffCode call")
}

// ResetUserAffCode rejects unsupported service referral-code resets by
// panicking in this test fixture.
func (r *oauthPendingFlowAffiliateRepo) ResetUserAffCode(context.Context, int64) (string, error) {
	panic("unexpected ResetUserAffCode call")
}

// SetUserRebateRate rejects unsupported user rebate-rate updates by panicking
// in this test fixture.
func (r *oauthPendingFlowAffiliateRepo) SetUserRebateRate(context.Context, int64, *float64) error {
	panic("unexpected SetUserRebateRate call")
}

// BatchSetUserRebateRate rejects unsupported batch rebate-rate updates by
// panicking in this test fixture.
func (r *oauthPendingFlowAffiliateRepo) BatchSetUserRebateRate(context.Context, []int64, *float64) error {
	panic("unexpected BatchSetUserRebateRate call")
}

// ListUsersWithCustomSettings rejects unsupported custom-setting administrator
// listing by panicking in this test fixture.
func (r *oauthPendingFlowAffiliateRepo) ListUsersWithCustomSettings(context.Context, promotion.AffiliateAdminFilter) ([]promotion.AffiliateAdminEntry, int64, error) {
	panic("unexpected ListUsersWithCustomSettings call")
}

// ListAffiliateInviteRecords rejects unsupported referral invite audit listing
// by panicking in this test fixture.
func (r *oauthPendingFlowAffiliateRepo) ListAffiliateInviteRecords(context.Context, promotion.AffiliateRecordFilter) ([]promotion.AffiliateInviteRecord, int64, error) {
	panic("unexpected ListAffiliateInviteRecords call")
}

// ListAffiliateRebateRecords rejects unsupported referral rebate audit listing
// by panicking in this test fixture.
func (r *oauthPendingFlowAffiliateRepo) ListAffiliateRebateRecords(context.Context, promotion.AffiliateRecordFilter) ([]promotion.AffiliateRebateRecord, int64, error) {
	panic("unexpected ListAffiliateRebateRecords call")
}

// ListAffiliateTransferRecords rejects unsupported referral transfer audit
// listing by panicking in this test fixture.
func (r *oauthPendingFlowAffiliateRepo) ListAffiliateTransferRecords(context.Context, promotion.AffiliateRecordFilter) ([]promotion.AffiliateTransferRecord, int64, error) {
	panic("unexpected ListAffiliateTransferRecords call")
}

// GetAffiliateUserOverview rejects unsupported referral user overview reads by
// panicking in this test fixture.
func (r *oauthPendingFlowAffiliateRepo) GetAffiliateUserOverview(context.Context, int64) (*promotion.AffiliateUserOverview, error) {
	panic("unexpected GetAffiliateUserOverview call")
}

// AssignOrExtendSubscription appends a shallow copy of nonnil input and returns
// nil,false,nil for every call. Nil input records nothing; nested quota and
// source-order pointers remain shared, and the fixture creates no persisted
// subscription.
func (s *oauthPendingFlowDefaultSubAssignerStub) AssignOrExtendSubscription(
	_ context.Context,
	input *billing.AssignSubscriptionInput,
) (*billing.UserSubscription, bool, error) {
	if input != nil {
		s.calls = append(s.calls, *input)
	}
	return nil, false, nil
}

// GetSetupSession returns the retained setup pointer or nil when the receiver,
// map or user entry is absent. It ignores context and expiry; callers treat a
// returned session as shared with the fixture map.
func (s *oauthPendingFlowTotpCacheStub) GetSetupSession(_ context.Context, userID int64) (*identitycore.TotpSetupSession, error) {
	if s == nil || s.setupSessions == nil {
		return nil, nil
	}
	return s.setupSessions[userID], nil
}

// SetSetupSession retains the supplied pointer under userID, allocating the
// setup map when necessary. It ignores context and TTL; callers serialize
// access and keep referenced data stable while the service reads it.
func (s *oauthPendingFlowTotpCacheStub) SetSetupSession(_ context.Context, userID int64, session *identitycore.TotpSetupSession, _ time.Duration) error {
	if s.setupSessions == nil {
		s.setupSessions = map[int64]*identitycore.TotpSetupSession{}
	}
	s.setupSessions[userID] = session
	return nil
}

// DeleteSetupSession removes the user entry and reports success even when the
// setup map has no such record. The receiver is required, and callers serialize
// deletion with reads and writes of this fixture.
func (s *oauthPendingFlowTotpCacheStub) DeleteSetupSession(_ context.Context, userID int64) error {
	// Remove the setup pointer after service completion so a later read cannot
	// reuse that fixture challenge; deleting an absent map entry still succeeds.
	delete(s.setupSessions, userID)
	return nil
}

// GetLoginSession returns the retained login pointer or nil for a missing
// receiver, map or temporary-token entry. It ignores context and expiry, so
// returned pending-bind data remains shared with the fixture's stored session.
func (s *oauthPendingFlowTotpCacheStub) GetLoginSession(_ context.Context, tempToken string) (*identitycore.TotpLoginSession, error) {
	if s == nil || s.loginSessions == nil {
		return nil, nil
	}
	return s.loginSessions[tempToken], nil
}

// SetLoginSession retains session under tempToken and allocates the login map
// when needed. Context and TTL are ignored; callers serialize fixture access
// and control the lifetime of the shared session data.
func (s *oauthPendingFlowTotpCacheStub) SetLoginSession(_ context.Context, tempToken string, session *identitycore.TotpLoginSession, _ time.Duration) error {
	if s.loginSessions == nil {
		s.loginSessions = map[string]*identitycore.TotpLoginSession{}
	}
	s.loginSessions[tempToken] = session
	return nil
}

// DeleteLoginSession removes tempToken and returns success when the entry is
// already absent. The receiver is required; the service's successful challenge
// cleanup is observed by a later nil login-session read.
func (s *oauthPendingFlowTotpCacheStub) DeleteLoginSession(_ context.Context, tempToken string) error {
	// Login completion consumes its temporary challenge. Remove the retained
	// pending-bind reference so the subsequent fixture lookup observes that
	// cleanup as an absent session.
	delete(s.loginSessions, tempToken)
	return nil
}

// IncrementVerifyAttempts allocates the attempt map if needed, increments the
// selected user's count and returns it with nil error. Counts remain until
// ClearVerifyAttempts; context is ignored and callers serialize this fixture's
// mutations.
func (s *oauthPendingFlowTotpCacheStub) IncrementVerifyAttempts(_ context.Context, userID int64) (int, error) {
	if s.verifyAttempts == nil {
		s.verifyAttempts = map[int64]int{}
	}
	s.verifyAttempts[userID]++
	return s.verifyAttempts[userID], nil
}

// GetVerifyAttempts returns the stored count or zero for a missing receiver,
// map or user entry. It ignores context and imposes no attempt-window expiry,
// so increments remain observable until a clear.
func (s *oauthPendingFlowTotpCacheStub) GetVerifyAttempts(_ context.Context, userID int64) (int, error) {
	if s == nil || s.verifyAttempts == nil {
		return 0, nil
	}
	return s.verifyAttempts[userID], nil
}

// ClearVerifyAttempts removes the user counter and returns success for an
// absent entry. It requires a nonnil receiver; later reads observe zero while
// other users' attempt counts remain in the fixture map.
func (s *oauthPendingFlowTotpCacheStub) ClearVerifyAttempts(_ context.Context, userID int64) error {
	// Successful verification clears the recorded user count. Subsequent fixture
	// reads then begin from zero, while counts for other users remain available.
	delete(s.verifyAttempts, userID)
	return nil
}

// SetStepUpGrant returns success while discarding user, session and lifetime
// inputs. HasStepUpGrant still reports false afterward; this fixture supplies
// login/setup state but retains no administrator step-up grant.
func (s *oauthPendingFlowTotpCacheStub) SetStepUpGrant(_ context.Context, _ int64, _ string, _ time.Duration) error {
	return nil
}

// HasStepUpGrant returns false and nil error for every supplied user and
// session ID. Step-up writes retain no grant in this fixture, so successful
// SetStepUpGrant calls cannot authorize its later reads.
func (s *oauthPendingFlowTotpCacheStub) HasStepUpGrant(_ context.Context, _ int64, _ string) (bool, error) {
	return false, nil
}

// Encrypt returns the supplied secret text unchanged with nil error. Stored
// fixture secrets remain plaintext, so these TOTP scenarios exercise
// authentication flow and code verification rather than encryption behavior.
func (oauthPendingFlowTotpEncryptorStub) Encrypt(plaintext string) (string, error) {
	return plaintext, nil
}

// Decrypt returns the supplied stored text unchanged with nil error. The
// fixture accepts arbitrary text and performs no cryptographic conversion; the
// TOTP service subsequently interprets the returned value as its secret.
func (oauthPendingFlowTotpEncryptorStub) Decrypt(ciphertext string) (string, error) {
	return ciphertext, nil
}

// ConsumeRefreshToken reports false and nil error for every supplied credential
// hash. The fixture stores no refresh records and grants no consumption claim,
// so successful store calls cannot make a later consume succeed.
func (s *oauthPendingFlowRefreshTokenCacheStub) ConsumeRefreshToken(context.Context, string) (bool, error) {
	return false, nil
}

// WithLockedInviter invokes the required callback synchronously with ctx and
// returns its result. It ignores the inviter ID and acquires no lock, so
// callers serialize these fixture operations instead of inferring database
// locking from the method name.
func (r *oauthPendingFlowAffiliateRepo) WithLockedInviter(ctx context.Context, _ int64, fn func(context.Context) error) error {
	return fn(ctx)
}

// buildEncodedOAuthBindUserCookie signs userID with the supplied secret and
// fails t when signing rejects its inputs. It returns the binding value;
// encodedCookie applies the separate cookie-value encoding when requests need
// it.
func buildEncodedOAuthBindUserCookie(t *testing.T, userID int64, secret string) string {
	t.Helper()
	value, err := identitycore.BuildOAuthBindUserCookieValue(userID, secret)
	require.NoError(t, err)
	return value
}

// encodedCookie returns an HTTP cookie with name, the encoded raw value and
// root path. Inputs go to EncodeCookieValue as supplied; callers attach the
// returned cookie to a direct test request with AddCookie.
func encodedCookie(name, value string) *http.Cookie {
	return &http.Cookie{
		Name:  name,
		Value: identityhttp.EncodeCookieValue(value),
		Path:  "/",
	}
}

// findCookie returns the first cookie with the requested name, or nil when
// absent. The returned pointer belongs to the supplied slice; entries are
// expected to be nonnil while this helper scans them.
func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

// requireCookieCleared finds the response cookie and requires its MaxAge to
// equal negative one. It fails t for an absent cookie; HttpOnly, path and other
// deletion attributes are asserted separately by callers.
func requireCookieCleared(t *testing.T, recorder *httptest.ResponseRecorder, name string) {
	t.Helper()
	cookie := findCookie(recorder.Result().Cookies(), name)
	require.NotNil(t, cookie)
	require.Equal(t, -1, cookie.MaxAge)
}

// decodeCookieValueForTest applies the shared OAuth cookie decoder and fails t
// on invalid encoding. Callers pass only the cookie's value; the returned
// string can then be compared or parsed as signed binding data.
func decodeCookieValueForTest(t *testing.T, value string) string {
	t.Helper()
	decoded, err := identityhttp.DecodeCookieValue(value)
	require.NoError(t, err)
	return decoded
}

// assertOAuthRedirectError requires the parsed redirect's error and
// error_message values to match the supplied strings. Parsing follows
// parseOAuthRedirectFragment's query-before-fragment rule, and malformed or
// empty locations fail t.
func assertOAuthRedirectError(t *testing.T, location string, errorCode string, errorMessage string) {
	t.Helper()
	values := parseOAuthRedirectFragment(t, location)
	require.Equal(t, errorCode, values.Get("error"))
	require.Equal(t, errorMessage, values.Get("error_message"))
}

// parseOAuthRedirectFragment returns parsed values from a nonempty location,
// failing t on URL or query errors. A nonempty RawQuery takes precedence; only
// an empty query selects Fragment, so the two sources are never merged.
func parseOAuthRedirectFragment(t *testing.T, location string) url.Values {
	t.Helper()
	require.NotEmpty(t, location)

	parsed, err := url.Parse(location)
	require.NoError(t, err)

	rawValues := parsed.RawQuery
	if rawValues == "" {
		rawValues = parsed.Fragment
	}
	values, err := url.ParseQuery(rawValues)
	require.NoError(t, err)
	return values
}

// newWeChatOAuthTestHandler builds a shared SQLite fixture with open-mode
// WeChat defaults and the requested invitation setting. It returns the
// configured HTTP fixture and client; its pool and task-stop callbacks are
// registered on t.
func newWeChatOAuthTestHandler(t *testing.T, invitationEnabled bool) (*authHTTPFixture, *dbent.Client) {
	return newWeChatOAuthTestHandlerWithSettings(t, invitationEnabled, nil)
}

// wechatOAuthTestSettings returns a fresh enabled-WeChat map for mode, app
// credentials and frontend callback. Scope is selected by
// SettingsDefaultWeChatConnectScopesForMode; callers can override the returned
// entries before fixture construction.
func wechatOAuthTestSettings(mode, appID, secret, frontendRedirect string) map[string]string {
	return map[string]string{
		identitycore.SettingKeyWeChatConnectEnabled:             "true",
		identitycore.SettingKeyWeChatConnectAppID:               appID,
		identitycore.SettingKeyWeChatConnectAppSecret:           secret,
		identitycore.SettingKeyWeChatConnectMode:                mode,
		identitycore.SettingKeyWeChatConnectScopes:              identitycore.SettingsDefaultWeChatConnectScopesForMode(mode),
		identitycore.SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
		identitycore.SettingKeyWeChatConnectFrontendRedirectURL: frontendRedirect,
	}
}

// newWeChatOAuthTestHandlerWithSettings builds the named shared-memory SQLite
// WeChat fixture and enables registration. Supplied entries override its
// open-mode defaults; the returned handler retains cfg for later payment
// signing-key selection.
//
// Pool cleanup is registered before handler task-stop callbacks. Caller defers
// and later client-close cleanups can close database resources first, so this
// helper does not promise that every callback drains before the first close.
// Overlapping lifetimes of its fixed DSN can share tables; the cases use
// sequential fixtures.
func newWeChatOAuthTestHandlerWithSettings(t *testing.T, invitationEnabled bool, extraSettings map[string]string) (*authHTTPFixture, *dbent.Client) {
	t.Helper()

	db, err := sql.Open("sqlite", "file:auth_wechat_oauth?mode=memory&cache=shared")
	require.NoError(t, err)
	// Register pool closure before later task-stop callbacks so LIFO cleanup
	// reaches their waits first. Any explicit or later caller client close can
	// still run earlier; pool close errors are discarded.
	t.Cleanup(func() { _ = db.Close() })

	// Issue SQLite foreign-key setup before schema creation. This call configures
	// the connection used by Exec and does not establish the setting on every
	// connection the pool could open.
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))

	userRepo := &oauthPendingFlowUserRepo{client: client}
	redeemRepo := billingpostgres.NewRedeemCodeRepository(client)
	cfg := &config.Config{
		Auth: config.AuthConfig{JWT: config.JWTConfig{
			Secret:                "test-secret",
			AccessTokenTTLMinutes: 60,
			RefreshTokenTTLDays:   7,
		}},
		UserDefaults: config.UserDefaultsConfig{
			Balance:     0,
			Concurrency: 1,
		},
	}
	values := map[string]string{
		identitycore.SettingKeyRegistrationEnabled: "true",
		promotion.SettingKeyInvitationCodeEnabled:  boolSettingValue(invitationEnabled),
	}
	maps.Copy(values, wechatOAuthTestSettings("open", "wx-open-app", "wx-open-secret", "/auth/wechat/callback"))
	maps.Copy(values, extraSettings)
	settingSvc := newAuthSettingsFixture(&wechatOAuthSettingRepoStub{values: values}, cfg)

	authSvc := identitytestkit.Auth(
		client, &identitycore.AuthDependencies{Users: userRepo, Redeem: redeemRepo, RefreshTokens: &wechatOAuthRefreshTokenCacheStub{}, Options: identitytestkit.AuthOptions(cfg), Settings: authContractSettings(settingSvc)},
	)

	return newAuthHTTPFixture(t, &authHTTPFixture{
		authDB:      client,
		authService: authSvc,
		settingSvc:  settingSvc,
		cfg:         cfg,
	}), client
}

// Get returns ErrSettingNotFound and no Setting record for every supplied key.
// Use GetValue or bulk repository reads for the values installed by fixture
// setup; this method ignores context and its lookup input.
func (s *wechatOAuthSettingRepoStub) Get(context.Context, string) (*settings.Setting, error) {
	return nil, settings.ErrSettingNotFound
}

// GetValue returns the configured string or ErrSettingNotFound when key is
// absent. Context is ignored, and a present empty string counts as a successful
// read from the supplied values map.
func (s *wechatOAuthSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return value, nil
}

// Set reports success while discarding the supplied key and value. GetValue and
// bulk repository reads continue to use the setup values map, so this call does
// not update the fixture's stored observations.
func (s *wechatOAuthSettingRepoStub) Set(context.Context, string, string) error {
	return nil
}

// GetMultiple copies requested keys that exist in the supplied values map.
// Missing keys are omitted, and callers can mutate the result independently of
// the fixture's string-valued map; context is ignored.
func (s *wechatOAuthSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

// SetMultiple reports success while discarding the supplied entry map.
// Subsequent GetValue and bulk repository reads continue to use setup values,
// and this fixture retains none of the batch's supplied string entries.
func (s *wechatOAuthSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}

// GetAll returns a fresh map containing every configured string entry. Callers
// can mutate that result independently of values; the fixture ignores context
// and provides no injected repository read error.
func (s *wechatOAuthSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	result := make(map[string]string, len(s.values))
	maps.Copy(result, s.values)
	return result, nil
}

// Delete reports success for every key while retaining the supplied values map.
// Later GetValue and bulk repository reads therefore continue to observe the
// entry installed by setup, if that key exists.
func (s *wechatOAuthSettingRepoStub) Delete(context.Context, string) error {
	return nil
}

// StoreRefreshToken reports success while discarding the credential, record and
// lifetime inputs. Later GetRefreshToken calls still report the credential
// missing; the fixture permits token-response assertions but retains no
// server-side refresh session.
func (s *wechatOAuthRefreshTokenCacheStub) StoreRefreshToken(context.Context, string, *identitycore.RefreshTokenData, time.Duration) error {
	return nil
}

// GetRefreshToken returns ErrRefreshTokenNotFound and no record for every
// credential hash. Successful store calls retain no credentials in this
// fixture, so this read never recovers a session issued earlier in a test.
func (s *wechatOAuthRefreshTokenCacheStub) GetRefreshToken(context.Context, string) (*identitycore.RefreshTokenData, error) {
	return nil, identitycore.ErrRefreshTokenNotFound
}

// DeleteRefreshToken reports success for every credential hash and ignores
// context. The fixture retains no refresh records before or after deletion, so
// handler success does not provide an observation of stored credential removal.
func (s *wechatOAuthRefreshTokenCacheStub) DeleteRefreshToken(context.Context, string) error {
	return nil
}

// DeleteUserRefreshTokens reports success for every user ID and ignores
// context. User-token indexes are empty in this fixture; the call supplies no
// count or retained evidence of credentials removed for that user.
func (s *wechatOAuthRefreshTokenCacheStub) DeleteUserRefreshTokens(context.Context, int64) error {
	return nil
}

// DeleteTokenFamily reports success for every supplied family ID and ignores
// context. Family-token indexes remain empty in this fixture, so callers
// receive successful cleanup while no stored family membership is inspected or
// changed.
func (s *wechatOAuthRefreshTokenCacheStub) DeleteTokenFamily(context.Context, string) error {
	return nil
}

// AddToUserTokenSet reports success while discarding user, hash and lifetime
// inputs. The fixture keeps no user-token index, so subsequent
// GetUserTokenHashes results remain empty despite accepted registration calls.
func (s *wechatOAuthRefreshTokenCacheStub) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}

// AddToFamilyTokenSet reports success while discarding family, hash and
// lifetime inputs. It retains no membership state, so later family-hash reads
// and membership checks stay empty and false respectively.
func (s *wechatOAuthRefreshTokenCacheStub) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

// GetUserTokenHashes returns a nil slice with nil error for every user ID. User
// registration and credential stores retain no index in this fixture, so prior
// successful writes do not populate that result.
func (s *wechatOAuthRefreshTokenCacheStub) GetUserTokenHashes(context.Context, int64) ([]string, error) {
	return nil, nil
}

// GetFamilyTokenHashes returns a nil slice with nil error for every family ID.
// Family registration calls retain no hashes in this fixture, so earlier
// successful writes do not populate the returned credential list.
func (s *wechatOAuthRefreshTokenCacheStub) GetFamilyTokenHashes(context.Context, string) ([]string, error) {
	return nil, nil
}

// IsTokenInFamily reports false and nil error for every family and hash pair.
// Family registration retains no membership in this fixture, so the result
// stays false after successful AddToFamilyTokenSet calls.
func (s *wechatOAuthRefreshTokenCacheStub) IsTokenInFamily(context.Context, string, string) (bool, error) {
	return false, nil
}

// ConsumeRefreshToken reports false and nil error for every supplied credential
// hash. The fixture stores no refresh records and grants no consumption claim,
// so successful store calls cannot make a later consume succeed.
func (s *wechatOAuthRefreshTokenCacheStub) ConsumeRefreshToken(context.Context, string) (bool, error) {
	return false, nil
}

// authBackgroundFixture returns lifecycle.Tasks.Go for named asynchronous work
// and registers a bounded stop on t. Cleanup waits up to thirty seconds and
// reports a stop error, while accepted callbacks retain their own contexts.
// Callers arrange database closure order themselves; later cleanup
// registrations and defers can close a client before this wait runs. Capture
// stable callback inputs before scheduling; callers provide callback
// cancellation and keep dependencies available while accepted work runs.
func authBackgroundFixture(t *testing.T) func(string, func()) bool {
	t.Helper()
	tasks := lifecycle.NewTasks()
	t.Cleanup(func() {
		// Bound the stop wait for accepted work so cleanup can report an incomplete
		// callback group. Cancelling this waiting context releases its timer;
		// callback contexts remain independent.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := tasks.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	return tasks.Go
}

// newAuthHTTPFixture assembles handlers into input and returns that same
// pointer. Input is required; callers can later replace retained dependencies
// and invoke bindAuthHTTPFixture again before dispatching requests through the
// promoted handlers.
func newAuthHTTPFixture(t *testing.T, input *authHTTPFixture) *authHTTPFixture {
	t.Helper()
	bindAuthHTTPFixture(t, input)
	return input
}

// bindAuthHTTPFixture rebuilds authentication and WeChat payment handlers from
// h's current dependencies. Call it before request dispatch and after replacing
// fields that constructors retain directly. Runtime provider settings take
// precedence over static cfg, while the Google verifier and payment signing
// inputs are read dynamically.
//
// Logout needs the completed pending handler, so callers start dispatch after
// this function returns. Optional dependencies remain nil when absent;
// early-rejection tests rely on reaching their validation result before those
// dependencies are called.
func bindAuthHTTPFixture(t *testing.T, h *authHTTPFixture) {
	t.Helper()
	var client *dbent.Client
	if h.authService != nil {
		client = h.authDB
	}
	flow := &identitycore.PendingFlow{Store: identitypostgres.NewPendingRepository(client), Database: &identitypostgres.PendingFlowDatabase{Client: client, Auth: h.authService, Profiles: h.userService}, Auth: h.authService, Profiles: h.userService}
	var sessionSettings identityhttp.SessionHTTPSettings
	if h.settingSvc != nil {
		sessionSettings = h.settingSvc
	}
	secret := ""
	if h.cfg != nil {
		secret = strings.TrimSpace(h.cfg.Auth.JWT.Secret)
	}
	// NewSessionHandler stores this callback without invoking it. Capture the
	// pending variable so the later NewPendingHandler assignment supplies the
	// handler used when logout eventually dispatches.
	var pending *identityhttp.PendingHandler
	session := identityhttp.NewSessionHandler(h.authService, h.userService, sessionSettings, h.redeemService, h.totpService, flow, identityhttp.SessionHTTPOptions{AuditActor: middleware.SetAuditActor, BackendMode: func(ctx context.Context) bool {
		if h.settingSvc == nil {
			return false
		}
		// Prefer backend mode from a successful public-settings read; an error or
		// missing result falls through to the dedicated backend reader for this
		// fixture.
		v, e := h.settingSvc.public.GetPublicSettings(ctx)
		if e == nil && v != nil {
			return v.BackendModeEnabled
		}
		return h.settingSvc.IsBackendModeEnabled(ctx)
	}, ClearPendingCookies: func(c *gin.Context) {
		secure := identityhttp.IsRequestHTTPS(c)
		identityhttp.ClearOAuthPendingSessionCookie(c, secure)
		identityhttp.ClearOAuthPendingBrowserCookie(c, secure)
	}, LogoutPending: func(c *gin.Context) {
		pending.ConsumePendingOAuthSessionOnLogout(c)
		identityhttp.ClearOAuthLoginCookies(c)
		paymenthttp.ClearWeChatPaymentCookies(c)
	}, PreviewPromotion: func(ctx context.Context, code string) identityhttp.PromotionPreview {
		v := h.promoService.PreviewRegistrationPromotion(ctx, code)
		return identityhttp.PromotionPreview{Valid: v.Valid, BonusAmount: v.BonusAmount, ErrorCode: v.ErrorCode}
	}})
	bind := identityhttp.NewOAuthBindHandler(session, identitycore.NewOAuthBindingSigner(secret))
	linux := func(ctx context.Context) (identitycore.LinuxDoOAuthOptions, error) {
		// Choose the runtime reader when present; static-config cases clear
		// settingSvc so this callback reads cfg while keeping the same provider
		// handler.
		if h.settingSvc != nil {
			v, e := h.settingSvc.oauth.GetLinuxDoConnectOAuthConfig(ctx)
			return identitycore.LinuxDoOAuthOptions(v), e
		}
		if h.cfg == nil {
			return identitycore.LinuxDoOAuthOptions{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
		}
		if !h.cfg.Auth.OAuth.LinuxDo.Enabled {
			return identitycore.LinuxDoOAuthOptions{}, apperror.NotFound("OAUTH_DISABLED", "oauth login is disabled")
		}
		return identitycore.LinuxDoOAuthOptions(h.cfg.Auth.OAuth.LinuxDo), nil
	}
	oidc := func(ctx context.Context) (identitycore.OIDCOAuthOptions, error) {
		// Choose the runtime reader when present; static-config cases clear
		// settingSvc so this callback reads cfg while keeping the same provider
		// handler.
		if h.settingSvc != nil {
			v, e := h.settingSvc.oauth.GetOIDCConnectOAuthConfig(ctx)
			return identitycore.OIDCOAuthOptions(v), e
		}
		if h.cfg == nil {
			return identitycore.OIDCOAuthOptions{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
		}
		if !h.cfg.Auth.OAuth.OIDC.Enabled {
			return identitycore.OIDCOAuthOptions{}, apperror.NotFound("OAUTH_DISABLED", "oauth login is disabled")
		}
		return identitycore.OIDCOAuthOptions(h.cfg.Auth.OAuth.OIDC), nil
	}
	dingConfig := func(ctx context.Context) (identitycore.DingTalkOAuthOptions, error) {
		// Choose the runtime reader when present; static-config cases clear
		// settingSvc so this callback reads cfg while keeping the same provider
		// handler.
		if h.settingSvc != nil {
			v, e := h.settingSvc.oauth.GetDingTalkConnectOAuthConfig(ctx)
			return identitycore.DingTalkOAuthOptions(v), e
		}
		if h.cfg == nil {
			return identitycore.DingTalkOAuthOptions{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
		}
		if !h.cfg.Auth.OAuth.DingTalk.Enabled {
			return identitycore.DingTalkOAuthOptions{}, apperror.NotFound("OAUTH_DISABLED", "dingtalk oauth login is disabled")
		}
		return identitycore.DingTalkOAuthOptions(h.cfg.Auth.OAuth.DingTalk.IdentityDefaults()), nil
	}
	clients := &provider.DingTalkClients{}
	syncer := &identitycore.DingTalkSyncRuntime{LoadConfig: dingConfig, Client: func(v identitycore.DingTalkOAuthOptions) identitycore.DingTalkOAuthClient {
		return clients.ForConfig(provider.DingTalkClientConfig{ClientID: v.ClientID, ClientSecret: v.ClientSecret, TokenURL: v.TokenURL, UserInfoURL: v.UserInfoURL})
	}, Profiles: &identitycore.DingTalkProfileSync{Users: h.userService, Attributes: h.userAttributeService}, Run: authBackgroundFixture(t)}
	pending = identityhttp.NewPendingHandler(session, flow, identityhttp.PendingHTTPOptions{ForceEmailOnSignup: func(ctx context.Context) bool {
		if h.settingSvc == nil {
			return false
		}
		// Missing or failed source-default reads produce false here. This callback
		// requests forced email completion only after obtaining a usable settings
		// value with that flag set.
		v, e := h.settingSvc.GetAuthSourceDefaultSettings(ctx)
		return e == nil && v != nil && v.ForceEmailOnThirdPartySignup
	}, AfterLogin: func(ctx context.Context, p *identitycore.PendingAuthSession, id int64) {
		syncer.Pending(ctx, p, id, false)
	}, AfterRegistration: func(ctx context.Context, p *identitycore.PendingAuthSession, id int64) {
		syncer.Pending(ctx, p, id, true)
	}, BeforeAccountCommit: func(ctx context.Context, p *identitycore.PendingAuthSession) error {
		if pendingOAuthCreateAccountPreCommitHook == nil {
			return nil
		}
		return pendingOAuthCreateAccountPreCommitHook(ctx, identitypostgres.PendingAuthSessionToEntity(p))
	}})
	email := identityhttp.NewEmailOAuthHandler(pending, provider.EmailOAuthClientAdapter{}, func(ctx context.Context, name string) (identitycore.EmailOAuthOptions, error) {
		if h.settingSvc == nil {
			return identitycore.EmailOAuthOptions{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
		}
		v, e := h.settingSvc.oauth.GetEmailOAuthProviderConfig(ctx, name)
		return identitycore.EmailOAuthOptions(v), e
	})
	var registration func(context.Context) bool
	if h.settingSvc != nil {
		registration = h.settingSvc.IsRegistrationEnabled
	}
	google := identityhttp.NewGoogleOneTapHandler(pending, googleVerifierFixture{h}, identityhttp.GoogleOneTapHTTPOptions{RegistrationEnabled: registration, LoadConfig: func(ctx context.Context) (identityhttp.GoogleOneTapOptions, error) {
		if h.settingSvc == nil {
			return identityhttp.GoogleOneTapOptions{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
		}
		v, e := h.settingSvc.oauth.GetGoogleOneTapConfig(ctx)
		return identityhttp.GoogleOneTapOptions{ClientID: v.ClientID, FrontendRedirectURL: v.FrontendRedirectURL}, e
	}})

	// Use a nonblank runtime frontend callback when its read succeeds; missing
	// readers, failed reads or blank values select the shared WeChat callback
	// path.
	wechatOptions := identityhttp.WeChatHTTPOptions{FrontendCallback: func(ctx context.Context) string {
		if h.settingSvc != nil {
			v, e := h.settingSvc.oauth.GetWeChatConnectOAuthConfig(ctx)
			if e == nil && strings.TrimSpace(v.FrontendRedirectURL) != "" {
				return strings.TrimSpace(v.FrontendRedirectURL)
			}
		}
		return identityhttp.WechatOAuthDefaultFrontendCB
	}}
	if h.settingSvc != nil {
		wechatOptions.LoadConfig = func(ctx context.Context, mode string) (identitycore.WeChatOAuthOptions, error) {
			v, e := h.settingSvc.oauth.GetWeChatConnectOAuthConfig(ctx)
			if e != nil {
				return identitycore.WeChatOAuthOptions{}, e
			}
			return identitycore.WeChatOAuthOptions{
				Mode:             mode,
				AppID:            v.AppIDForMode(mode),
				AppSecret:        v.AppSecretForMode(mode),
				Scope:            v.ScopeForMode(mode),
				RedirectURI:      v.RedirectURL,
				FrontendCallback: v.FrontendRedirectURL,
				OpenEnabled:      v.OpenEnabled,
				MPEnabled:        v.MPEnabled,
			}, nil
		}
	}
	wechat := identityhttp.NewWeChatHandler(pending, bind, provider.WeChatClient{TokenURL: wechatOAuthAccessTokenURL, UserInfoURL: wechatOAuthUserInfoURL}, wechatOptions)
	h.AuthenticationHandler = &identityhttp.AuthenticationHandler{Session: session, Pending: pending, Bind: bind, LinuxDo: identityhttp.NewLinuxDoHandler(pending, bind, provider.LinuxDoClient{}, linux), OIDC: identityhttp.NewOIDCHandler(pending, bind, provider.OIDCClient{}, oidc), Email: email, Google: google, WeChat: wechat, DingTalk: identityhttp.NewDingTalkHandler(pending, bind, syncer, identityhttp.DingTalkHTTPOptions{LoadConfig: dingConfig, RegistrationEnabled: registration})}
	h.WeChatPaymentHandler = paymenthttp.NewWeChatPaymentHandler(paymenthttp.WeChatPaymentHTTPOptions{Config: wechat.GetConfig, CallbackURL: func(_ context.Context, c *gin.Context) string {
		return identityhttp.ResolveWeChatOAuthAbsoluteURL(c, "/api/v1/auth/oauth/wechat/payment/callback")
	}, Resume: h.paymentResume, Exchange: func(ctx context.Context, v identitycore.WeChatOAuthOptions, code string) (paymenthttp.WeChatPaymentToken, error) {
		value, err := provider.ExchangeWeChatOAuthCode(ctx, provider.WeChatOptions{AppID: v.AppID, AppSecret: v.AppSecret, TokenURL: wechatOAuthAccessTokenURL}, code)
		if err != nil {
			return paymenthttp.WeChatPaymentToken{}, err
		}
		return paymenthttp.WeChatPaymentToken{OpenID: value.OpenID, Scope: value.Scope}, nil
	}})
}

// Verify loads h's current injected Google verifier and delegates credential,
// audience and context to it. Nil injection selects GoogleAPIIDTokenVerifier,
// allowing tests to install a fixture after the enclosing HTTP handler has been
// built.
func (v googleVerifierFixture) Verify(ctx context.Context, credential, audience string) (*provider.GoogleIDTokenClaims, error) {
	verifier := v.h.googleIDTokenVerifier
	if verifier == nil {
		verifier = provider.GoogleAPIIDTokenVerifier{}
	}
	return verifier.Verify(ctx, credential, audience)
}

// paymentResume builds a fresh resume service from environment signing text and
// available configured legacy encryption material. It logs
// ConfiguredEncryptionKey warning text and omits legacy material when parsing
// fails; the resolver chooses the primary key and distinct verification
// fallbacks. NewPaymentResumeService copies the selected byte slices.
func (h *authHTTPFixture) paymentResume() *payment.PaymentResumeService {
	var raw string
	var configured bool
	if h.cfg != nil {
		raw = h.cfg.Security.SecretEncryptionKey
		configured = h.cfg.Security.SecretEncryptionKeyConfigured
	}
	key, warning, err := payment.ConfiguredEncryptionKey(raw, configured)
	if warning != "" {
		slog.Warn(warning)
	}
	var legacy []byte
	// A failed encryption-key parse contributes no legacy verification bytes. The
	// resolver still receives environment signing text, which can supply the
	// primary key independently.
	if err == nil {
		legacy = []byte(key)
	}
	signing, fallbacks := payment.ResolvePaymentResumeSigningKeys(h.cfg.Payment.ResumeSigningKey, legacy)
	return payment.NewPaymentResumeService(signing, fallbacks...)
}

// newAuthSettingsFixture builds authentication, promotion and public/aggregate
// readers over one supplied repository. Nil cfg selects an empty configuration;
// OAuth defaults are copied during construction, while several default
// callbacks retain cfg for later reads.
func newAuthSettingsFixture(repo settings.Repository, cfg *config.Config) *authSettingsFixture {
	if cfg == nil {
		cfg = &config.Config{}
	}
	store := settings.New(repo)
	oauth := identitycore.NewOAuthSettings(store, &identitycore.OAuthSettingsDefaults{LinuxDo: cfg.Auth.OAuth.LinuxDo, DingTalk: cfg.Auth.OAuth.DingTalk.IdentityDefaults(), OIDC: cfg.Auth.OAuth.OIDC, WeChat: cfg.Auth.OAuth.WeChat.IdentityDefaults(), GitHubOAuth: cfg.Auth.OAuth.GitHub, GoogleOAuth: cfg.Auth.OAuth.Google}, provider.ResolveSettingsOIDCMetadata)
	grants := identitycore.NewGrantSettings(store, identitycore.GrantSettingsOptions{DefaultBalance: cfg.UserDefaults.Balance, DefaultConcurrency: cfg.UserDefaults.Concurrency})
	value := &authSettingsFixture{RuntimeSettings: identitycore.NewRuntimeSettings(store, settings.ErrSettingNotFound), GrantSettings: grants, DisplaySettings: site.NewDisplaySettings(store, func() string { return cfg.Site.FrontendURL }), oauth: oauth, promotion: promotion.NewRuntimeSettings(store), backend: admission.NewBackendMode(store, nil)}
	value.public = site.NewPublicService(site.NewInputSource(store, site.PublicInputOptions{Auth: func(raw map[string]string) site.PublicAuth {
		v := oauth.PublicSettingsFromValues(raw)
		enabled, selfService := team.PublicSettings(raw, cfg.Team.Enabled, cfg.Team.SelfServiceEnabled)
		return site.PublicAuth{LinuxDo: v.LinuxDo, DingTalk: v.DingTalk, OIDC: v.OIDC, OIDCName: v.OIDCName, WeChat: v.WeChat, WeChatOpen: v.WeChatOpen, WeChatMP: v.WeChatMP, WeChatMobile: v.WeChatMobile, GitHub: v.GitHub, Google: v.Google, GoogleOneTap: v.GoogleOneTap, GoogleClientID: v.GoogleClientID, Team: enabled, TeamSelfService: selfService, Passkey: cfg.Auth.WebAuthn.Enabled, TencentRegion: v.TencentRegion, AliyunRegion: v.AliyunRegion, RegistrationSuffixes: v.RegistrationSuffixes}
	}, Usage: func(raw map[string]string) site.PublicUsage {
		v := usage.ParseRankingSettings(raw)
		return site.PublicUsage{Limit: v.Limit, Enabled: v.Enabled, SortBy: string(v.SortBy), ShowTotalTokens: v.ShowTotalTokens, ShowRequests: v.ShowRequests, ShowActualCost: v.ShowActualCost}
	}, Version: store.Version}), timezone.NewCalendar(time.Local), "")
	rules := gateway.AdminSettingsRules{GrokDefaultTextModel: grok.DefaultTextModel, NormalizeUserAgentVersion: antigravity.NormalizeUserAgentVersion, ValidateClaudePromptBlocks: anthropic.ValidateClaudeOAuthSystemPromptBlocksConfig}
	read := composite.ReadOptions{OAuth: oauth, Gateway: rules, Scheduler: scheduler.DefaultAdminSettingsDefaults(), DefaultBalance: func() float64 { return cfg.UserDefaults.Balance }, DefaultConcurrency: func() int { return cfg.UserDefaults.Concurrency }, Forwarded: func() runtimeconfig.ForwardedInput {
		v := cfg.ForwardedClientIPSettings()
		return runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: v.TrustForwardedIP, ForwardedClientIPHeaders: v.Headers}
	}, PublishModel: func(model string) {
	}}
	value.composite = composite.NewRuntime(store, read, composite.PrepareOptions{}, grants, nil, cfg.Security.SecretEncryptionKeyConfigured, nil)
	return value
}

// GetDingTalkConnectOAuthConfig returns enabled, bypass-registration and
// corporation-policy values from the OAuth settings reader. It also returns
// that reader's error with the mapped value, so callers decide whether a
// failing read is usable.
func (s *authSettingsFixture) GetDingTalkConnectOAuthConfig(ctx context.Context) (identitycore.DingTalkRegistrationPolicy, error) {
	v, e := s.oauth.GetDingTalkConnectOAuthConfig(ctx)
	return identitycore.DingTalkRegistrationPolicy{Enabled: v.Enabled, BypassRegistration: v.BypassRegistration, CorpRestrictionPolicy: v.CorpRestrictionPolicy}, e
}

// IsInvitationCodeEnabled reads whether registration requires an invitation
// through the promotion reader. Only exact true enables that requirement;
// missing settings or failed reads return false to its signup callers.
func (s *authSettingsFixture) IsInvitationCodeEnabled(ctx context.Context) bool {
	return s.promotion.IsInvitationCodeEnabled(ctx)
}

// IsPromoCodeEnabled reads whether registration promotion grants are enabled
// through the promotion reader. Only exact false disables them; missing
// settings or failed reads keep those grants enabled for signup preview.
func (s *authSettingsFixture) IsPromoCodeEnabled(ctx context.Context) bool {
	return s.promotion.IsPromoCodeEnabled(ctx)
}

// IsBackendModeEnabled returns the admission reader's cached or refreshed
// backend choice. Pass a nonnil context; refresh uses an independent
// five-second read budget and resolves missing or failed reads as false unless
// a newer administrative publication wins.
func (s *authSettingsFixture) IsBackendModeEnabled(ctx context.Context) bool {
	return s.backend.Enabled(ctx)
}

// authContractSettings returns the fixture as AuthSettings, or a nil interface
// when the fixture is nil. The nil branch keeps absence observable to
// authentication constructors because wrapping a nil fixture pointer would
// otherwise produce a nonnil interface with that nil pointer inside.
func authContractSettings(s *authSettingsFixture) identitycore.AuthSettings {
	if s == nil {
		return nil
	}
	return s
}
