package identity_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
)

// Signup captures selected limits and grants in the created account, while
// existing OAuth accounts retain their prior values. These tests vary
// settings presence and provider policy, and inspect actual returned fields
// or recorded calls rather than assuming every authentication entry has the
// same CAPTCHA coverage.
//
// The invitation race uses independently synchronized account and invitation
// fixtures. A start barrier releases competing registrations together, while
// the invitation status check and claim occur under one fixture mutex. Its
// assertions establish a single successful claim and represented rejections;
// shared fixture assembly still supplies database-free creation and fixed
// cache results, so account rollback and real database isolation require
// other tests.

// invitationRaceUserRepo serializes exact-mailbox creation and retains
// shallow account copies for racing registrations.
type invitationRaceUserRepo struct {
	// UserRepository supplies methods outside the implemented lookup/create
	// subset. A nil embedded implementation cannot handle other account
	// capabilities reached by a changed test flow.
	identity.UserRepository

	// mu protects nextID and byEmail across exact lookup and creation calls.
	// Create checks occupancy and publishes its shallow clone before releasing
	// that lock.
	mu      sync.Mutex                // account creation state lock
	nextID  int64                     // next assigned account identifier
	byEmail map[string]*identity.User // retained exact mailbox accounts
}

// invitationRaceRedeemRepo guards one invitation and conditionally publishes
// its used state for concurrent tests.
type invitationRaceRedeemRepo struct {
	// RedeemCodeRepository supplies redemption methods outside GetByCode and
	// Use. Tests populate it before reaching any other method; the race fixture
	// itself models this invitation only.
	billing.RedeemCodeRepository

	// mu protects code during copies and conditional claims. Use checks unused
	// status before setting its used fields, so competing calls observe one
	// serialized claim.
	mu sync.Mutex // invitation claim state lock
	// code is the configured invitation value. GetByCode copies its outer fields
	// but retains nested pointers, which test callers keep stable while claims
	// run.
	code billing.RedeemCode // configured invitation claim record
}

// TestAuthService_RegisterSnapshotsDefaultUserAPIKeyLimit registers accounts
// with missing, zero, positive and invalid stored Key limits. Returned and
// recorded account fields match the selected signup snapshot, including the
// built-in fallback.
func TestAuthService_RegisterSnapshotsDefaultUserAPIKeyLimit(t *testing.T) {
	tests := []struct {
		name string // descriptive Key-limit case name
		// value supplies stored limit text when nonnil. Nil omits that key so the
		// registration reader exercises its missing-setting choice.
		value  *string // optional stored limit text
		expect int     // expected signup Key limit
	}{
		{name: "设置缺失回退内置值", expect: identity.DefaultUserAPIKeyLimit},
		{name: "显式不限量", value: stringPointer("0"), expect: 0},
		{name: "显式上限", value: stringPointer("23"), expect: 23},
		{name: "非法设置回退内置值", value: stringPointer("invalid"), expect: identity.DefaultUserAPIKeyLimit},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := map[string]string{identity.SettingKeyRegistrationEnabled: "true"}
			if tt.value != nil {
				settings[identity.SettingKeyDefaultUserAPIKeyLimit] = *tt.value
			}
			repo := &userRepoStub{nextID: int64(index + 1)}
			svc := newAuthService(repo, settings, nil)

			_, user, err := svc.RegisterWithVerification(context.Background(), fmt.Sprintf("api-limit-%d@example.com", index), "strong-pass", "", "", "", "")
			require.NoError(t, err)
			require.Equal(t, tt.expect, user.APIKeyLimit)
			require.Equal(t, tt.expect, repo.created[0].APIKeyLimit)
		})
	}
}

// TestAuthService_DefaultUserAPIKeyLimitDoesNotRetroactivelyChangeUsers
// changes the stored Key limit between two registrations. The second account
// receives the new limit while the first returned/recorded account retains
// its earlier snapshot.
func TestAuthService_DefaultUserAPIKeyLimitDoesNotRetroactivelyChangeUsers(t *testing.T) {
	settings := map[string]string{
		identity.SettingKeyRegistrationEnabled:    "true",
		identity.SettingKeyDefaultUserAPIKeyLimit: "10",
	}
	repo := &userRepoStub{}
	svc := newAuthService(repo, settings, nil)

	_, first, err := svc.RegisterWithVerification(context.Background(), "api-limit-first@example.com", "strong-pass", "", "", "", "")
	require.NoError(t, err)
	require.Equal(t, 10, first.APIKeyLimit)

	settings[identity.SettingKeyDefaultUserAPIKeyLimit] = "20"
	_, second, err := svc.RegisterWithVerification(context.Background(), "api-limit-second@example.com", "strong-pass", "", "", "", "")
	require.NoError(t, err)
	require.Equal(t, 20, second.APIKeyLimit)
	require.Equal(t, 10, first.APIKeyLimit)
	require.Equal(t, 10, repo.created[0].APIKeyLimit)
}

// TestAuthService_AllOAuthSourcesSnapshotDefaultUserAPIKeyLimit creates
// accounts through each of six provider source labels with one configured Key
// limit. Each returned and recorded account receives that limit during
// signup.
func TestAuthService_AllOAuthSourcesSnapshotDefaultUserAPIKeyLimit(t *testing.T) {
	for index, signupSource := range []string{"linuxdo", "wechat", "oidc", "github", "google", "dingtalk"} {
		t.Run(signupSource, func(t *testing.T) {
			repo := &userRepoStub{}
			svc := newAuthService(repo, map[string]string{
				identity.SettingKeyRegistrationEnabled:    "true",
				identity.SettingKeyDefaultUserAPIKeyLimit: "29",
			}, nil)
			svc.RefreshTokens = &refreshTokenCacheStub{}
			rebuildSessionForTest(svc)

			_, user, err := svc.LoginOrRegisterOAuthWithTokenPair(context.Background(), fmt.Sprintf("api-limit-oauth-%d@example.com", index), "OAuth User", "", "", signupSource)
			require.NoError(t, err)
			require.Equal(t, 29, user.APIKeyLimit)
			require.Equal(t, 29, repo.created[0].APIKeyLimit)
		})
	}
}

// stringPointer returns a pointer to the copied string argument for
// optional-setting cases.
func stringPointer(value string) *string {
	return &value
}

// newInvitationRaceUserRepo creates a fresh mutex-backed exact-email map with
// IDs beginning at one. Unsupported embedded repository methods require a
// separate implementation; the fixture supports the account creation path
// used by the invitation race.
func newInvitationRaceUserRepo() *invitationRaceUserRepo {
	return &invitationRaceUserRepo{
		nextID:  1,
		byEmail: make(map[string]*identity.User),
	}
}

// ExistsByEmail reads exact-mailbox occupancy while holding the fixture
// mutex. It returns the map observation with nil error; the supplied text is
// not normalized and later creation performs its own locked conflict check.
func (r *invitationRaceUserRepo) ExistsByEmail(_ context.Context, email string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.byEmail[email]
	return exists, nil
}

// ExistsByNormalizedEmail reports no alias-mailbox conflict for this
// invitation race fixture.
func (r *invitationRaceUserRepo) ExistsByNormalizedEmail(context.Context, string) (bool, error) {
	return false, nil
}

// Create checks exact-email uniqueness and publishes an outer account copy
// under one mutex. Successful calls assign nextID to the submitted account
// ID, advance the counter and retain the outer clone.
//
// The lock keeps concurrent create calls from publishing the same exact email
// or reusing an assigned ID. Nested account references remain shared, and
// this fixture has no account rollback or deletion implementation; the
// separate invitation fixture owns the conditional invitation claim observed
// by the race test.
func (r *invitationRaceUserRepo) Create(_ context.Context, user *identity.User) error {
	// Hold exact-email conflict checking, ID allocation and account publication
	// together so competing create calls cannot observe an unpublished account
	// or reuse this fixture ID.
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byEmail[user.Email]; exists {
		return identity.ErrEmailExists
	}
	user.ID = r.nextID
	r.nextID++
	clone := *user
	r.byEmail[user.Email] = &clone
	return nil
}

// GetByCode returns a shallow invitation copy after matching its exact code
// under the mutex. Unknown codes return redeem-not-found; nested pointer
// fields of the returned value remain shared with fixture state.
func (r *invitationRaceRedeemRepo) GetByCode(_ context.Context, code string) (*billing.RedeemCode, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code.Code != code {
		return nil, billing.ErrRedeemCodeNotFound
	}
	clone := r.code
	return &clone, nil
}

// Use claims the configured invitation under its mutex when ID matches and
// status is unused. Missing IDs return redeem-not-found and other statuses
// return redeem-used before any claim fields change.
//
// The status check and used status/user/time/count publication share one
// critical section, allowing one successful claim among competing
// registrations. Account creation is held in another fixture with no shared
// transaction, so this local claim guarantee says nothing about rollback of
// accounts from rejected registrations.
func (r *invitationRaceRedeemRepo) Use(_ context.Context, id, userID int64) error {
	// Keep unused-state checking and used-field publication in one critical
	// section; competing registrations then claim this configured invitation at
	// most once in the fixture.
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code.ID != id {
		return billing.ErrRedeemCodeNotFound
	}
	if r.code.Status != billing.StatusUnused {
		return billing.ErrRedeemCodeUsed
	}
	now := time.Now().UTC()
	r.code.Status = billing.StatusUsed
	r.code.UsedBy = &userID
	r.code.UsedAt = &now
	r.code.UsedCount = 1
	return nil
}

// TestAuthService_Register_InvitationCodeSingleUseUnderConcurrency releases
// eight distinct-mailbox registrations against one mutex-protected invitation
// fixture. It observes one success, seven invalid-invitation failures and
// used status; the fixture does not model account rollback.
func TestAuthService_Register_InvitationCodeSingleUseUnderConcurrency(t *testing.T) {
	const (
		code   = "INV-RACE-001"
		called = 8
	)
	userRepo := newInvitationRaceUserRepo()
	redeemRepo := &invitationRaceRedeemRepo{code: billing.RedeemCode{
		ID:     1,
		Code:   code,
		Type:   billing.RedeemTypeInvitation,
		Status: billing.StatusUnused,
	}}
	svc := newOAuthEmailFlowAuthService(
		userRepo,
		redeemRepo,
		&refreshTokenCacheStub{},
		map[string]string{
			identity.SettingKeyRegistrationEnabled:    "true",
			promotion.SettingKeyInvitationCodeEnabled: "true",
		},
		nil,
	)

	// The start barrier releases all prepared registrations together; buffered
	// results let each worker report before the parent waits for every sender to
	// finish.
	start := make(chan struct{})
	results := make(chan error, called)
	var group sync.WaitGroup
	for index := range called {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, _, err := svc.RegisterWithVerification(
				context.Background(),
				fmt.Sprintf("race-%d@example.com", index),
				"Password123!",
				"",
				"",
				code,
				"",
			)
			results <- err
		}(index)
	}
	// Release the waiting workers, then join them before closing result
	// delivery, keeping every send valid while the parent collects the completed
	// registration outcomes.
	close(start)
	group.Wait()
	close(results)

	successes := 0
	rejected := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, identity.ErrInvitationCodeInvalid):
			rejected++
		default:
			t.Fatalf("unexpected registration error: %v", err)
		}
	}

	require.Equal(t, 1, successes)
	require.Equal(t, called-1, rejected)
	redeemRepo.mu.Lock()
	require.Equal(t, billing.StatusUsed, redeemRepo.code.Status)
	redeemRepo.mu.Unlock()
}

// tencentCaptchaSettings returns a fresh enabled Tencent credential map for
// CAPTCHA fixtures. The strings provide all local credential inputs expected
// by configuration validation, while actual vendor response selection belongs
// to the verifier stub.
func tencentCaptchaSettings() map[string]string {
	return map[string]string{
		identity.SettingKeyTencentCaptchaEnabled:        "true",
		identity.SettingKeyTencentCaptchaAppID:          "123456789",
		identity.SettingKeyTencentCaptchaAppSecretKey:   "app-secret",
		identity.SettingKeyTencentCaptchaCloudSecretID:  "cloud-secret-id",
		identity.SettingKeyTencentCaptchaCloudSecretKey: "cloud-secret-key",
	}
}

// TestVerifyCaptchaUsesTencentWhenEnabled supplies a complete enabled Tencent
// configuration and successful vendor fixture. Verification succeeds and the
// vendor receives one call for the submitted ticket proof.
func TestVerifyCaptchaUsesTencentWhenEnabled(t *testing.T) {
	verifier := &tencentCaptchaVerifierStub{response: &identity.TencentCaptchaVerifyResponse{CaptchaCode: 1}}
	svc := newAuthServiceForCaptchaTest(tencentCaptchaSettings(), false, nil, verifier)

	err := svc.VerifyCaptcha(context.Background(), identity.CaptchaProof{
		TencentTicket:  "ticket",
		TencentRandstr: "@rand",
	}, "203.0.113.10")

	require.NoError(t, err)
	require.Equal(t, 1, verifier.calls)
}

// TestVerifyCaptchaRejectsDirtyDoubleEnabledSettings enables Tencent and
// Turnstile together with their required settings. Provider-conflict
// rejection occurs before either verifier records a call, protecting the
// single-provider selection rule.
func TestVerifyCaptchaRejectsDirtyDoubleEnabledSettings(t *testing.T) {
	settings := tencentCaptchaSettings()
	settings[identity.SettingKeyTurnstileEnabled] = "true"
	settings[identity.SettingKeyTurnstileSecretKey] = "turnstile-secret"
	turnstileVerifier := &turnstileVerifierSpy{}
	tencentVerifier := &tencentCaptchaVerifierStub{response: &identity.TencentCaptchaVerifyResponse{CaptchaCode: 1}}
	svc := newAuthServiceForCaptchaTest(settings, false, turnstileVerifier, tencentVerifier)

	err := svc.VerifyCaptcha(context.Background(), identity.CaptchaProof{
		TurnstileToken: "turnstile-token",
		TencentTicket:  "ticket",
		TencentRandstr: "@rand",
	}, "203.0.113.10")

	require.ErrorIs(t, err, identity.ErrCaptchaProviderConflict)
	require.Zero(t, turnstileVerifier.called)
	require.Zero(t, tencentVerifier.calls)
}

// TestVerifyCaptchaRequiredModeAcceptsCompleteTencentProvider requires
// CAPTCHA in release mode with complete Tencent settings. A successful
// Tencent response satisfies verification even when no Turnstile capability
// is supplied.
func TestVerifyCaptchaRequiredModeAcceptsCompleteTencentProvider(t *testing.T) {
	verifier := &tencentCaptchaVerifierStub{response: &identity.TencentCaptchaVerifyResponse{CaptchaCode: 1}}
	svc := newAuthServiceForCaptchaTest(tencentCaptchaSettings(), true, nil, verifier)

	err := svc.VerifyCaptcha(context.Background(), identity.CaptchaProof{
		TencentTicket:  "ticket",
		TencentRandstr: "@rand",
	}, "203.0.113.10")

	require.NoError(t, err)
}

// TestVerifyCaptchaForRegisterSkipsDuplicateTencentTicketAfterEmailCode
// enables email verification and passes nonempty code text to the
// CAPTCHA-only registration helper. It succeeds without vendor invocation;
// the test does not verify that code.
func TestVerifyCaptchaForRegisterSkipsDuplicateTencentTicketAfterEmailCode(t *testing.T) {
	settings := tencentCaptchaSettings()
	settings[identity.SettingKeyEmailVerifyEnabled] = "true"
	verifier := &tencentCaptchaVerifierStub{response: &identity.TencentCaptchaVerifyResponse{CaptchaCode: 1}}
	svc := newAuthServiceForCaptchaTest(settings, true, nil, verifier)

	err := svc.VerifyCaptchaForRegister(context.Background(), identity.CaptchaProof{}, "203.0.113.10", "123456")

	require.NoError(t, err)
	require.Zero(t, verifier.calls)
}

// TestVerifyCaptchaFailsClosedWhenProviderSettingsCannotBeRead makes the
// provider settings batch fail before verifier selection. Verification
// returns service-unavailable rather than accepting the supplied empty proof
// under unreadable configuration.
func TestVerifyCaptchaFailsClosedWhenProviderSettingsCannotBeRead(t *testing.T) {
	repo := &captchaSettingsStore{err: errors.New("settings unavailable")}
	svc := newAuthServiceForCaptchaRepoTest(repo, false, &turnstileVerifierSpy{}, &tencentCaptchaVerifierStub{})

	err := svc.VerifyCaptcha(context.Background(), identity.CaptchaProof{}, "203.0.113.10")

	require.ErrorIs(t, err, identity.ErrServiceUnavailable)
}

// TestVerifyCaptchaReadsProviderConfigurationOnce verifies a Tencent proof
// with a counting settings store. The observed path makes one batch read, no
// single-value reads and one vendor invocation.
func TestVerifyCaptchaReadsProviderConfigurationOnce(t *testing.T) {
	repo := &captchaSettingsStore{values: tencentCaptchaSettings()}
	verifier := &tencentCaptchaVerifierStub{response: &identity.TencentCaptchaVerifyResponse{CaptchaCode: 1}}
	svc := newAuthServiceForCaptchaRepoTest(repo, false, &turnstileVerifierSpy{}, verifier)

	err := svc.VerifyCaptcha(context.Background(), identity.CaptchaProof{
		TencentTicket:  "ticket",
		TencentRandstr: "@rand",
	}, "203.0.113.10")

	require.NoError(t, err)
	require.Equal(t, 1, repo.getMultipleCalls)
	require.Zero(t, repo.getValueCalls)
	require.Equal(t, 1, verifier.calls)
}

// TestOptionalCaptchaReportsTheVerifiedSnapshot checks disabled and enabled optional verification under authentication's stricter startup requirement.
// Optional callers receive the actual selected state from one read, while the ordinary authentication entry still rejects a disabled provider.
func TestOptionalCaptchaReportsTheVerifiedSnapshot(t *testing.T) {
	repo := &captchaSettingsStore{values: map[string]string{}}
	verifier := &tencentCaptchaVerifierStub{response: &identity.TencentCaptchaVerifyResponse{CaptchaCode: 1}}
	service := newAuthServiceForCaptchaRepoTest(repo, true, nil, verifier)
	enabled, err := service.VerifyOptionalCaptcha(t.Context(), identity.CaptchaProof{}, "192.0.2.7")
	require.NoError(t, err)
	require.False(t, enabled)
	require.Equal(t, 1, repo.getMultipleCalls)
	require.ErrorIs(t, service.VerifyCaptcha(t.Context(), identity.CaptchaProof{}, "192.0.2.7"), identity.ErrTurnstileNotConfigured)
	repo.values = tencentCaptchaSettings()
	before := repo.getMultipleCalls
	enabled, err = service.VerifyOptionalCaptcha(t.Context(), identity.CaptchaProof{TencentTicket: "ticket", TencentRandstr: "random"}, "192.0.2.7")
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, before+1, repo.getMultipleCalls)
	require.Equal(t, 1, verifier.calls)
	repo.err = errors.New("settings unavailable")
	_, err = service.VerifyOptionalCaptcha(t.Context(), identity.CaptchaProof{}, "192.0.2.7")
	require.ErrorIs(t, err, identity.ErrServiceUnavailable)
}

// TestVerifyCaptchaRejectsEnabledTencentProviderWithIncompleteCredentials
// enables Tencent with only its application ID present. Configuration
// rejection follows one batch read and leaves the vendor invocation count at
// zero.
func TestVerifyCaptchaRejectsEnabledTencentProviderWithIncompleteCredentials(t *testing.T) {
	repo := &captchaSettingsStore{values: map[string]string{
		identity.SettingKeyTencentCaptchaEnabled: "true",
		identity.SettingKeyTencentCaptchaAppID:   "123456789",
	}}
	verifier := &tencentCaptchaVerifierStub{response: &identity.TencentCaptchaVerifyResponse{CaptchaCode: 1}}
	svc := newAuthServiceForCaptchaRepoTest(repo, false, &turnstileVerifierSpy{}, verifier)

	err := svc.VerifyCaptcha(context.Background(), identity.CaptchaProof{
		TencentTicket:  "ticket",
		TencentRandstr: "@rand",
	}, "203.0.113.10")

	require.ErrorIs(t, err, identity.ErrTencentCaptchaNotConfigured)
	require.Equal(t, 1, repo.getMultipleCalls)
	require.Zero(t, verifier.calls)
}

// TestVerifyActionCaptchaIfEnabledVerifiesTencentProof submits Tencent
// ticket/randstr to the optional action helper. It succeeds through one
// vendor call and records the exact proof fields received by that verifier.
func TestVerifyActionCaptchaIfEnabledVerifiesTencentProof(t *testing.T) {
	verifier := &tencentCaptchaVerifierStub{response: &identity.TencentCaptchaVerifyResponse{CaptchaCode: 1}}
	svc := newAuthServiceForCaptchaTest(tencentCaptchaSettings(), false, nil, verifier)

	err := svc.VerifyActionCaptchaIfEnabled(context.Background(), identity.CaptchaProof{
		TencentTicket:  "ticket",
		TencentRandstr: "@rand",
	}, "203.0.113.10")

	require.NoError(t, err)
	require.Equal(t, 1, verifier.calls)
	require.Equal(t, identity.TencentCaptchaProof{Ticket: "ticket", Randstr: "@rand"}, verifier.proof)
}

// TestVerifyActionCaptchaIfEnabledDoesNotExpandTurnstileCoverage enables
// Turnstile alone for the optional action helper. Empty proof succeeds and
// the Turnstile spy remains uncalled, protecting this entry point's
// selected-provider coverage.
func TestVerifyActionCaptchaIfEnabledDoesNotExpandTurnstileCoverage(t *testing.T) {
	settings := map[string]string{
		identity.SettingKeyTurnstileEnabled:   "true",
		identity.SettingKeyTurnstileSecretKey: "turnstile-secret",
	}
	turnstileVerifier := &turnstileVerifierSpy{}
	svc := newAuthServiceForCaptchaTest(settings, false, turnstileVerifier, nil)

	err := svc.VerifyActionCaptchaIfEnabled(context.Background(), identity.CaptchaProof{}, "203.0.113.10")

	require.NoError(t, err)
	require.Zero(t, turnstileVerifier.called)
}

// TestVerifyActionCaptchaIfEnabledFailsClosedOnSettingReadError injects a
// failed runtime settings batch for the optional action helper. It returns
// service-unavailable before treating absent proof as an accepted action.
func TestVerifyActionCaptchaIfEnabledFailsClosedOnSettingReadError(t *testing.T) {
	repo := &captchaSettingsStore{err: errors.New("settings unavailable")}
	svc := newAuthServiceForCaptchaRepoTest(repo, false, &turnstileVerifierSpy{}, &tencentCaptchaVerifierStub{})

	err := svc.VerifyActionCaptchaIfEnabled(context.Background(), identity.CaptchaProof{}, "203.0.113.10")

	require.ErrorIs(t, err, identity.ErrServiceUnavailable)
}

// TestAuthService_Register_Disabled sets the stored registration switch to
// false before ordinary signup. It checks the shared registration-disabled
// result for that disabled policy.
func TestAuthService_Register_Disabled(t *testing.T) {
	repo := &userRepoStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "false",
	}, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrRegDisabled)
}

// TestAuthService_Register_DisabledByDefault omits the authentication
// settings dependency during ordinary signup. The resulting
// registration-disabled error protects the service's default decision when no
// registration switch can be read.
func TestAuthService_Register_DisabledByDefault(t *testing.T) {
	repo := &userRepoStub{}
	service := newAuthService(repo, nil, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrRegDisabled)
}

// TestAuthService_Register_EmailVerifyEnabledButServiceNotConfigured requires
// email verification while omitting its service capability. Submitted code
// text cannot make registration succeed; the request returns the
// service-unavailable sentinel.
func TestAuthService_Register_EmailVerifyEnabledButServiceNotConfigured(t *testing.T) {
	repo := &userRepoStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
		identity.SettingKeyEmailVerifyEnabled:  "true",
	}, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "any-code", "", "", "")
	require.ErrorIs(t, err, identity.ErrServiceUnavailable)
}

// TestAuthService_Register_EmailVerifyRequired provides an email service and
// requires verification, then submits an empty code. It checks the
// email-verification-required result for that missing proof text.
func TestAuthService_Register_EmailVerifyRequired(t *testing.T) {
	repo := &userRepoStub{}
	cache := &emailCacheStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
		identity.SettingKeyEmailVerifyEnabled:  "true",
	}, cache)

	_, _, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrEmailVerifyRequired)
}

// TestAuthService_Register_EmailVerifyInvalid submits a code different from
// the configured verification record. Registration returns the invalid-code
// sentinel and keeps verification context in its returned message.
func TestAuthService_Register_EmailVerifyInvalid(t *testing.T) {
	repo := &userRepoStub{}
	cache := &emailCacheStub{
		data: &identity.VerificationCodeData{Code: "expected", Attempts: 0},
	}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
		identity.SettingKeyEmailVerifyEnabled:  "true",
	}, cache)

	_, _, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "wrong", "", "", "")
	require.ErrorIs(t, err, identity.ErrInvalidVerifyCode)
	require.ErrorContains(t, err, "verify code")
}

// TestAuthService_Register_EmailExists configures the repository
// mailbox-conflict observation before signup. It checks that registration
// returns the shared email-exists rejection for the submitted address.
func TestAuthService_Register_EmailExists(t *testing.T) {
	repo := &userRepoStub{exists: true}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
	}, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrEmailExists)
}

// TestAuthService_Register_CheckEmailError fails the repository
// mailbox-conflict check during signup. Registration maps that read failure
// to service-unavailable, distinguishing unreadable occupancy from an
// available mailbox.
func TestAuthService_Register_CheckEmailError(t *testing.T) {
	repo := &userRepoStub{existsErr: errors.New("db down")}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
	}, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrServiceUnavailable)
}

// TestAuthService_Register_ReservedEmail requests ordinary signup with a
// reserved LinuxDo compatibility mailbox. The service returns email-reserved,
// keeping synthetic provider addresses outside the local email registration
// path.
func TestAuthService_Register_ReservedEmail(t *testing.T) {
	repo := &userRepoStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
	}, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "linuxdo-123@linuxdo-connect.invalid", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrEmailReserved)
}

// TestAuthService_Register_EmailDomainRegistrationLimit enables non-whitelist
// domain quotas with existing occupancy. Registration returns the
// domain-limit sentinel and its matching application reason for another
// account in that domain.
func TestAuthService_Register_EmailDomainRegistrationLimit(t *testing.T) {
	repo := &userRepoStub{domainCounts: map[string]int{"other.com": 1}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist:    `["@example.com","@company.com"]`,
		identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
	}, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "user@other.com", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrEmailDomainRegistrationLimit)
	appErr := apperror.FromError(err)
	require.Equal(t, "EMAIL_DOMAIN_REGISTRATION_LIMIT", appErr.Reason)
}

// TestAuthService_Register_NonWhitelistDomainAllowsFirstAccount enables
// domain quotas for an unoccupied non-whitelist suffix. Signup succeeds and
// records the derived custom.example guard domain for a subdomain mailbox.
func TestAuthService_Register_NonWhitelistDomainAllowsFirstAccount(t *testing.T) {
	repo := &userRepoStub{nextID: 9, domainCounts: map[string]int{"custom.example": 0}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist:    `["@example.com"]`,
		identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
	}, nil)

	_, user, err := service.RegisterWithVerification(context.Background(), "first@sub.custom.example", "password", "", "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(9), user.ID)
	require.Equal(t, []string{"custom.example"}, repo.domainGuardCalls)
}

// TestAuthService_Register_NonWhitelistDomainRejectedWhenQuotaDisabledByDefault
// omits the quota switch while a whitelist excludes the mailbox. Signup
// returns suffix-not-allowed and records neither account creation nor a
// domain guard call.
func TestAuthService_Register_NonWhitelistDomainRejectedWhenQuotaDisabledByDefault(t *testing.T) {
	repo := &userRepoStub{nextID: 9, domainCounts: map[string]int{"custom.example": 0}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:              "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist: `["@example.com"]`,
	}, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "first@custom.example", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
	require.Equal(t, "EMAIL_SUFFIX_NOT_ALLOWED", apperror.FromError(err).Reason)
	require.Empty(t, repo.created)
	require.Empty(t, repo.domainGuardCalls)
}

// TestAuthService_Register_NonWhitelistDomainRejectedWhenQuotaExplicitlyDisabled
// sets the quota switch to false for a non-whitelist mailbox. Registration
// returns suffix-not-allowed even when the configured domain has no existing
// account.
func TestAuthService_Register_NonWhitelistDomainRejectedWhenQuotaExplicitlyDisabled(t *testing.T) {
	repo := &userRepoStub{domainCounts: map[string]int{"custom.example": 0}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist:    `["@example.com"]`,
		identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "false",
	}, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "first@custom.example", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
}

// TestAuthService_Register_EmailSuffixAllowed uses a mailbox matching an
// enabled registration whitelist. Signup succeeds and returns the account ID
// assigned by the creation fixture for that request.
func TestAuthService_Register_EmailSuffixAllowed(t *testing.T) {
	repo := &userRepoStub{nextID: 8}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:              "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist: `["example.com"]`,
	}, nil)

	_, user, err := service.RegisterWithVerification(context.Background(), "user@example.com", "password", "", "", "", "")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, int64(8), user.ID)
}

// TestAuthService_SendVerifyCode_EmailDomainRegistrationLimit requests a
// verification message for an occupied non-whitelist domain with quotas
// enabled. Domain-limit rejection and its reason occur before the missing
// email service is needed.
func TestAuthService_SendVerifyCode_EmailDomainRegistrationLimit(t *testing.T) {
	repo := &userRepoStub{domainCounts: map[string]int{"other.com": 1}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist:    `["@example.com","@company.com"]`,
		identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
	}, nil)

	err := service.SendVerifyCode(context.Background(), "user@other.com")
	require.ErrorIs(t, err, identity.ErrEmailDomainRegistrationLimit)
	appErr := apperror.FromError(err)
	require.Equal(t, "EMAIL_DOMAIN_REGISTRATION_LIMIT", appErr.Reason)
}

// TestAuthService_SendVerifyCode_NonWhitelistDomainRejectedWhenQuotaDisabled
// requests synchronous verification delivery for an excluded suffix while
// quotas are omitted. The service returns suffix-not-allowed before
// attempting the unavailable email capability.
func TestAuthService_SendVerifyCode_NonWhitelistDomainRejectedWhenQuotaDisabled(t *testing.T) {
	repo := &userRepoStub{domainCounts: map[string]int{"custom.example": 0}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:              "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist: `["@example.com"]`,
	}, nil)

	err := service.SendVerifyCode(context.Background(), "user@custom.example")
	require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
}

// TestAuthService_SendVerifyCodeAsync_NonWhitelistDomainRejectedWhenQuotaDisabled
// requests queued verification delivery for an excluded suffix while quotas
// are omitted. The service returns suffix-not-allowed before attempting its
// absent queue capability.
func TestAuthService_SendVerifyCodeAsync_NonWhitelistDomainRejectedWhenQuotaDisabled(t *testing.T) {
	repo := &userRepoStub{domainCounts: map[string]int{"custom.example": 0}}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:              "true",
		identity.SettingKeyRegistrationEmailSuffixWhitelist: `["@example.com"]`,
	}, nil)

	_, err := service.SendVerifyCodeAsync(context.Background(), "user@custom.example")
	require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
}

// TestAuthService_Register_CreateError makes account creation fail after
// registration admission and mailbox checks. The resulting
// service-unavailable sentinel protects the public error selection for a
// generic persistence failure.
func TestAuthService_Register_CreateError(t *testing.T) {
	repo := &userRepoStub{createErr: errors.New("create failed")}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
	}, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrServiceUnavailable)
}

// TestAuthService_Register_CreateEmailExistsRace allows the mailbox precheck
// but injects email-exists at creation. The service returns that conflict
// sentinel, distinguishing a late uniqueness conflict from generic
// persistence unavailability.
func TestAuthService_Register_CreateEmailExistsRace(t *testing.T) {
	repo := &userRepoStub{createErr: identity.ErrEmailExists}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
	}, nil)

	_, _, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrEmailExists)
}

// TestAuthService_Register_Success registers with email-source grants
// disabled and explicit startup grant values. Returned token/account fields,
// stored password verification and one creation record protect the ordinary
// signup result.
func TestAuthService_Register_Success(t *testing.T) {
	repo := &userRepoStub{nextID: 5}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup: "false",
	}, nil)

	token, user, err := service.RegisterWithVerification(context.Background(), "user@test.com", "password", "", "", "", "")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.NotNil(t, user)
	require.Equal(t, int64(5), user.ID)
	require.Equal(t, "user@test.com", user.Email)
	require.Equal(t, identity.RoleUser, user.Role)
	require.Equal(t, billing.StatusActive, user.Status)
	require.Equal(t, 3.5, user.Balance)
	require.Equal(t, 2, user.Concurrency)
	require.Len(t, repo.created, 1)
	require.True(t, user.CheckPassword("password"))
}

// TestAuthService_Register_AssignsDefaultSubscriptions registers with two
// global plans and disabled email-source grants. It checks the first
// assignment's account ID and both plan IDs in configured order.
func TestAuthService_Register_AssignsDefaultSubscriptions(t *testing.T) {
	repo := &userRepoStub{nextID: 42}
	assigner := &defaultSubscriptionAssignerStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		billing.SettingKeyDefaultSubscriptions:                 `[{"plan_id":11},{"plan_id":12}]`,
		identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup: "false",
	}, nil)
	service.DefaultSubscriptions = assigner

	_, user, err := service.RegisterWithVerification(context.Background(), "default-sub@test.com", "password", "", "", "", "")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Len(t, assigner.calls, 2)
	require.Equal(t, int64(42), assigner.calls[0].UserID)
	require.Equal(t, int64(11), assigner.calls[0].PlanID)
	require.Equal(t, int64(12), assigner.calls[1].PlanID)
}

// TestAuthService_Register_UsesEmailAuthSourceDefaultsWhenGrantEnabled
// enables email-source signup grants alongside differing global plans.
// Returned balance/concurrency and the single assignment record use that
// source's selected values and plan.
func TestAuthService_Register_UsesEmailAuthSourceDefaultsWhenGrantEnabled(t *testing.T) {
	repo := &userRepoStub{nextID: 52}
	assigner := &defaultSubscriptionAssignerStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		billing.SettingKeyDefaultSubscriptions:                 `[{"plan_id":91}]`,
		identity.SettingKeyAuthSourceDefaultEmailBalance:       "12.5",
		identity.SettingKeyAuthSourceDefaultEmailConcurrency:   "7",
		identity.SettingKeyAuthSourceDefaultEmailSubscriptions: `[{"plan_id":11}]`,
		identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup: "true",
	}, nil)
	service.DefaultSubscriptions = assigner

	_, user, err := service.RegisterWithVerification(context.Background(), "email-defaults@test.com", "password", "", "", "", "")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, 12.5, user.Balance)
	require.Equal(t, 7, user.Concurrency)
	require.Len(t, assigner.calls, 1)
	require.Equal(t, int64(11), assigner.calls[0].PlanID)
}

// TestAuthService_Register_GrantOnSignupFalseFallsBackToGlobalDefaults
// supplies provider grant values with the signup flag disabled. Returned
// balance/concurrency and the assigned plan follow global defaults despite
// the stored provider values.
func TestAuthService_Register_GrantOnSignupFalseFallsBackToGlobalDefaults(t *testing.T) {
	repo := &userRepoStub{nextID: 53}
	assigner := &defaultSubscriptionAssignerStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		billing.SettingKeyDefaultSubscriptions:                 `[{"plan_id":31}]`,
		identity.SettingKeyAuthSourceDefaultEmailBalance:       "99",
		identity.SettingKeyAuthSourceDefaultEmailConcurrency:   "88",
		identity.SettingKeyAuthSourceDefaultEmailSubscriptions: `[{"plan_id":32}]`,
		identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup: "false",
	}, nil)
	service.DefaultSubscriptions = assigner

	_, user, err := service.RegisterWithVerification(context.Background(), "email-global@test.com", "password", "", "", "", "")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, 3.5, user.Balance)
	require.Equal(t, 2, user.Concurrency)
	require.Len(t, assigner.calls, 1)
	require.Equal(t, int64(31), assigner.calls[0].PlanID)
}

// TestAuthService_Register_GrantOnSignupMergesSourceOverridesWithGlobalDefaults
// enables provider grants with an empty source subscription list. Source
// balance/concurrency apply while the nonempty global plan remains selected
// for the created account.
func TestAuthService_Register_GrantOnSignupMergesSourceOverridesWithGlobalDefaults(t *testing.T) {
	repo := &userRepoStub{nextID: 54}
	assigner := &defaultSubscriptionAssignerStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                 "true",
		billing.SettingKeyDefaultSubscriptions:                 `[{"plan_id":31}]`,
		identity.SettingKeyAuthSourceDefaultEmailBalance:       "9.5",
		identity.SettingKeyAuthSourceDefaultEmailConcurrency:   "5",
		identity.SettingKeyAuthSourceDefaultEmailSubscriptions: `[]`,
		identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup: "true",
	}, nil)
	service.DefaultSubscriptions = assigner

	_, user, err := service.RegisterWithVerification(context.Background(), "email-merged@test.com", "password", "", "", "", "")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, 9.5, user.Balance)
	require.Equal(t, 5, user.Concurrency)
	require.Len(t, assigner.calls, 1)
	require.Equal(t, int64(31), assigner.calls[0].PlanID)
}

// TestAuthService_LoginOrRegisterOAuthWithTokenPair_UsesLinuxDoAuthSourceDefaultsOnSignup
// creates a LinuxDo-compatible account with enabled source grants and a
// refresh fixture. Returned token/account and assignment records use the
// source balance, concurrency and subscription plan.
func TestAuthService_LoginOrRegisterOAuthWithTokenPair_UsesLinuxDoAuthSourceDefaultsOnSignup(t *testing.T) {
	repo := &userRepoStub{nextID: 61}
	assigner := &defaultSubscriptionAssignerStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                   "true",
		billing.SettingKeyDefaultSubscriptions:                   `[{"plan_id":81}]`,
		identity.SettingKeyAuthSourceDefaultLinuxDoBalance:       "21.75",
		identity.SettingKeyAuthSourceDefaultLinuxDoConcurrency:   "9",
		identity.SettingKeyAuthSourceDefaultLinuxDoSubscriptions: `[{"plan_id":22}]`,
		identity.SettingKeyAuthSourceDefaultLinuxDoGrantOnSignup: "true",
	}, nil)
	service.DefaultSubscriptions = assigner
	service.RefreshTokens = &refreshTokenCacheStub{}
	rebuildSessionForTest(service)

	tokenPair, user, err := service.LoginOrRegisterOAuthWithTokenPair(context.Background(), "linuxdo-123@linuxdo-connect.invalid", "linuxdo_user", "", "", "linuxdo")
	require.NoError(t, err)
	require.NotNil(t, tokenPair)
	require.NotNil(t, user)
	require.Equal(t, int64(61), user.ID)
	require.Equal(t, 21.75, user.Balance)
	require.Equal(t, 9, user.Concurrency)
	require.Len(t, repo.created, 1)
	require.Len(t, assigner.calls, 1)
	require.Equal(t, int64(22), assigner.calls[0].PlanID)
}

// TestAuthService_LoginOrRegisterOAuthWithTokenPair_ExistingUserDoesNotGrantAgain
// logs in a configured active LinuxDo account with source grants enabled. The
// token pair succeeds while existing funds/concurrency remain and
// creation/subscription histories stay empty.
func TestAuthService_LoginOrRegisterOAuthWithTokenPair_ExistingUserDoesNotGrantAgain(t *testing.T) {
	existing := &identity.User{
		ID:           88,
		Email:        "linuxdo-123@linuxdo-connect.invalid",
		Username:     "existing-linuxdo",
		Role:         identity.RoleUser,
		Status:       billing.StatusActive,
		Balance:      4,
		Concurrency:  1,
		TokenVersion: 2,
	}
	repo := &userRepoStub{user: existing}
	assigner := &defaultSubscriptionAssignerStub{}
	service := newAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:                   "true",
		identity.SettingKeyAuthSourceDefaultLinuxDoBalance:       "21.75",
		identity.SettingKeyAuthSourceDefaultLinuxDoConcurrency:   "9",
		identity.SettingKeyAuthSourceDefaultLinuxDoSubscriptions: `[{"plan_id":22}]`,
		identity.SettingKeyAuthSourceDefaultLinuxDoGrantOnSignup: "true",
	}, nil)
	service.DefaultSubscriptions = assigner
	service.RefreshTokens = &refreshTokenCacheStub{}
	rebuildSessionForTest(service)

	tokenPair, user, err := service.LoginOrRegisterOAuthWithTokenPair(context.Background(), existing.Email, "linuxdo_user", "", "", "linuxdo")
	require.NoError(t, err)
	require.NotNil(t, tokenPair)
	require.Equal(t, existing.ID, user.ID)
	require.Equal(t, 4.0, user.Balance)
	require.Equal(t, 1, user.Concurrency)
	require.Empty(t, repo.created)
	require.Empty(t, assigner.calls)
}

// newAuthServiceWithDingTalkCfg assembles database-free authentication
// settings from supplied DingTalk startup values and shared runtime strings.
// It omits user storage because its callers exercise the registration-bypass
// predicate, with provider validation delegated to production readers.
func newAuthServiceWithDingTalkCfg(settings map[string]string, dtCfg config.DingTalkOAuthConfig) *identity.AuthService {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			JWT: config.JWTConfig{
				Secret:                "test-secret",
				AccessTokenTTLMinutes: 60,
			},
			OAuth: config.OAuthProvidersConfig{DingTalk: dtCfg},
		},
		UserDefaults: config.UserDefaultsConfig{
			Balance:     3.5,
			Concurrency: 2,
		},
	}
	settingService := newAuthSettingsFixture(&settingRepoStub{values: settings}, cfg)
	return identitytestkit.Auth(nil, &identity.AuthDependencies{Options: identitytestkit.AuthOptions(cfg), Settings: authSettingsPort(settingService)})
}

// minDingTalkURLs returns value-only endpoint/client/application settings
// accepted by local DingTalk configuration validation. Enablement and policy
// remain zero for cases to set independently; the example addresses perform
// no upstream identity verification.
func minDingTalkURLs() config.DingTalkOAuthConfig {
	return config.DingTalkOAuthConfig{
		ClientID:            "test-client",
		ClientSecret:        "test-secret",
		AuthorizeURL:        "https://example.com/oauth2/auth",
		TokenURL:            "https://example.com/oauth2/token",
		UserInfoURL:         "https://example.com/oauth2/userinfo",
		RedirectURL:         "https://example.com/callback",
		FrontendRedirectURL: "https://example.com/auth/callback",
		DingTalkAppKind:     "internal_app",
		AppType:             "internal",
	}
}

// TestCanBypassRegistrationDisabledForOAuth varies source, enabled state,
// bypass switch and organization policy against valid endpoint defaults. The
// predicate accepts only the represented enabled
// DingTalk/internal_only/bypass combination.
func TestCanBypassRegistrationDisabledForOAuth(t *testing.T) {
	cases := []struct {
		name         string // descriptive bypass policy case
		signupSource string // requested authentication source label
		// settings and dtCfg supply saved runtime strings and startup provider
		// values respectively. The settings fixture retains the map while
		// OAuthSettings snapshots the startup values before the predicate reads its
		// effective configuration.
		settings map[string]string          // shared runtime provider settings
		dtCfg    config.DingTalkOAuthConfig // startup DingTalk configuration values
		want     bool                       // expected registration bypass observation
	}{
		{
			name:         "non-dingtalk source → false",
			signupSource: "linuxdo",
			settings:     map[string]string{},
			dtCfg:        minDingTalkURLs(),
			want:         false,
		},
		{
			name:         "dingtalk but cfg.Enabled=false → false",
			signupSource: "dingtalk",
			settings: map[string]string{
				identity.SettingKeyDingTalkConnectEnabled:               "false",
				identity.SettingKeyDingTalkConnectBypassRegistration:    "true",
				identity.SettingKeyDingTalkConnectCorpRestrictionPolicy: "internal_only",
			},
			dtCfg: minDingTalkURLs(),
			want:  false,
		},
		{
			name:         "dingtalk enabled but BypassRegistration=false → false",
			signupSource: "dingtalk",
			settings: map[string]string{
				identity.SettingKeyDingTalkConnectEnabled:               "true",
				identity.SettingKeyDingTalkConnectBypassRegistration:    "false",
				identity.SettingKeyDingTalkConnectCorpRestrictionPolicy: "internal_only",
			},
			dtCfg: minDingTalkURLs(),
			want:  false,
		},
		{
			name:         "dingtalk enabled + bypass=true but policy=none → false",
			signupSource: "dingtalk",
			settings: map[string]string{
				identity.SettingKeyDingTalkConnectEnabled:               "true",
				identity.SettingKeyDingTalkConnectBypassRegistration:    "true",
				identity.SettingKeyDingTalkConnectCorpRestrictionPolicy: "none",
			},
			dtCfg: minDingTalkURLs(),
			want:  false,
		},
		{
			name:         "dingtalk enabled + bypass=true + policy=internal_only → true",
			signupSource: "dingtalk",
			settings: map[string]string{
				identity.SettingKeyDingTalkConnectEnabled:               "true",
				identity.SettingKeyDingTalkConnectBypassRegistration:    "true",
				identity.SettingKeyDingTalkConnectCorpRestrictionPolicy: "internal_only",
			},
			dtCfg: minDingTalkURLs(),
			want:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newAuthServiceWithDingTalkCfg(tc.settings, tc.dtCfg)
			got := svc.AuthCanBypassRegistrationDisabledForOAuth(context.Background(), tc.signupSource)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestAuthService_VerifyTurnstileForRegister_SkipWhenEmailVerifyCodeProvided
// requires release CAPTCHA but supplies code text while email verification is
// enabled. The CAPTCHA helper succeeds with no Turnstile call; this test does
// not validate the code.
func TestAuthService_VerifyTurnstileForRegister_SkipWhenEmailVerifyCodeProvided(t *testing.T) {
	verifier := &turnstileVerifierSpy{}
	service := newAuthServiceForRegisterTurnstileTest(map[string]string{
		identity.SettingKeyEmailVerifyEnabled:  "true",
		identity.SettingKeyTurnstileEnabled:    "true",
		identity.SettingKeyTurnstileSecretKey:  "secret",
		identity.SettingKeyRegistrationEnabled: "true",
	}, verifier)

	err := service.VerifyCaptchaForRegister(context.Background(), identity.CaptchaProof{TurnstileToken: ""}, "127.0.0.1", "123456")
	require.NoError(t, err)
	require.Equal(t, 0, verifier.called)
}

// TestAuthService_VerifyTurnstileForRegister_RequireWhenVerifyCodeMissing
// enables email verification and Turnstile but supplies neither code nor
// token. The registration CAPTCHA helper returns the Turnstile
// verification-failed sentinel.
func TestAuthService_VerifyTurnstileForRegister_RequireWhenVerifyCodeMissing(t *testing.T) {
	verifier := &turnstileVerifierSpy{}
	service := newAuthServiceForRegisterTurnstileTest(map[string]string{
		identity.SettingKeyEmailVerifyEnabled: "true",
		identity.SettingKeyTurnstileEnabled:   "true",
		identity.SettingKeyTurnstileSecretKey: "secret",
	}, verifier)

	err := service.VerifyCaptchaForRegister(context.Background(), identity.CaptchaProof{TurnstileToken: ""}, "127.0.0.1", "")
	require.ErrorIs(t, err, identity.ErrTurnstileVerificationFailed)
}

// TestAuthService_VerifyTurnstileForRegister_NoSkipWhenEmailVerifyDisabled
// provides code text while email verification is disabled and Turnstile
// enabled. The helper invokes Turnstile once with the submitted token instead
// of taking the code-presence skip.
func TestAuthService_VerifyTurnstileForRegister_NoSkipWhenEmailVerifyDisabled(t *testing.T) {
	verifier := &turnstileVerifierSpy{}
	service := newAuthServiceForRegisterTurnstileTest(map[string]string{
		identity.SettingKeyEmailVerifyEnabled: "false",
		identity.SettingKeyTurnstileEnabled:   "true",
		identity.SettingKeyTurnstileSecretKey: "secret",
	}, verifier)

	err := service.VerifyCaptchaForRegister(context.Background(), identity.CaptchaProof{TurnstileToken: "turnstile-token"}, "127.0.0.1", "123456")
	require.NoError(t, err)
	require.Equal(t, 1, verifier.called)
	require.Equal(t, "turnstile-token", verifier.lastToken)
}

// newAuthServiceForCaptchaRepoTest assembles release-mode CAPTCHA services
// around one runtime settings reader and configured verifiers. It supplies
// logging observers and the required flag, while account/session storage is
// absent because callers exercise CAPTCHA entry points.
func newAuthServiceForCaptchaRepoTest(repo *captchaSettingsStore, required bool, turnstileVerifier identity.TurnstileVerifier, tencentVerifier identity.TencentCaptchaVerifier) *identity.AuthService {
	runtime := identity.NewRuntimeSettings(repo, settingscore.ErrSettingNotFound)
	turnstile := identity.NewTurnstileService(runtime, turnstileVerifier)
	turnstile.SetObserver(logging.LegacyPrintf)
	tencent := identity.NewTencentCaptchaService(runtime, tencentVerifier)
	tencent.SetObserver(logging.LegacyPrintf)
	return identity.NewAuthService(&identity.AuthDependencies{Options: captchaAuthOptions(required), Settings: captchaAuthSettings{runtime: runtime}, Turnstile: turnstile, Tencent: tencent, Observer: identity.Observer{Log: logging.LegacyPrintf}}, nil)
}

// newAuthServiceForCaptchaTest builds CAPTCHA authentication over the
// supplied shared values map. A nil verifier additionally removes its service
// capability after construction, so tests can distinguish absent services
// from configured verifier outcomes.
func newAuthServiceForCaptchaTest(values map[string]string, required bool, turnstileVerifier identity.TurnstileVerifier, tencentVerifier identity.TencentCaptchaVerifier) *identity.AuthService {
	svc := newAuthServiceForCaptchaRepoTest(&captchaSettingsStore{values: values}, required, turnstileVerifier, tencentVerifier)
	if turnstileVerifier == nil {
		svc.Turnstile = nil
	}
	if tencentVerifier == nil {
		svc.Tencent = nil
	}
	return svc
}

// newAuthServiceForRegisterTurnstileTest builds mandatory release CAPTCHA
// with the supplied Turnstile verifier and no Tencent capability. It
// delegates shared settings assembly, allowing code-presence tests to
// exercise registration-specific CAPTCHA selection.
func newAuthServiceForRegisterTurnstileTest(values map[string]string, verifier identity.TurnstileVerifier) *identity.AuthService {
	return newAuthServiceForCaptchaTest(values, true, verifier, nil)
}

// newEmailNormalizationAuthService assembles database-free signup with fixed
// JWT/grant defaults and shared normalization settings. The supplied
// repository defines exact/alias conflict observations; testkit binds only
// the optional repository capabilities it implements.
func newEmailNormalizationAuthService(repo identity.UserRepository, settings map[string]string) *identity.AuthService {
	cfg := &config.Config{
		Auth: config.AuthConfig{JWT: config.JWTConfig{
			Secret:                "test-secret",
			AccessTokenTTLMinutes: 60,
		}},
		UserDefaults: config.UserDefaultsConfig{
			Balance:     3.5,
			Concurrency: 2,
		},
	}

	return identitytestkit.Auth(
		nil, &identity.AuthDependencies{Users: repo, Options: identitytestkit.AuthOptions(cfg), Settings: authSettingsPort(newAuthSettingsFixture(&settingRepoStub{values: settings}, cfg))},
	)
}

// TestAuthService_Register_UsesNormalizedEmailLookupWhenEnabled enables
// mailbox normalization with an alias conflict in the fixture. Signup checks
// the original address, then the normalized Gmail alias, and rejects before
// creation.
func TestAuthService_Register_UsesNormalizedEmailLookupWhenEnabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{existsByNormalized: true}
	svc := newEmailNormalizationAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled:            "true",
		identity.SettingKeyRegistrationEmailNormalization: "true",
	})

	_, _, err := svc.RegisterWithVerification(context.Background(), "Y.o.u.r.N.a.m.e+abc@googlemail.com.", "password", "", "", "", "")
	require.ErrorIs(t, err, identity.ErrEmailExists)
	require.Equal(t, []string{"Y.o.u.r.N.a.m.e+abc@googlemail.com."}, repo.existsByEmailCalls)
	require.Equal(t, []string{"yourname@gmail.com"}, repo.existsByNormalizedCalls)
	require.Empty(t, repo.createCalls)
}

// TestAuthService_Register_SkipsNormalizedLookupWhenDisabled omits the
// normalization switch for a mixed-case mailbox with a plus suffix. Signup
// succeeds after the exact lookup, and the normalized lookup history stays
// empty.
func TestAuthService_Register_SkipsNormalizedLookupWhenDisabled(t *testing.T) {
	repo := &emailNormalizationRepoStub{}
	svc := newEmailNormalizationAuthService(repo, map[string]string{
		identity.SettingKeyRegistrationEnabled: "true",
	})

	_, user, err := svc.RegisterWithVerification(context.Background(), "Y.o.u.r.N.a.m.e+abc@example.com", "password", "", "", "", "")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, []string{"Y.o.u.r.N.a.m.e+abc@example.com"}, repo.existsByEmailCalls)
	require.Empty(t, repo.existsByNormalizedCalls)
}
