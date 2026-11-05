package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"log/slog"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/sync/singleflight"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity/authconfig"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// user_service.go manages account profiles, provider identity summaries,
// notification recipients, and activity observations.
// Account profile changes can affect separate avatar records, selected user
// columns, sign-in relationships and authentication snapshots. The profile
// helper records a field mask and passes the runner's context to its selected
// writes before concurrency-related invalidation. Stores determine their actual
// transaction participation. Identity summaries preserve legacy email display
// and select current provider records
// before runtime switches remove unsupported unbound actions.
//
// Optional activity touches use account observations, process-local deadlines
// and keyed singleflight to limit writes. Notification-address verification has
// a different lifetime: mail is requested before credential storage, and its
// counter update follows storage. Those effects and whole-list recipient edits
// can fail independently, so caller results distinguish persistence from
// diagnostic-only cache or observation work.

const (
	// Recipient count and avatar byte budgets govern profile persistence;
	// inline images above the target undergo the ordered JPEG candidate search.
	ProfileMaxNotifyEmails      = 3 // Maximum number of notification emails per user
	ProfileMaxInlineAvatarBytes = 100 * 1024
	ProfileTargetAvatarBytes    = 20 * 1024

	// These values bound observed notification-code requests across recipient
	// addresses for one user and set the refreshed counter window after successful storage.
	ProfileNotifyCodeUserRateLimit  = 5
	ProfileNotifyCodeUserRateWindow = 10 * time.Minute

	// The binding destination is used for blank redirects. Activity-touch intervals
	// govern process-local successful-write suppression and the shorter failure retry window.
	ProfileDefaultUserIdentityRedirect = "/settings/profile"
	ProfileUserLastActiveMinTouch      = 10 * time.Minute
	ProfileUserLastActiveFailBackoff   = 30 * time.Second

	// These translation keys select profile explanations for managed email and
	// allowed or blocked third-party unbinding after the remaining login-method check.
	ProfileUserIdentityNoteEmailManagedByBinding   = "profile.authBindings.notes.emailManagedByBinding"
	ProfileUserIdentityNoteCanUnbind               = "profile.authBindings.notes.canUnbind"
	ProfileUserIdentityNoteBindAnotherBeforeUnbind = "profile.authBindings.notes.bindAnotherBeforeUnbind"

	// These persistent keys select explicit profile bind availability and site
	// branding; their interpretation leaves missing runtime switches to owning provider defaults.
	SettingKeyDingTalkConnectEnabled     = "dingtalk_connect_enabled"
	SettingKeyLinuxDoConnectEnabled      = "linuxdo_connect_enabled"
	SettingKeyOIDCConnectEnabled         = "oidc_connect_enabled"
	SettingKeySiteName                   = "site_name"
	SettingKeyWeChatConnectEnabled       = "wechat_connect_enabled"
	SettingKeyWeChatConnectMPEnabled     = "wechat_connect_mp_enabled"
	SettingKeyWeChatConnectMobileEnabled = "wechat_connect_mobile_enabled"
	SettingKeyWeChatConnectMode          = "wechat_connect_mode"
	SettingKeyWeChatConnectOpenEnabled   = "wechat_connect_open_enabled"
)

var (
	// Scale and quality candidates are tried in their listed order. Profile
	// compression returns the first JPEG meeting its configured stored byte budget.
	// These exported slices are read directly; callers keep their contents stable
	// while compression runs or exclude concurrent reads when configuring them.
	ProfileAvatarScaleSteps   = []float64{1, 0.92, 0.84, 0.76, 0.68, 0.6, 0.52, 0.44, 0.36}
	ProfileAvatarQualitySteps = []int{88, 80, 72, 64, 56, 48, 40, 32}

	// These errors report profile authorization, recipient limits, avatar input
	// failures and identity controls to callers before or after their selected persistence work.
	ErrBalanceNegative                  = billing.ErrBalanceNegative
	ErrInsufficientPerms                = infraerrors.Forbidden("INSUFFICIENT_PERMISSIONS", "insufficient permissions")
	ErrNotifyCodeUserRateLimit          = infraerrors.TooManyRequests("NOTIFY_CODE_USER_RATE_LIMIT", "too many verification codes requested, please try again later")
	ErrAvatarInvalid                    = infraerrors.BadRequest("AVATAR_INVALID", "avatar must be a valid image data URL or http(s) URL")
	ErrAvatarTooLarge                   = infraerrors.BadRequest("AVATAR_TOO_LARGE", "avatar image must be 100KB or smaller")
	ErrAvatarNotImage                   = infraerrors.BadRequest("AVATAR_NOT_IMAGE", "avatar content must be an image")
	ErrProfileEmailChangeForbidden      = infraerrors.BadRequest("EMAIL_PROFILE_UPDATE_FORBIDDEN", "email must be changed through verified email binding")
	ErrIdentityProviderInvalid          = infraerrors.BadRequest("IDENTITY_PROVIDER_INVALID", "identity provider is invalid")
	ErrIdentityRedirectInvalid          = infraerrors.BadRequest("IDENTITY_REDIRECT_INVALID", "identity redirect path is invalid")
	ErrUserAPIKeyLimitInvalid           = infraerrors.BadRequest("INVALID_API_KEY_LIMIT", fmt.Sprintf("api key limit must be between 0 and %d", MaxUserAPIKeyLimit))
	ErrBalanceRechargeMultiplierInvalid = infraerrors.BadRequest("INVALID_BALANCE_RECHARGE_MULTIPLIER", "balance recharge multiplier must be a finite number greater than 0")
	ErrIdentityUnbindLastMethod         = infraerrors.Conflict(
		"IDENTITY_UNBIND_LAST_METHOD",
		"bind another sign-in method before unbinding this provider",
	)
)

// ProfileUserProfileIdentityTxRunner supplies a persistence context for the profile update callback.
type ProfileUserProfileIdentityTxRunner interface {
	// WithUserProfileIdentityTx invokes the profile callback with a repository or
	// context-carried transaction. Callback failures remain errors; a successful
	// return does not establish every store's participation or commit an externally
	// owned transaction reused by the PostgreSQL runner.
	WithUserProfileIdentityTx(ctx context.Context, fn func(txCtx context.Context) error) error
}

// UserService manages user profiles, credentials, and account updates.
// Repository return values and transaction participation determine when writes
// become durable; invalidation follows this service's successful calls unless
// the deletion path deliberately publishes before its write.
type UserService struct {
	operationClock // selected per-operation time source
	// runBackground schedules post-write balance-cache cleanup. The constructor
	// substitutes synchronous execution when nil; callback acceptance remains runner-owned.
	runBackground func(string, func()) bool
	userRepo      UserRepository  // account and profile persistence
	settingRepo   ProfileSettings // runtime profile policy reader
	// authCacheInvalidator receives account changes affecting authentication.
	// Nil skips publication, and callers retain the shared implementation while updates run.
	authCacheInvalidator UserAuthInvalidator
	// billingCache is invalidated after successful balance persistence under the
	// background runner. Nil disables that cleanup; its errors remain diagnostic outcomes.
	billingCache UserBalanceCache
	// lastActiveTouchL1 stores next-allowed timestamps by user ID, and
	// lastActiveTouchSF shares one in-progress callback for that user's string key.
	// The callback rechecks L1 before its write, then publishes normal or failure delay.
	lastActiveTouchL1 sync.Map           // user-keyed next touch deadlines
	lastActiveTouchSF singleflight.Group // user-keyed activity write coordination
}

// ProfileSettings reads runtime settings used by the profile page.
type ProfileSettings interface {
	// GetMultiple reads the supplied profile setting keys into one map. Missing
	// keys stay absent, and ProfileApplyExplicitProviderAvailability retains its
	// current summaries when this batch read fails.
	GetMultiple(context.Context, []string) (map[string]string, error)
	// GetValue reads one profile setting as text. SendNotifyVerifyEmail uses
	// nonempty readable branding and retains its built-in site name after an empty
	// result or read failure.
	GetValue(context.Context, string) (string, error)
}

type (
	// UserAuthInvalidator publishes account changes to authentication snapshot consumers.
	UserAuthInvalidator interface {
		// InvalidateAuthCacheByUserID invalidates snapshots associated with the account.
		// Callers publish after successful updates except deletion, which invalidates
		// before requesting its repository delete and can then return a persistence error.
		InvalidateAuthCacheByUserID(context.Context, int64)
	}
	// UserBalanceCache removes saved balance observations after account funding changes.
	UserBalanceCache interface {
		// InvalidateUserBalance removes cached funding data for the selected user.
		// Profile balance updates invoke it after persistence under a separate
		// timeout; failures remain errors for that callback to diagnose.
		InvalidateUserBalance(context.Context, int64) error
	}
)

// NotifyVerificationNotice contains the recipient and verification code prepared for delivery.
type NotifyVerificationNotice struct {
	// UserID supplies recipient-account locale lookup to the app mailer; Locale
	// is negotiated against that fallback before the notification template is rendered.
	UserID int64 // recipient local account identifier
	// Email names the verification recipient, Code carries its prepared challenge,
	// Locale retains the selected language hint and SiteName supplies current branding.
	// The sender consumes this notice after identity decides send ordering.
	// Email: recipient notification email. Code: prepared verification challenge text.
	// Locale: selected notification language hint. SiteName: selected message branding name.
	Email, Code, Locale, SiteName string
}

// NotifyVerificationSender prepares codes and sends identity-selected notification-address verification notices.
type NotifyVerificationSender interface {
	// GenerateVerifyCode returns the challenge text used by notification-address
	// verification. SendNotifyEmailCode calls it before mail or cache writes,
	// and generation failure stops both later effects for that request.
	GenerateVerifyCode() (string, error)
	// SendNotifyVerification requests delivery of the prepared notice. The caller
	// saves this notification-address credential after a successful result, so
	// sender errors stop its cache and counter writes for this request.
	SendNotifyVerification(context.Context, NotifyVerificationNotice) error
}

// IsValidUserAPIKeyLimit reports whether the count fits the supported database
// integer range.
func IsValidUserAPIKeyLimit(limit int) bool {
	return limit >= 0 && limit <= MaxUserAPIKeyLimit
}

// NewUserService retains profile stores, invalidators and the selected
// operation clock. A nil background runner executes callbacks synchronously;
// otherwise its scheduling and completion behavior belong to the supplied
// runner, which account updates may use after persistence. The first clock
// entry is used; an absent or nil first entry selects time.Now.
func NewUserService(users UserRepository, settings ProfileSettings, auth UserAuthInvalidator, balance UserBalanceCache, background func(string, func()) bool, clocks ...func() time.Time) *UserService {
	if background == nil {
		background = func(_ string, fn func()) bool { fn(); return true }
	}
	return &UserService{operationClock: clockFromOptional(clocks), userRepo: users, settingRepo: settings, authCacheInvalidator: auth, billingCache: balance, runBackground: background}
}

// GetFirstAdmin reads the repository's first current administrator for
// management-key authentication. Repository errors are wrapped for the caller;
// this read does not itself validate a submitted management credential or apply
// step-up checks.
func (s *UserService) GetFirstAdmin(ctx context.Context) (*User, error) {
	admin, err := s.userRepo.GetFirstAdmin(ctx)
	if err != nil {
		return nil, fmt.Errorf("get first admin: %w", err)
	}
	return admin, nil
}

// GetProfile loads account data, resolves its credential version and hydrates
// the separate avatar view. User or avatar read errors abort the result, and
// the returned object carries the mutable fields supplied by those loaders.
func (s *UserService) GetProfile(ctx context.Context, userID int64) (*User, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	ProfileNormalizeLoadedUserTokenVersion(user)
	if err := s.ProfileHydrateUserAvatar(ctx, user); err != nil {
		return nil, fmt.Errorf("get user avatar: %w", err)
	}
	return user, nil
}

// GetProfileIdentitySummaries assembles email and supported provider views for
// a user. Nil user input is loaded by userID; supplied user data is reused,
// while identity records are still loaded for that identifier. Callers keep
// those two account references aligned.
//
// Primary record selection supplies display hints and binding metadata, then
// explicit runtime switches can disable otherwise offered bind actions. The
// returned set describes profile controls rather than performing an identity
// mutation; actual binding and unbinding entry points apply their own current
// account rules.
func (s *UserService) GetProfileIdentitySummaries(ctx context.Context, userID int64, user *User) (UserIdentitySummarySet, error) {
	if user == nil {
		var err error
		user, err = s.userRepo.GetByID(ctx, userID)
		if err != nil {
			return UserIdentitySummarySet{}, fmt.Errorf("get user: %w", err)
		}
	}

	records, err := s.ProfileListUserAuthIdentities(ctx, userID)
	if err != nil {
		return UserIdentitySummarySet{}, err
	}

	summaries := UserIdentitySummarySet{
		Email:    s.ProfileBuildEmailIdentitySummary(user, records),
		LinuxDo:  s.ProfileBuildProviderIdentitySummary("linuxdo", user, records),
		OIDC:     s.ProfileBuildProviderIdentitySummary("oidc", user, records),
		WeChat:   s.ProfileBuildProviderIdentitySummary("wechat", user, records),
		DingTalk: s.ProfileBuildProviderIdentitySummary("dingtalk", user, records),
	}

	s.ProfileApplyExplicitProviderAvailability(ctx, &summaries)
	return summaries, nil
}

// ProfileApplyExplicitProviderAvailability removes bind actions disabled by
// explicit runtime values. Nil inputs or a missing settings reader leave
// summaries unchanged, and read failure also retains the existing result.
// Nonblank non-true switches disable their unbound provider controls.
//
// WeChat evaluates its browser capabilities only after an explicit enabled
// global switch; a mobile-only choice cannot provide that browser bind action.
// This helper changes offered actions rather than their persisted identities,
// so already bound summaries remain available for their existing account
// relationship.
func (s *UserService) ProfileApplyExplicitProviderAvailability(ctx context.Context, summaries *UserIdentitySummarySet) {
	if s == nil || summaries == nil || s.settingRepo == nil {
		return
	}

	settings, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyLinuxDoConnectEnabled,
		SettingKeyOIDCConnectEnabled,
		SettingKeyWeChatConnectEnabled,
		SettingKeyWeChatConnectOpenEnabled,
		SettingKeyWeChatConnectMPEnabled,
		SettingKeyWeChatConnectMobileEnabled,
		SettingKeyWeChatConnectMode,
		SettingKeyDingTalkConnectEnabled,
	})
	if err != nil {
		// Failed availability reads leave existing relationship views intact;
		// login-start validation still checks provider readiness at the actual protected action.
		return
	}

	if raw, ok := settings[SettingKeyLinuxDoConnectEnabled]; ok && strings.TrimSpace(raw) != "" && raw != "true" {
		ProfileDisableIdentityBindAction(&summaries.LinuxDo)
	}
	if raw, ok := settings[SettingKeyDingTalkConnectEnabled]; ok && strings.TrimSpace(raw) != "" && raw != "true" {
		ProfileDisableIdentityBindAction(&summaries.DingTalk)
	}
	if raw, ok := settings[SettingKeyOIDCConnectEnabled]; ok && strings.TrimSpace(raw) != "" && raw != "true" {
		ProfileDisableIdentityBindAction(&summaries.OIDC)
	}
	if raw, ok := settings[SettingKeyWeChatConnectEnabled]; ok && strings.TrimSpace(raw) != "" {
		if raw != "true" {
			ProfileDisableIdentityBindAction(&summaries.WeChat)
			return
		}
		openEnabled, mpEnabled, _ := ParseWeChatConnectCapabilitySettings(settings, true, settings[SettingKeyWeChatConnectMode])
		if !openEnabled && !mpEnabled {
			ProfileDisableIdentityBindAction(&summaries.WeChat)
		}
	}
}

// ProfileDisableIdentityBindAction clears the offered path and bind flag for an
// unbound summary. Nil or already bound entries remain unchanged, preserving
// their existing relationship display while unbound controls can be suppressed.
func ProfileDisableIdentityBindAction(summary *UserIdentitySummary) {
	if summary == nil || summary.Bound {
		return
	}
	summary.CanBind = false
	summary.BindStartPath = ""
}

// PrepareIdentityBindingStart returns the protected binding-start URL and
// browser redirect instructions. It normalizes the requested provider and
// destination, rejects unsupported values, and constructs a GET action; current
// login state is checked by the HTTP entry point.
func (s *UserService) PrepareIdentityBindingStart(_ context.Context, req StartUserIdentityBindingRequest) (*StartUserIdentityBindingResult, error) {
	provider := ProfileNormalizeUserIdentityProvider(req.Provider)
	if provider == "" {
		return nil, ErrIdentityProviderInvalid
	}

	authorizeURL, err := ProfileBuildUserIdentityBindAuthorizeURL(provider, req.RedirectTo)
	if err != nil {
		return nil, err
	}

	return &StartUserIdentityBindingResult{
		Provider:           provider,
		AuthorizeURL:       authorizeURL,
		Method:             "GET",
		UseBrowserRedirect: true,
	}, nil
}

// UnbindUserAuthProvider forwards an unbind request and returns the refreshed
// account. It discards the changed boolean from
// UnbindUserAuthProviderWithResult; callers that need to distinguish an absent
// binding invoke that result-bearing method directly.
func (s *UserService) UnbindUserAuthProvider(ctx context.Context, userID int64, provider string) (*User, error) {
	user, _, err := s.UnbindUserAuthProviderWithResult(ctx, userID, provider)
	return user, err
}

// UnbindUserAuthProviderWithResult removes a supported third-party binding
// while retaining a recognized sign-in method. Email and invalid providers are
// rejected, and an already absent provider returns the loaded user with
// changed=false.
//
// The method checks identity records before deletion, then invalidates
// authentication data and reloads the profile after a successful write. These
// are separate operations here; a later reload error can follow a completed
// unbind, so callers cannot treat every error as proof that the identity
// relationship stayed unchanged.
func (s *UserService) UnbindUserAuthProviderWithResult(ctx context.Context, userID int64, provider string) (*User, bool, error) {
	provider = ProfileNormalizeUserIdentityProvider(provider)
	if provider == "" || provider == "email" {
		return nil, false, ErrIdentityProviderInvalid
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, false, fmt.Errorf("get user: %w", err)
	}

	records, err := s.ProfileListUserAuthIdentities(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	if len(ProfileFilterUserAuthIdentities(records, provider)) == 0 {
		return user, false, nil
	}
	if !s.ProfileCanUnbindProvider(provider, user, records) {
		return nil, false, ErrIdentityUnbindLastMethod
	}

	if err := s.userRepo.UnbindUserAuthProvider(ctx, userID, provider); err != nil {
		return nil, false, err
	}
	// Invalidate after the unbind write so later authentication snapshots can
	// reload the changed relationship even if the following profile reload fails.
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}

	updatedUser, err := s.GetProfile(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	return updatedUser, true, nil
}

// UpdateProfile persists selected profile inputs and returns the updated
// account view. Repositories supporting ProfileUserProfileIdentityTxRunner
// receive the helper callback with a transaction context; other repositories
// execute the helper directly. Each store determines its transaction participation.
//
// Concurrency invalidation follows a successful runner/helper return. A runner
// reusing an external transaction can return before its owner commits. The
// PostgreSQL UserStore account update starts from its bound client, so callback
// context alone does not establish a shared account/avatar commit. The field mask
// protects omitted columns, and returned errors can follow writes outside the
// transaction that subsequently failed.
func (s *UserService) UpdateProfile(ctx context.Context, userID int64, req UpdateProfileRequest) (*User, error) {
	if txRunner, ok := s.userRepo.(ProfileUserProfileIdentityTxRunner); ok {
		var (
			updated        *User
			oldConcurrency int
		)
		if err := txRunner.WithUserProfileIdentityTx(ctx, func(txCtx context.Context) error {
			var err error
			updated, oldConcurrency, err = s.ProfileUpdateProfile(txCtx, userID, req)
			return err
		}); err != nil {
			return nil, err
		}
		// Publish after the runner reports success. An externally owned transaction
		// may still await commit, so its owner coordinates visibility beyond this callback result.
		if s.authCacheInvalidator != nil && updated != nil && updated.Concurrency != oldConcurrency {
			s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
		}
		return updated, nil
	}

	updated, oldConcurrency, err := s.ProfileUpdateProfile(ctx, userID, req)
	if err != nil {
		return nil, err
	}
	// Direct helper persistence completed; invalidate changed concurrency after
	// that result so authentication reloads the selected account limit on later requests.
	if s.authCacheInvalidator != nil && updated.Concurrency != oldConcurrency {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	return updated, nil
}

// ProfileUpdateProfile applies requested profile fields and returns the account
// plus its previous concurrency. ClearPreferredLocale is applied before a
// supplied locale, so a supplied valid value takes precedence; email input is
// rejected in favor of verified binding.
//
// Avatar changes use separate avatar persistence, while account fields use an
// explicit UserUpdateFields mask. Nonpositive supplied balance thresholds clear
// the override. The working user can be changed before a later validation or
// persistence failure. Shared rollback requires both stores to honor the same
// transaction; merely supplying an outer context does not establish that participation.
func (s *UserService) ProfileUpdateProfile(ctx context.Context, userID int64, req UpdateProfileRequest) (*User, int, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("get user: %w", err)
	}
	oldConcurrency := user.Concurrency

	// Select requested columns so this snapshot cannot replace unselected balance,
	// status or limits changed by other writers during this profile operation.
	var fields UserUpdateFields
	if req.ClearPreferredLocale {
		user.PreferredLocale = nil
		fields.PreferredLocale = true
	}
	if req.PreferredLocale != nil {
		code := locale.Normalize(*req.PreferredLocale)
		if code == "" {
			return nil, oldConcurrency, infraerrors.BadRequest("INVALID_LOCALE", "Language is not supported.")
		}
		user.PreferredLocale = &code
		fields.PreferredLocale = true
	}

	if req.Email != nil {
		return nil, oldConcurrency, ErrProfileEmailChangeForbidden
	}

	if req.Username != nil {
		user.Username = *req.Username
		fields.Username = true
	}

	if req.AvatarURL != nil {
		// Persist the avatar before the selected account write. Shared rollback
		// depends on actual transaction participation; an independently completed write
		// can survive failure of the other store or its transaction completion.
		avatar, err := s.SetAvatar(ctx, userID, *req.AvatarURL)
		if err != nil {
			return nil, oldConcurrency, err
		}
		ProfileApplyUserAvatar(user, avatar)
	}

	if req.Concurrency != nil {
		user.Concurrency = *req.Concurrency
		fields.Concurrency = true
	}

	if req.BalanceNotifyEnabled != nil {
		user.BalanceNotifyEnabled = *req.BalanceNotifyEnabled
		fields.BalanceNotifySettings = true
	}
	if req.BalanceNotifyThreshold != nil {
		// Nonpositive profile thresholds restore the global threshold choice.
		if *req.BalanceNotifyThreshold <= 0 {
			user.BalanceNotifyThreshold = nil
		} else {
			user.BalanceNotifyThreshold = req.BalanceNotifyThreshold
		}
		fields.BalanceNotifySettings = true
	}

	if err := s.userRepo.Update(ctx, user, fields); err != nil {
		return nil, oldConcurrency, fmt.Errorf("update user: %w", err)
	}

	return user, oldConcurrency, nil
}

// SetAvatar stores a normalized avatar choice and returns its separate record.
// Blank trimmed text deletes the current avatar and returns nil; nonblank text
// must pass URL or inline-image preparation before the repository upsert.
func (s *UserService) SetAvatar(ctx context.Context, userID int64, raw string) (*UserAvatar, error) {
	avatarValue := strings.TrimSpace(raw)
	if avatarValue == "" {
		if err := s.userRepo.DeleteUserAvatar(ctx, userID); err != nil {
			return nil, fmt.Errorf("delete avatar: %w", err)
		}
		return nil, nil
	}

	avatarInput, err := ProfileNormalizeUserAvatarInput(avatarValue)
	if err != nil {
		return nil, err
	}

	avatar, err := s.userRepo.UpsertUserAvatar(ctx, userID, avatarInput)
	if err != nil {
		return nil, fmt.Errorf("upsert avatar: %w", err)
	}
	return avatar, nil
}

// ProfileApplyUserAvatar copies an avatar record into the account's hydrated
// display fields. Nil users are skipped, and nil avatars clear URL, source,
// MIME, size and digest together so stale view metadata does not remain.
func ProfileApplyUserAvatar(user *User, avatar *UserAvatar) {
	if user == nil {
		return
	}
	if avatar == nil {
		user.AvatarURL = ""
		user.AvatarSource = ""
		user.AvatarMIME = ""
		user.AvatarByteSize = 0
		user.AvatarSHA256 = ""
		return
	}

	user.AvatarURL = avatar.URL
	user.AvatarSource = avatar.StorageProvider
	user.AvatarMIME = avatar.ContentType
	user.AvatarByteSize = avatar.ByteSize
	user.AvatarSHA256 = avatar.SHA256
}

// ProfileNormalizeUserAvatarInput prepares an inline data URL or remote HTTP(S)
// choice. Empty, malformed or hostless input is rejected; remote URLs are
// retained as trimmed text and are not fetched or image-validated by this
// preparation step.
func ProfileNormalizeUserAvatarInput(raw string) (UpsertUserAvatarInput, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return UpsertUserAvatarInput{}, ErrAvatarInvalid
	}
	if strings.HasPrefix(raw, "data:") {
		return ProfileNormalizeInlineUserAvatarInput(raw)
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil {
		return UpsertUserAvatarInput{}, ErrAvatarInvalid
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return UpsertUserAvatarInput{}, ErrAvatarInvalid
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return UpsertUserAvatarInput{}, ErrAvatarInvalid
	}

	return UpsertUserAvatarInput{
		StorageProvider: "remote_url",
		URL:             raw,
	}, nil
}

// ValidateUserAvatar returns the shared normalizer's rejection for an avatar value.
// Remote HTTP(S) input is checked as a URL, while inline input follows the
// base64 and size-dependent compression rules without writing any profile data.
func ValidateUserAvatar(raw string) error {
	_, err := ProfileNormalizeUserAvatarInput(raw)
	return err
}

// ProfileNormalizeInlineUserAvatarInput decodes an image-prefixed base64 data
// URL into saved avatar metadata. It requires the data separator and base64
// marker, rejects decoded input above the maximum and compresses input above
// the target size.
//
// Compression decodes image bytes and writes JPEG data; smaller supplied bytes
// retain their declared image MIME and original data URL without that decoder
// pass. Size and SHA-256 reflect the resulting bytes. Callers therefore
// distinguish format preparation from proof that every below-target payload is
// decodable image content.
func ProfileNormalizeInlineUserAvatarInput(raw string) (UpsertUserAvatarInput, error) {
	body := strings.TrimPrefix(raw, "data:")
	meta, encoded, ok := strings.Cut(body, ",")
	if !ok {
		return UpsertUserAvatarInput{}, ErrAvatarInvalid
	}
	meta = strings.TrimSpace(meta)
	encoded = strings.TrimSpace(encoded)
	if !strings.HasSuffix(strings.ToLower(meta), ";base64") {
		return UpsertUserAvatarInput{}, ErrAvatarInvalid
	}

	contentType := strings.TrimSpace(meta[:len(meta)-len(";base64")])
	if contentType == "" || !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return UpsertUserAvatarInput{}, ErrAvatarNotImage
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return UpsertUserAvatarInput{}, ErrAvatarInvalid
	}
	// Reject oversized decoded bytes before any compression candidate can be tried.
	if len(decoded) > ProfileMaxInlineAvatarBytes {
		return UpsertUserAvatarInput{}, ErrAvatarTooLarge
	}

	// Larger inline payloads pass through image decoding and JPEG selection;
	// a successful replacement updates both the stored URL and its byte-derived metadata.
	if len(decoded) > ProfileTargetAvatarBytes {
		decoded, contentType, err = ProfileCompressInlineAvatar(decoded)
		if err != nil {
			return UpsertUserAvatarInput{}, err
		}
		raw = "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(decoded)
	}

	sum := sha256.Sum256(decoded)
	return UpsertUserAvatarInput{
		StorageProvider: "inline",
		URL:             raw,
		ContentType:     contentType,
		ByteSize:        len(decoded),
		SHA256:          hex.EncodeToString(sum[:]),
	}, nil
}

// ProfileCompressInlineAvatar decodes image bytes and searches ordered scale
// and JPEG quality choices for a result within the target. Empty bounds or
// encode/decode failures return the invalid-avatar error; exhausting every
// candidate returns too large.
//
// Each scale uses dimensions clamped to one pixel and composites onto white
// before JPEG encoding, so alpha is represented on that background. The first
// candidate meeting the byte budget wins, making the configured scale and
// quality order part of the returned image choice rather than an independent
// optimization hint.
func ProfileCompressInlineAvatar(decoded []byte) ([]byte, string, error) {
	src, _, err := image.Decode(bytes.NewReader(decoded))
	if err != nil {
		return nil, "", ErrAvatarInvalid
	}

	srcBounds := src.Bounds()
	if srcBounds.Empty() {
		return nil, "", ErrAvatarInvalid
	}

	for _, scale := range ProfileAvatarScaleSteps {
		// Keep scaled dimensions at least one pixel for JPEG encoding.
		width := max(1, int(float64(srcBounds.Dx())*scale))
		height := max(1, int(float64(srcBounds.Dy())*scale))
		dst := image.NewRGBA(image.Rect(0, 0, width, height))
		// JPEG has no alpha channel, so composite each resized candidate on white
		// before encoding to give transparent source pixels a defined stored background.
		stddraw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, stddraw.Src)
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, srcBounds, stddraw.Over, nil)

		// Try all configured qualities at this scale before moving to the next
		// dimensions; the earliest candidate within budget determines the persisted image.
		for _, quality := range ProfileAvatarQualitySteps {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
				return nil, "", ErrAvatarInvalid
			}
			if buf.Len() <= ProfileTargetAvatarBytes {
				return buf.Bytes(), "image/jpeg", nil
			}
		}
	}

	return nil, "", ErrAvatarTooLarge
}

// ProfileBuildEmailIdentitySummary chooses the email relationship displayed for
// an account. Explicit email identities use the selected primary record and a
// sequence of metadata, subject, account email and provider-key display
// candidates.
//
// When no email identity exists, a nonreserved stored account email still
// produces a bound legacy view so accounts awaiting backfill retain their
// profile display. That compatibility view does not enable third-party bind or
// unbind actions. The selected record's verification pointer remains shared,
// and nil user input returns the initial managed-email summary.
func (s *UserService) ProfileBuildEmailIdentitySummary(user *User, records []UserAuthIdentityRecord) UserIdentitySummary {
	summary := UserIdentitySummary{
		Provider:  "email",
		CanBind:   false,
		CanUnbind: false,
		NoteKey:   ProfileUserIdentityNoteEmailManagedByBinding,
		Note:      "Primary account email is managed through verified email binding.",
	}
	if user == nil {
		return summary
	}

	filtered := ProfileFilterUserAuthIdentities(records, "email")
	if len(filtered) > 0 {
		primary := ProfileSelectPrimaryUserAuthIdentity(filtered)
		email := strings.TrimSpace(ProfileFirstStringIdentityValue(primary.Metadata, "email"))
		// Metadata may lack a useful mailbox label, so use the selected subject
		// before consulting the account and provider-key compatibility candidates.
		if email == "" {
			email = strings.TrimSpace(primary.ProviderSubject)
		}
		// A missing or placeholder identity label can still use the account
		// mailbox, keeping the visible email choice tied to existing local account data.
		if email == "" || IsReservedEmail(email) {
			email = strings.TrimSpace(user.Email)
		}
		// When neither identity nor account supplies a real mailbox label, retain
		// the selected provider key as the final relationship display candidate.
		if email == "" || IsReservedEmail(email) {
			email = strings.TrimSpace(primary.ProviderKey)
		}

		summary.Bound = true
		summary.BoundCount = len(filtered)
		summary.DisplayName = email
		summary.SubjectHint = ProfileMaskEmailIdentity(email)
		summary.ProviderKey = strings.TrimSpace(primary.ProviderKey)
		summary.VerifiedAt = primary.VerifiedAt
		return summary
	}

	// A real stored mailbox supplies the legacy bound view when identity
	// backfill has no row yet, keeping email display usable for those existing accounts.
	email := strings.TrimSpace(user.Email)
	if email == "" || IsReservedEmail(email) {
		return summary
	}
	summary.Bound = true
	summary.BoundCount = 1
	summary.DisplayName = email
	summary.SubjectHint = ProfileMaskEmailIdentity(email)
	summary.ProviderKey = "email"
	return summary
}

// ProfileBuildProviderIdentitySummary builds one third-party relationship view
// from matching records. Unbound providers offer their protected start path
// when construction succeeds, while bound providers select a primary record for
// name, avatar, subject hint and verification metadata.
//
// Unbind eligibility comes from the recognized remaining sign-in methods on
// this user, and its result selects the corresponding profile note. The helper
// performs no persistence or full provider configuration check; explicit
// availability filtering runs afterward on the assembled summaries. Returned
// verification pointers still refer to the selected record.
func (s *UserService) ProfileBuildProviderIdentitySummary(provider string, user *User, records []UserAuthIdentityRecord) UserIdentitySummary {
	summary := UserIdentitySummary{
		Provider:  provider,
		CanUnbind: false,
	}
	filtered := ProfileFilterUserAuthIdentities(records, provider)
	if len(filtered) == 0 {
		summary.CanBind = true
		bindStartPath, err := ProfileBuildUserIdentityBindAuthorizeURL(provider, "")
		// An unavailable start path leaves the offered bind flag with an empty
		// path; this display helper suppresses the construction error for its caller.
		if err == nil {
			summary.BindStartPath = bindStartPath
		}
		return summary
	}

	primary := ProfileSelectPrimaryUserAuthIdentity(filtered)
	summary.Bound = true
	summary.BoundCount = len(filtered)
	summary.DisplayName = ProfileUserAuthIdentityDisplayName(primary)
	summary.AvatarURL = strings.TrimSpace(ProfileFirstStringIdentityValue(primary.Metadata, "avatar_url", "suggested_avatar_url", "headimgurl"))
	summary.SubjectHint = ProfileMaskOpaqueIdentity(primary.ProviderSubject)
	summary.ProviderKey = strings.TrimSpace(primary.ProviderKey)
	summary.VerifiedAt = primary.VerifiedAt
	summary.CanUnbind = s.ProfileCanUnbindProvider(provider, user, records)
	if summary.CanUnbind {
		summary.NoteKey = ProfileUserIdentityNoteCanUnbind
		summary.Note = "You can unbind this sign-in method."
	} else {
		summary.NoteKey = ProfileUserIdentityNoteBindAnotherBeforeUnbind
		summary.Note = "Bind another sign-in method before unbinding."
	}
	return summary
}

// ProfileCanUnbindProvider reports whether removing a bound non-email provider
// leaves another recognized login method. A usable stored email can satisfy the
// check; otherwise a distinct LinuxDo, OIDC, WeChat or DingTalk identity is
// required. Callers supply a normalized provider label because candidate exclusion
// compares that text exactly.
//
// Email usability depends on signup source or accepted email-identity metadata.
// GitHub and Google are not enumerated as alternatives here. The supplied user and
// records need the same account ownership, and a later separate unbind write can
// still fail after this snapshot-based eligibility decision.
func (s *UserService) ProfileCanUnbindProvider(provider string, user *User, records []UserAuthIdentityRecord) bool {
	if provider == "" || provider == "email" || len(ProfileFilterUserAuthIdentities(records, provider)) == 0 {
		return false
	}

	if s.ProfileCanUseEmailAsSignInMethod(user, records) {
		return true
	}

	for _, candidate := range []string{"linuxdo", "oidc", "wechat", "dingtalk"} {
		if candidate == provider {
			continue
		}
		if len(ProfileFilterUserAuthIdentities(records, candidate)) > 0 {
			return true
		}
	}

	return false
}

// ProfileCanUseEmailAsSignInMethod checks stored email text and its login
// provenance. Nil users, blank text or reserved synthetic addresses fail;
// empty/email signup sources succeed, while other sources need an email identity
// with an accepted metadata source.
//
// A stored email can be a compatibility address associated with third-party
// signup. The recognized binding, login-backfill and dual-write origins distinguish
// usable email sign-in provenance from compatibility-only identity rows, so the
// profile unbind decision does not count those other rows as a remaining email
// login method. This helper does not parse mailbox syntax.
func (s *UserService) ProfileCanUseEmailAsSignInMethod(user *User, records []UserAuthIdentityRecord) bool {
	if user == nil {
		return false
	}

	email := strings.ToLower(strings.TrimSpace(user.Email))
	if email == "" || IsReservedEmail(email) {
		return false
	}

	if ProfileEmailSignupSourceAllowsLogin(user.SignupSource) {
		return true
	}

	return slices.ContainsFunc(ProfileFilterUserAuthIdentities(records, "email"), ProfileEmailIdentitySupportsSignIn)
}

// ProfileEmailSignupSourceAllowsLogin accepts an empty or email source after
// case and whitespace normalization. Other source labels return false, so
// caller email-login decisions can consult identity provenance rather than the
// presence of an address alone.
func ProfileEmailSignupSourceAllowsLogin(signupSource string) bool {
	signupSource = strings.ToLower(strings.TrimSpace(signupSource))
	return signupSource == "" || signupSource == "email"
}

// ProfileEmailIdentitySupportsSignIn classifies an already filtered email
// record by metadata source. Binding, login-backfill and dual-write origins
// return true; the helper does not inspect ProviderType or verify the mailbox,
// so callers apply their provider/account checks first.
func ProfileEmailIdentitySupportsSignIn(record UserAuthIdentityRecord) bool {
	source := strings.TrimSpace(ProfileFirstStringIdentityValue(record.Metadata, "source"))
	switch source {
	case "auth_service_email_bind", "auth_service_login_backfill", "auth_service_dual_write":
		return true
	default:
		return false
	}
}

// ProfileListUserAuthIdentities loads the account's identity records through
// its repository. Nonpositive IDs, a nil service or an absent repository return
// nil without error; configured repository failures remain visible to the
// profile caller.
func (s *UserService) ProfileListUserAuthIdentities(ctx context.Context, userID int64) ([]UserAuthIdentityRecord, error) {
	if userID <= 0 || s == nil || s.userRepo == nil {
		return nil, nil
	}
	return s.userRepo.ListUserAuthIdentities(ctx, userID)
}

// ProfileBuildUserIdentityBindAuthorizeURL returns a protected local start path
// with the selected redirect and bind_current_user intent. It accepts supported
// third-party providers, normalizes the frontend path, and rejects email or
// unknown provider choices before URL construction.
func ProfileBuildUserIdentityBindAuthorizeURL(provider, redirectTo string) (string, error) {
	provider = ProfileNormalizeUserIdentityProvider(provider)
	if provider == "" || provider == "email" {
		return "", ErrIdentityProviderInvalid
	}

	redirectTo, err := ProfileNormalizeUserIdentityRedirect(redirectTo)
	if err != nil {
		return "", err
	}

	path := ""
	switch provider {
	case "linuxdo":
		path = "/api/v1/auth/oauth/linuxdo/bind/start"
	case "oidc":
		path = "/api/v1/auth/oauth/oidc/bind/start"
	case "wechat":
		path = "/api/v1/auth/oauth/wechat/bind/start"
	case "dingtalk":
		path = "/api/v1/auth/oauth/dingtalk/bind/start"
	default:
		return "", ErrIdentityProviderInvalid
	}

	query := url.Values{}
	query.Set("redirect", redirectTo)
	query.Set("intent", "bind_current_user")
	return path + "?" + query.Encode(), nil
}

// ProfileNormalizeUserIdentityProvider returns a recognized profile provider
// label after trimming and lowercasing. Email, LinuxDo, OIDC, WeChat and
// DingTalk are accepted; other labels become empty so operation-specific
// callers can reject unsupported choices.
func ProfileNormalizeUserIdentityProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "linuxdo":
		return "linuxdo"
	case "oidc":
		return "oidc"
	case "wechat":
		return "wechat"
	case "dingtalk":
		return "dingtalk"
	case "email":
		return "email"
	default:
		return ""
	}
}

// ProfileNormalizeUserIdentityRedirect prepares a local frontend return path
// for binding. Blank input uses the profile settings path; oversized text,
// missing leading slash or a protocol-relative prefix returns the redirect
// error. Other local path text is retained.
func ProfileNormalizeUserIdentityRedirect(raw string) (string, error) {
	redirect := strings.TrimSpace(raw)
	if redirect == "" {
		return ProfileDefaultUserIdentityRedirect, nil
	}
	// Bound redirect bytes for its binding-start query value.
	if len(redirect) > 2048 || !strings.HasPrefix(redirect, "/") || strings.HasPrefix(redirect, "//") {
		return "", ErrIdentityRedirectInvalid
	}
	return redirect, nil
}

// ProfileFilterUserAuthIdentities returns matching records in supplied order.
// It trims recorded provider types and compares them case-insensitively against
// the caller's untrimmed provider text. Empty input returns nil, while record maps
// and pointers remain shared in the new slice.
func ProfileFilterUserAuthIdentities(records []UserAuthIdentityRecord, provider string) []UserAuthIdentityRecord {
	if len(records) == 0 {
		return nil
	}
	filtered := make([]UserAuthIdentityRecord, 0, len(records))
	for _, record := range records {
		if strings.EqualFold(strings.TrimSpace(record.ProviderType), provider) {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

// ProfileSelectPrimaryUserAuthIdentity selects the latest effective record
// time, breaking time ties by provider key. Empty input returns a zero record.
// The supplied slice is stably sorted in place, so callers copy it when they
// need its previous order retained.
//
// Time selection prefers a nonzero verification timestamp, then update time,
// then creation time; all are compared in UTC. Stable sorting preserves input
// order for equal time and key. The returned record is a value copy whose
// metadata and pointer fields retain their underlying sharing.
func ProfileSelectPrimaryUserAuthIdentity(records []UserAuthIdentityRecord) UserAuthIdentityRecord {
	if len(records) == 0 {
		return UserAuthIdentityRecord{}
	}
	// Sort the caller-provided working slice in place so the chosen first record
	// and the remaining order share one timestamp/key preference without detached record indexes.
	sort.SliceStable(records, func(i, j int) bool {
		left := ProfileUserAuthIdentitySortTime(records[i])
		right := ProfileUserAuthIdentitySortTime(records[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return records[i].ProviderKey < records[j].ProviderKey
	})
	return records[0]
}

// ProfileUserAuthIdentitySortTime returns the first usable verification, update
// or creation timestamp in UTC. Nil or zero verification time falls through to
// update and creation values; a record with no usable observation returns zero
// time.
//
// Records without verification can therefore participate in primary selection
// through a lifecycle timestamp. A nonzero verification time takes precedence
// even when update time is later, so the caller's latest-record order compares
// this chosen observation rather than the largest of every timestamp in a record.
func ProfileUserAuthIdentitySortTime(record UserAuthIdentityRecord) time.Time {
	// Prefer an actual verification observation, then modification and creation
	// times, so profile primary selection compares the available identity lifecycle timestamp.
	if record.VerifiedAt != nil && !record.VerifiedAt.IsZero() {
		return record.VerifiedAt.UTC()
	}
	if !record.UpdatedAt.IsZero() {
		return record.UpdatedAt.UTC()
	}
	if !record.CreatedAt.IsZero() {
		return record.CreatedAt.UTC()
	}
	return time.Time{}
}

// ProfileUserAuthIdentityDisplayName selects the first usable metadata display
// hint in configured key order. If no supported nonblank value exists, trimmed
// subject text is used, then trimmed provider type, giving the profile a
// fallback identity label.
func ProfileUserAuthIdentityDisplayName(record UserAuthIdentityRecord) string {
	if displayName := ProfileFirstStringIdentityValue(record.Metadata,
		"display_name",
		"suggested_display_name",
		"username",
		"name",
		"nickname",
		"email",
	); displayName != "" {
		return displayName
	}
	// Metadata may supply no supported display value; retain a relationship label
	// from the selected subject or provider type so the bound profile view remains identifiable.
	if subject := strings.TrimSpace(record.ProviderSubject); subject != "" {
		return subject
	}
	return strings.TrimSpace(record.ProviderType)
}

// ProfileFirstStringIdentityValue returns the first nonblank trimmed value at
// the supplied keys. Strings and fmt.Stringer values are supported; other types
// and absent keys are skipped, and no usable candidate returns empty text.
func ProfileFirstStringIdentityValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, ok := values[key]
		if !ok {
			continue
		}
		switch value := raw.(type) {
		case string:
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		case fmt.Stringer:
			if trimmed := strings.TrimSpace(value.String()); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

// ProfileMaskEmailIdentity masks the local part while retaining its first and
// optional last rune plus the domain. Unsplittable or empty-part input
// delegates to the opaque mask; valid split input is not subjected to complete
// mailbox validation.
func ProfileMaskEmailIdentity(email string) string {
	local, domain, ok := strings.Cut(strings.TrimSpace(email), "@")
	// Unsuitable mailbox splitting still gets an opaque subject hint, allowing
	// the profile to display a masked value instead of unusable email-specific structure.
	if !ok || local == "" || domain == "" {
		return ProfileMaskOpaqueIdentity(email)
	}
	// Retain complete endpoint runes while masking the mailbox local part.
	runes := []rune(local)
	if len(runes) == 1 {
		return string(runes[0]) + "***@" + domain
	}
	return string(runes[0]) + "***" + string(runes[len(runes)-1]) + "@" + domain
}

// ProfileMaskOpaqueIdentity returns a bounded visible prefix and suffix
// selected by rune count. Empty input stays empty; shorter identifiers reveal
// fewer endpoint runes, with three asterisks separating or following the
// retained text.
func ProfileMaskOpaqueIdentity(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	// Rune-count bands set visible endpoints without splitting UTF-8 characters.
	switch {
	case len(runes) == 0:
		return ""
	case len(runes) <= 4:
		return string(runes[0]) + "***"
	case len(runes) <= 8:
		return string(runes[:2]) + "***" + string(runes[len(runes)-1:])
	default:
		return string(runes[:3]) + "***" + string(runes[len(runes)-3:])
	}
}

// ChangePassword verifies the current password and persists the newly generated
// hash through its field mask. Hash or write failures remain errors. The
// in-memory seed increment is separate from stored credential-fingerprint
// invalidation; this method does not persist an independent token-version
// column.
func (s *UserService) ChangePassword(ctx context.Context, userID int64, req ChangePasswordRequest) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	if !user.CheckPassword(req.CurrentPassword) {
		return ErrPasswordIncorrect
	}

	if err := user.SetPassword(req.NewPassword); err != nil {
		return fmt.Errorf("set password: %w", err)
	}

	// Update the in-memory version; persistence below changes the credential fingerprint.
	user.TokenVersion++

	// TokenVersion has no database column. ResolvedTokenVersion derives it from
	// email and password_hash, so persisting the new hash invalidates old tokens.
	if err := s.userRepo.Update(ctx, user, UserUpdateFields{PasswordHash: true}); err != nil {
		return fmt.Errorf("update user: %w", err)
	}

	return nil
}

// GetByID loads an account, resolves its session fingerprint and hydrates
// avatar metadata for management use. User or avatar read failures abort the
// result, and the returned value uses the loaded object rather than a deep
// copy.
func (s *UserService) GetByID(ctx context.Context, id int64) (*User, error) {
	user, err := s.userRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	ProfileNormalizeLoadedUserTokenVersion(user)
	if err := s.ProfileHydrateUserAvatar(ctx, user); err != nil {
		return nil, fmt.Errorf("get user avatar: %w", err)
	}
	return user, nil
}

// ProfileNormalizeLoadedUserTokenVersion replaces an unresolved seed with its
// credential fingerprint and marks that snapshot resolved. Nil or already
// resolved users are skipped; callers keep the resolved value consistent when
// later changing that snapshot's credential fields.
func ProfileNormalizeLoadedUserTokenVersion(user *User) {
	if user == nil || user.TokenVersionResolved {
		return
	}
	user.TokenVersion = ResolvedTokenVersion(user)
	user.TokenVersionResolved = true
}

// TouchLastActive loads an account and requests a throttled activity update.
// Invalid IDs or unavailable dependencies are skipped, and load/write failures
// are diagnosed rather than returned so authentication can finish independently
// of this optional activity observation.
func (s *UserService) TouchLastActive(ctx context.Context, userID int64) {
	if s == nil || s.userRepo == nil || userID <= 0 {
		return
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		// A failed optional activity read supplies no writable account observation;
		// diagnose it and allow authentication to finish without propagating that failure.
		slog.Debug("skip touch user last active after load failure", "user_id", userID, "error", err)
		return
	}
	s.TouchLastActiveForUser(ctx, user)
}

// TouchLastActiveForUser throttles activity writes for an already loaded
// account using its timestamp, a process-local next-allowed time and user-keyed
// singleflight. Invalid inputs and fresh observations skip work before joining
// that keyed operation. Callers keep user.ID and LastActiveAt stable until this
// synchronous method returns.
//
// The singleflight callback rechecks process-local timing before writing with
// the winning caller's context. Success records the normal touch interval,
// while failure records a shorter backoff and is diagnosed. Waiting calls share
// that callback's result, so their supplied contexts do not independently
// cancel the in-progress synchronous singleflight call.
func (s *UserService) TouchLastActiveForUser(ctx context.Context, user *User) {
	if s == nil || s.userRepo == nil || user == nil || user.ID <= 0 {
		return
	}

	now := s.now()
	if ProfileUserLastActiveFresh(user.LastActiveAt, now) {
		return
	}
	if v, ok := s.lastActiveTouchL1.Load(user.ID); ok {
		if nextAllowedAt, ok := v.(time.Time); ok && now.Before(nextAllowedAt) {
			return
		}
	}

	// Share the first caller's synchronous write by user key; rechecking
	// deadlines inside that callback prevents a later admitted call from writing
	// again after the preceding callback records its process-local suppression window.
	_, err, _ := s.lastActiveTouchSF.Do(strconv.FormatInt(user.ID, 10), func() (any, error) {
		latest := s.now()
		if v, ok := s.lastActiveTouchL1.Load(user.ID); ok {
			if nextAllowedAt, ok := v.(time.Time); ok && latest.Before(nextAllowedAt) {
				return nil, nil
			}
		}
		if ProfileUserLastActiveFresh(user.LastActiveAt, latest) {
			return nil, nil
		}
		if err := s.userRepo.UpdateUserLastActiveAt(ctx, user.ID, latest); err != nil {
			// Publish a short retry delay after the failed write so another request
			// does not immediately repeat the same unavailable activity persistence.
			s.lastActiveTouchL1.Store(user.ID, latest.Add(ProfileUserLastActiveFailBackoff))
			return nil, fmt.Errorf("touch user last active: %w", err)
		}
		// Publish the successful touch deadline so subsequent calls can skip writes
		// even when their loaded account timestamp still predates this observation.
		s.lastActiveTouchL1.Store(user.ID, latest.Add(ProfileUserLastActiveMinTouch))
		return nil, nil
	})
	if err != nil {
		// Keep failed optional activity persistence diagnostic after publishing its
		// retry deadline; the authentication caller receives no failure from this observation helper.
		slog.Warn("touch user last active failed", "user_id", user.ID, "error", err)
	}
}

// ProfileUserLastActiveFresh reports whether the recorded activity is still
// inside the minimum touch interval. Nil timestamps are stale, while equality
// at the interval endpoint permits another touch; future timestamps remain
// fresh under this comparison.
func ProfileUserLastActiveFresh(lastActiveAt *time.Time, now time.Time) bool {
	if lastActiveAt == nil {
		return false
	}
	return now.Before(lastActiveAt.Add(ProfileUserLastActiveMinTouch))
}

// ProfileHydrateUserAvatar loads separate avatar metadata into the supplied
// account view. Nil service/repository/user and zero user ID are successful
// no-ops; other read failures remain errors, and an absent avatar clears the
// hydrated fields.
func (s *UserService) ProfileHydrateUserAvatar(ctx context.Context, user *User) error {
	if s == nil || s.userRepo == nil || user == nil || user.ID == 0 {
		return nil
	}

	avatar, err := s.userRepo.GetUserAvatar(ctx, user.ID)
	if err != nil {
		return err
	}
	ProfileApplyUserAvatar(user, avatar)
	return nil
}

// List returns the repository's requested administrative account page and
// pagination metadata. Read failures are wrapped for the caller; this method
// retains the repository's loading and ordering choices and adds no avatar
// hydration pass.
func (s *UserService) List(ctx context.Context, params pagination.PaginationParams) ([]User, *pagination.PaginationResult, error) {
	users, pagination, err := s.userRepo.List(ctx, params)
	if err != nil {
		return nil, nil, fmt.Errorf("list users: %w", err)
	}
	return users, pagination, nil
}

// UpdateBalance applies a balance increment before publishing authentication and
// optional balance-cache invalidation. The PostgreSQL writer also adds positive
// increments to cumulative recharges. Persistence failure stops later effects;
// the background runner owns acceptance and completion of its cleanup callback.
//
// That callback uses a fresh background context with a five-second timeout, so
// request cancellation does not cancel cleanup. A nil configured runner executes
// it synchronously through constructor fallback. Rejected scheduling, cache
// failure or a recovered callback panic does not undo the stored balance increment
// or change this method's successful result.
func (s *UserService) UpdateBalance(ctx context.Context, userID int64, amount float64) error {
	if err := s.userRepo.UpdateBalance(ctx, userID, amount); err != nil {
		return fmt.Errorf("update balance: %w", err)
	}
	// Publish authentication invalidation after the stored increment succeeds, so
	// later snapshots can reload funding even if separate balance-cache cleanup fails.
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	if s.billingCache != nil {
		// Dispatch balance cleanup after the account write. Runner rejection leaves
		// persistence successful and provides no cleanup result to this calling operation.
		s.runBackground("service/user_service.go:UpdateBalance", func() {
			// Recover callback failure after persistence so a cache-cleanup panic can
			// be diagnosed within the submitted work rather than undo the account update.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic in balance cache invalidation", "user_id", userID, "recover", r)
				}
			}()
			// Give cleanup its own five-second context and release it when the
			// callback returns; cancellation of the account request does not cancel this work.
			cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.billingCache.InvalidateUserBalance(cacheCtx, userID); err != nil {
				slog.Error("invalidate user balance cache failed", "user_id", userID, "error", err)
			}
		})
	}
	return nil
}

// UpdateConcurrency applies the supplied concurrency increment and then
// invalidates authentication snapshots. The PostgreSQL writer adds this value to
// the current limit; write errors stop invalidation, and this method performs no
// range validation or absolute-value replacement.
func (s *UserService) UpdateConcurrency(ctx context.Context, userID int64, concurrency int) error {
	if err := s.userRepo.UpdateConcurrency(ctx, userID, concurrency); err != nil {
		return fmt.Errorf("update concurrency: %w", err)
	}
	// Publish only after the increment succeeds, so cached admission limits can
	// reload the changed stored concurrency without reporting a failed write as complete.
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	return nil
}

// UpdateStatus loads the account, selects only its status column for
// persistence and then invalidates authentication snapshots. A read or write
// error stops later effects; the status string reaches the repository as
// supplied by the operation caller.
func (s *UserService) UpdateStatus(ctx context.Context, userID int64, status string) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	user.Status = status

	if err := s.userRepo.Update(ctx, user, UserUpdateFields{Status: true}); err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	// Publish after the selected status write succeeds, so later authentication
	// snapshots can reload account admission state without invalidating on persistence failure.
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}

	return nil
}

// Delete requests account authentication invalidation before repository
// deletion. A deletion error follows that request, whose callback supplies no
// propagation acknowledgement; repository ownership determines related cleanup
// and the account's retained soft-delete record.
func (s *UserService) Delete(ctx context.Context, userID int64) error {
	// Request invalidation before deletion so configured consumers can discard
	// earlier account observations even if the following delete fails. The void
	// callback does not confirm removal across every cache consumer.
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	if err := s.userRepo.Delete(ctx, userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// SendNotifyEmailCode checks recipient cooldown and user limits, creates a code
// and requests notification delivery before saving its credential. A send error
// leaves the cache and request counter unchanged by this operation; successful
// send is followed by credential storage.
//
// Saving uses separate clock reads for creation and expiry, then the user
// counter is incremented with its window. Save failure returns an error after
// the notification request succeeded, while counter failure is diagnosed and
// still succeeds. These independent effects do not provide one atomic
// delivered-code transaction.
func (s *UserService) SendNotifyEmailCode(ctx context.Context, userID int64, email string, emailService NotifyVerificationSender, cache EmailCache, locale ...string) error {
	if err := ProfileCheckNotifyCodeRateLimitWithClock(ctx, cache, userID, email, s.now); err != nil {
		return err
	}

	code, err := emailService.GenerateVerifyCode()
	if err != nil {
		return fmt.Errorf("generate code: %w", err)
	}

	// Request delivery before caching or counting so an unsuccessful sender
	// result leaves no cooldown record from this flow, even when transport acceptance
	// is uncertain after that failed request.
	if err := s.SendNotifyVerifyEmail(ctx, emailService, userID, email, code, FirstEmailLocale(locale)); err != nil {
		return err
	}

	if err := ProfileSaveNotifyVerifyCodeWithClock(ctx, cache, email, code, s.now); err != nil {
		return err
	}

	// Count this request after credential storage; an increment failure is
	// diagnosed while the completed sender and saved code remain successful results.
	if _, err := cache.IncrNotifyCodeUserRate(ctx, userID, ProfileNotifyCodeUserRateWindow); err != nil {
		slog.Error("failed to increment notify code user rate", "user_id", userID, "error", err)
	}

	return nil
}

// ProfileCheckNotifyCodeRateLimitWithClock checks an email-address cooldown and
// the requesting user's separate notification-code allowance. A readable recent
// credential returns the cooldown error before the user counter is read; a
// readable counter at the cap returns the user-limit error.
//
// The address credential is shared by that email key, while the counter covers
// this user's requests across addresses. Failed reads provide no confirmed
// exceeded limit and are skipped independently. The supplied clock is called
// only when a credential is available, and the eventual counter increment occurs
// after sending and credential storage in SendNotifyEmailCode.
func ProfileCheckNotifyCodeRateLimitWithClock(ctx context.Context, cache EmailCache, userID int64, email string, now func() time.Time) error {
	// A failed address-cache read skips its cooldown observation; the user
	// allowance is still checked separately before this request is allowed to send.
	existing, err := cache.GetNotifyVerifyCode(ctx, email)
	if err == nil && existing != nil {
		if now().Sub(existing.CreatedAt) < VerifyCodeCooldown {
			return ErrVerifyCodeTooFrequent
		}
	}
	// A failed counter read supplies no confirmed exhausted allowance, so this
	// independent user-limit check permits sending after the address cooldown already passed.
	count, err := cache.GetNotifyCodeUserRate(ctx, userID)
	if err == nil && count >= ProfileNotifyCodeUserRateLimit {
		return ErrNotifyCodeUserRateLimit
	}
	return nil
}

// ProfileSaveNotifyVerifyCodeWithClock saves the supplied email-keyed challenge
// under VerifyCodeTTL. A callable clock is read separately for CreatedAt and
// ExpiresAt; this record contains no user identifier. Cache write failures
// remain errors, so callers do not infer confirmed persistence from them.
func ProfileSaveNotifyVerifyCodeWithClock(ctx context.Context, cache EmailCache, email, code string, now func() time.Time) error {
	data := &VerificationCodeData{
		Code:      code,
		Attempts:  0,
		CreatedAt: now(),
		ExpiresAt: now().Add(VerifyCodeTTL),
	}
	if err := cache.SetNotifyVerifyCode(ctx, email, data, VerifyCodeTTL); err != nil {
		return fmt.Errorf("save verify code: %w", err)
	}
	return nil
}

// VerifyAndAddNotifyEmail checks the saved notification code, attempts deletion
// and then updates the recipient list. The email-keyed credential selects the
// verification record; userID selects the account receiving that address.
// Matching code success permits the list operation even when cleanup fails.
//
// Verification and deletion are separate cache calls, and the recipient list is
// separately read and replaced. A later list error can follow completed
// verification or attempted deletion. Callers interpret successful code checking
// separately from final list persistence, since atomic credential claiming and
// concurrent same-list edits require coordination beyond this implementation.
func (s *UserService) VerifyAndAddNotifyEmail(ctx context.Context, userID int64, email, code string, cache EmailCache) error {
	if err := ProfileVerifyNotifyCodeWithClock(ctx, cache, email, code, s.now); err != nil {
		return err
	}
	// The matching code already passed verification. Cleanup is best effort,
	// so recipient-list persistence proceeds even when the deletion result is unavailable.
	_ = cache.DeleteNotifyVerifyCode(ctx, email)
	return s.ProfileAddOrVerifyNotifyEmail(ctx, userID, email)
}

// ProfileVerifyNotifyCodeWithClock compares a saved notification code and its
// attempt allowance. Missing or unreadable records are invalid; a matching code
// succeeds without reading ExpiresAt directly, relying on cache lifetime for
// record availability.
//
// Mismatch increments the request's record and preserves its remaining
// clock-derived TTL during an attempted write. Expired remaining time is
// invalid, and reaching the attempt cap returns the cap error even if counter
// persistence failed. The helper leaves deletion to its caller and does not
// atomically claim the matching credential.
func ProfileVerifyNotifyCodeWithClock(ctx context.Context, cache EmailCache, email, code string, now func() time.Time) error {
	data, err := cache.GetNotifyVerifyCode(ctx, email)
	// Missing or unreadable credentials cannot support verification, so collapse
	// those storage outcomes into the same invalid-code result used by the profile caller.
	if err != nil || data == nil {
		return ErrInvalidVerifyCode
	}
	if data.Attempts >= MaxVerifyCodeAttempts {
		return ErrVerifyCodeMaxAttempts
	}
	if subtle.ConstantTimeCompare([]byte(data.Code), []byte(code)) != 1 {
		data.Attempts++
		// Attempt updates retain only the credential's remaining lifetime.
		remaining := data.ExpiresAt.Sub(now())
		if remaining <= 0 {
			return ErrInvalidVerifyCode
		}
		// Preserve the remaining credential lifetime when saving attempts; a
		// failed write leaves the stored counter uncertain but this request uses its
		// incremented in-memory count to choose the returned verification error.
		if err := cache.SetNotifyVerifyCode(ctx, email, data, remaining); err != nil {
			slog.Error("failed to update notify verify code attempts", "email", email, "error", err)
		}
		if data.Attempts >= MaxVerifyCodeAttempts {
			return ErrVerifyCodeMaxAttempts
		}
		return ErrInvalidVerifyCode
	}
	return nil
}

// ProfileAddOrVerifyNotifyEmail marks an existing case-insensitive recipient
// match verified or appends a new enabled verified record. Existing verified
// entries return success, and a new entry is rejected once the configured
// recipient limit is reached.
//
// Persistence selects the whole notification-list field, retaining unrelated
// account columns but replacing the loaded list. Concurrent edits to that same
// list can overwrite each other. The supplied email spelling is kept for newly
// appended records, and this helper assumes its caller already completed the
// verification decision.
func (s *UserService) ProfileAddOrVerifyNotifyEmail(ctx context.Context, userID int64, email string) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	// Replace only the selected recipient-list field after this case-insensitive
	// decision; concurrent changes to the same list still require caller coordination.
	for i, e := range user.BalanceNotifyExtraEmails {
		if strings.EqualFold(e.Email, email) {
			if !e.Verified {
				user.BalanceNotifyExtraEmails[i].Verified = true
				return s.userRepo.Update(ctx, user, UserUpdateFields{BalanceNotifyExtraEmails: true})
			}
			return nil
		}
	}
	if len(user.BalanceNotifyExtraEmails) >= ProfileMaxNotifyEmails {
		return infraerrors.BadRequest("TOO_MANY_NOTIFY_EMAILS", fmt.Sprintf("maximum %d notification emails allowed", ProfileMaxNotifyEmails))
	}
	user.BalanceNotifyExtraEmails = append(user.BalanceNotifyExtraEmails, NotifyEmailEntry{
		Email:    email,
		Disabled: false,
		Verified: true,
	})
	return s.userRepo.Update(ctx, user, UserUpdateFields{BalanceNotifyExtraEmails: true})
}

// RemoveNotifyEmail removes case-insensitive matches from the loaded
// notification list. An absent address returns the profile error; persistence
// replaces that selected whole list, so callers still coordinate concurrent
// edits to the same recipient collection.
func (s *UserService) RemoveNotifyEmail(ctx context.Context, userID int64, email string) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}

	filtered := make([]NotifyEmailEntry, 0, len(user.BalanceNotifyExtraEmails))
	found := false
	for _, e := range user.BalanceNotifyExtraEmails {
		if strings.EqualFold(e.Email, email) {
			found = true
		} else {
			filtered = append(filtered, e)
		}
	}
	if !found {
		return infraerrors.BadRequest("EMAIL_NOT_FOUND", "notification email not found")
	}
	user.BalanceNotifyExtraEmails = filtered
	return s.userRepo.Update(ctx, user, UserUpdateFields{BalanceNotifyExtraEmails: true})
}

// ToggleNotifyEmail changes the disabled flag on the first case-insensitive
// recipient match. Missing addresses return the profile error; the selected
// list is then persisted as a whole, preserving other account columns but
// retaining same-list concurrency behavior.
func (s *UserService) ToggleNotifyEmail(ctx context.Context, userID int64, email string, disabled bool) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}

	found := false
	for i, e := range user.BalanceNotifyExtraEmails {
		if strings.EqualFold(e.Email, email) {
			user.BalanceNotifyExtraEmails[i].Disabled = disabled
			found = true
			break
		}
	}
	if !found {
		return infraerrors.BadRequest("EMAIL_NOT_FOUND", "notification email not found")
	}

	return s.userRepo.Update(ctx, user, UserUpdateFields{BalanceNotifyExtraEmails: true})
}

// SendNotifyVerifyEmail assembles the sender notice with a runtime site name
// when readable and nonempty. Missing reader, read failure or empty branding
// uses TokenRouter; email, code and locale are passed to the configured sender
// as supplied.
func (s *UserService) SendNotifyVerifyEmail(ctx context.Context, sender NotifyVerificationSender, userID int64, email, code, locale string) error {
	siteName := "TokenRouter"
	// Branding falls back to the built-in site name when the optional reader
	// has no nonempty value, keeping the prepared verification notice renderable.
	if s.settingRepo != nil {
		if name, err := s.settingRepo.GetValue(ctx, SettingKeySiteName); err == nil && name != "" {
			siteName = name
		}
	}
	return sender.SendNotifyVerification(ctx, NotifyVerificationNotice{UserID: userID, Email: email, Code: code, Locale: locale, SiteName: siteName})
}

// NormalizeWeChatConnectModeSetting returns mp or mobile after case and
// whitespace normalization. Every other value selects open, giving capability
// and scope callers the common default mode for absent or unrecognized
// settings.
func NormalizeWeChatConnectModeSetting(raw string) string {
	return authconfig.NormalizeWeChatConnectMode(raw)
}

// ParseWeChatConnectCapabilitySettings returns the open, official-account and
// mobile switches from one supplied map. Any nonblank explicit capability key
// selects the three explicit values together, including false for missing
// neighbors.
//
// When no explicit capability is configured, disabled global state returns all
// false; enabled state derives one capability from the normalized mode. This
// distinction keeps older single-mode settings readable while preserving
// explicitly selected multi-capability combinations. The returned flags alone
// do not establish credential presence or full login-configuration readiness.
func ParseWeChatConnectCapabilitySettings(settings map[string]string, enabled bool, mode string) (bool, bool, bool) {
	mode = NormalizeWeChatConnectModeSetting(mode)
	rawOpen, hasOpen := settings[SettingKeyWeChatConnectOpenEnabled]
	rawMP, hasMP := settings[SettingKeyWeChatConnectMPEnabled]
	rawMobile, hasMobile := settings[SettingKeyWeChatConnectMobileEnabled]
	openConfigured := hasOpen && strings.TrimSpace(rawOpen) != ""
	mpConfigured := hasMP && strings.TrimSpace(rawMP) != ""
	mobileConfigured := hasMobile && strings.TrimSpace(rawMobile) != ""

	if openConfigured || mpConfigured || mobileConfigured {
		openEnabled := strings.TrimSpace(rawOpen) == "true"
		mpEnabled := strings.TrimSpace(rawMP) == "true"
		mobileEnabled := strings.TrimSpace(rawMobile) == "true"
		return openEnabled, mpEnabled, mobileEnabled
	}

	// No explicit capability keys exist, so the global switch and legacy mode
	// determine the single offered capability instead of treating missing neighbors as explicit false.
	if !enabled {
		return false, false, false
	}
	if mode == "mp" {
		return false, true, false
	}
	if mode == "mobile" {
		return false, false, true
	}
	return true, false, false
}
