package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/authconfig"
	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	runtimesettings "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	settingsdto "github.com/TokenFlux/TokenRouter/internal/settings/httpapi/dto"
	sitedto "github.com/TokenFlux/TokenRouter/internal/site/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// composite_update.go validates and commits partial administrator settings
// updates before returning saved values.
// A combined settings request needs its raw field presence before typed
// decoding supplies zero values. The handler rejects retired inputs first, then
// acquires one update session before reading current values, merging partial
// controls, and validating user-visible content or changed challenge credentials.
// Those phases can fail before any settings batch is persisted.
//
// Broad prepared values are replaced by each registered owner's result before
// the single Commit. Successful persistence precedes runtime callbacks, audit
// logging, and response readback; a callback or later read failure therefore
// reports a saved update with an incomplete later phase. Pointer merging and
// raw-presence filtering have field-specific exceptions, including security
// normalization and always-written merged switches, so they are kept visible
// where the corresponding participant fields are assembled.

var (
	// semverPattern limits supported CLI version overrides to three dot-separated
	// numeric components; the handler separately compares admitted minimum and maximum values.
	semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

	// menuItemIDPattern limits submitted navigation IDs to ASCII letters, digits,
	// hyphens, and underscores; length and within-list uniqueness are checked separately.
	menuItemIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

	// markdownMenuSlugPattern applies the page reader's ASCII slug shape to md:
	// navigation targets before the handler accepts them as a page destination.
	markdownMenuSlugPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

	// settingKeyJSONAliases maps request JSON names that differ from their stored setting keys.
	// Other UpdateSettingsRequest fields use their setting keys as JSON names.
	settingKeyJSONAliases = map[string]string{
		"smtp_from_email": notification.SettingKeySMTPFrom,
	}

	// settingKeyByJSONName maps non-pointer top-level request fields to stored setting keys.
	// It is built once from struct tags so added fields participate automatically.
	//
	// UpdateSettings merges pointer fields with stored values, so they are excluded here.
	// Some pointer fields are rewritten on every save to normalize invalid security settings;
	// see TestUpdateSettingsMalformedForwardedClientIPHeadersRemainFailClosedWhenOmitted.
	settingKeyByJSONName = buildSettingKeyByJSONName()
)

// UpdateSettingsRequest aliases the flat combined payload decoded for administrator settings updates.
type UpdateSettingsRequest = settingsdto.UpdateSettingsRequest

// generateMenuItemID returns sixteen hexadecimal characters from eight random
// bytes for a missing navigation ID. The caller checks list uniqueness separately
// and keeps supplied IDs. crypto/rand.Read in the configured Go toolchain fills
// the buffer or terminates the process when its randomness source fails, so
// successful generation returns a nil error.
func generateMenuItemID() (string, error) {
	// Encode 64 random bits within the accepted ASCII navigation-ID format.
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate menu item ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// scopesContainOpenID reports whether whitespace-separated scope text includes openid, ignoring case.
func scopesContainOpenID(scopes string) bool {
	return slices.Contains(strings.Fields(strings.ToLower(strings.TrimSpace(scopes))), "openid")
}

// firstNonEmpty returns the first value whose trimmed text is nonempty. It
// returns empty text when no candidate remains, allowing each caller to select
// its saved, deployment, or built-in default order.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// ensureActorTotpForStepUp checks the current actor before enabling step-up
// for sensitive operations. The mounting route supplies administrator authorization; this
// helper rejects management API keys, absent identity, unavailable user reads,
// and an actor without TOTP, writing the failure response and returning false.
func (h *Handler) ensureActorTotpForStepUp(c *gin.Context) bool {
	if c.GetString("auth_method") == audit.AuditAuthMethodAdminAPIKey {
		httpx.ErrorWithDetails(c, http.StatusForbidden,
			"Admin API key cannot enable step-up verification; use an admin session with TOTP enabled",
			"STEP_UP_ADMIN_API_KEY_FORBIDDEN", nil)
		return false
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		httpx.ErrorWithDetails(c, http.StatusForbidden,
			"Enabling step-up verification requires an authenticated admin session",
			"STEP_UP_ENABLE_REQUIRES_TOTP", nil)
		return false
	}
	if h.userService == nil {
		httpx.InternalError(c, "Step-up precondition check unavailable")
		return false
	}
	user, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return false
	}
	if !user.TotpEnabled {
		httpx.ErrorWithDetails(c, http.StatusBadRequest,
			"Enable two-factor authentication (TOTP) for your account before turning on step-up verification",
			"STEP_UP_ENABLE_REQUIRES_TOTP", nil)
		return false
	}
	return true
}

// buildSettingKeyByJSONName maps tagged non-pointer request fields to storage
// keys used by omission filtering. It skips pointers because UpdateSettings handles
// those fields through separate prior-value merging, and ignores fields with no
// usable JSON name before retaining their mapping.
//
// Most request names already match storage keys. smtp_from_email instead uses
// its declared alias so dropping an omitted sender field removes the actual
// SMTP storage key. The returned map is constructed at initialization from the
// request type, keeping later non-pointer additions included in omission detection
// without treating the struct's zero values as submitted input.
func buildSettingKeyByJSONName() map[string]string {
	t := reflect.TypeFor[UpdateSettingsRequest]()
	out := make(map[string]string, t.NumField())
	for field := range t.Fields() {
		if field.Type.Kind() == reflect.Pointer {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		if alias, ok := settingKeyJSONAliases[name]; ok {
			out[name] = alias
			continue
		}
		out[name] = name
	}
	return out
}

// omittedSettingKeys records storage keys whose tagged non-pointer names are
// absent from the raw request. JSON null is present and therefore does not add
// an omission entry; the decoded field and owning preparer determine its supplied
// value handling later in the update.
//
// This list is passed to broad preparation after normalization so missing typed
// fields cannot reset unrelated stored settings to zero. The precomputed mapping
// applies aliases such as smtp_from_email, while pointer fields follow their
// separate merging path. The returned set is newly allocated for this request
// and DropFrom mutates the eventual prepared value map.
func omittedSettingKeys(sentFields map[string]json.RawMessage) runtimesettings.OmittedKeys {
	omitted := make(runtimesettings.OmittedKeys, len(settingKeyByJSONName))
	for jsonName, settingKey := range settingKeyByJSONName {
		if _, sent := sentFields[jsonName]; !sent {
			omitted[settingKey] = struct{}{}
		}
	}
	return omitted
}

// SettingsAuditRequest returns a shallow request copy with Tencent credential
// text and Aliyun access-key secret trimmed for audit presence checks. Other
// fields retain their supplied values, including shared pointer and collection
// references; the handler captures this copy before credential fallback merging.
func SettingsAuditRequest(req UpdateSettingsRequest) UpdateSettingsRequest {
	req.TencentCaptchaAppSecretKey = strings.TrimSpace(req.TencentCaptchaAppSecretKey)
	req.TencentCaptchaCloudSecretID = strings.TrimSpace(req.TencentCaptchaCloudSecretID)
	req.TencentCaptchaCloudSecretKey = strings.TrimSpace(req.TencentCaptchaCloudSecretKey)
	req.AliyunCaptchaAccessKeySecret = strings.TrimSpace(req.AliyunCaptchaAccessKeySecret)
	return req
}

// UpdateSettings validates a partial administrator request, commits one combined
// batch, and returns its saved readback. It decodes raw presence before typed
// values, rejects retired fields before service access, and holds an update session
// from current-value reads through response construction.
//
// Pointer fields use specific prior-value/default selection, while omitted
// non-pointer keys are removed after normalization. The handler prepares every
// registered owner before Commit and replaces broad-map keys with those results.
// Changed credential probes and step-up transitions occur before persistence.
//
// Commit success precedes audit and final reads. Runtime application errors carry
// persisted=true from the session; response read failures can also follow saved
// settings and completed audit logging. Optional fast-policy read failures are
// logged, while payment readback failure stops PUT response construction. The
// caller supplies complete module dependencies before reaching their branches.
func (h *Handler) UpdateSettings(c *gin.Context) {
	var sentFields map[string]json.RawMessage
	if err := c.ShouldBindBodyWith(&sentFields, binding.JSON); err != nil {
		httpx.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	// Reject presence before loading settings so even false or null cannot make
	// clients infer that the retired automatic cross-model mapping option remains supported.
	if _, exists := sentFields["grok_cross_client_model_map_enabled"]; exists {
		httpx.BadRequest(c, "grok_cross_client_model_map_enabled has been removed; configure explicit model_mapping instead")
		return
	}
	for _, field := range []string{"site_name_zh", "site_name_en", "site_title_zh", "site_title_en", "site_subtitle_zh", "site_subtitle_en"} {
		if _, exists := sentFields[field]; exists {
			httpx.ErrorWithDetails(c, http.StatusBadRequest, "Use site_texts to edit translations.", "REMOVED_SETTING_FIELD", map[string]string{"field": field})
			return
		}
	}
	if rejectRemovedUngroupedKeySchedulingField(c, sentFields) || rejectRemovedPlatformQuotaFields(c, sentFields) || rejectDeprecatedAdvancedSchedulerRequestFields(c, sentFields) {
		return
	}
	var req UpdateSettingsRequest
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
		httpx.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	update, err := h.settingService.BeginSettingsUpdate(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	defer update.Close()
	c.Request = c.Request.WithContext(update.Context())

	// Normalize supplied selections through the installed model reader before
	// preparing the batch so unsupported operations can be removed against current
	// candidate capabilities.
	if req.CreativeModelSettings != nil {
		if sanitizer, ok := h.creativeModelReader.(interface {
			// NormalizeCreativeModelSettingsForSave returns selections after resolving
			// current candidate capabilities. The handler uses its normalized list before
			// module validation, and a returned error prevents preparing this update.
			NormalizeCreativeModelSettingsForSave(context.Context, []creative.CreativeModelSetting) ([]creative.CreativeModelSetting, error)
		}); ok {
			normalized, normalizeErr := sanitizer.NormalizeCreativeModelSettingsForSave(c.Request.Context(), *req.CreativeModelSettings)
			if normalizeErr != nil {
				httpx.BadRequest(c, "Invalid creative model settings: "+normalizeErr.Error())
				return
			}
			req.CreativeModelSettings = &normalized
		}
	}
	auditReq := SettingsAuditRequest(req)
	omitted := omittedSettingKeys(sentFields)

	previousSettings, err := h.settingService.GetAllSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	previousAuthSourceDefaults, err := h.settingService.GetAuthSourceDefaultSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	// Merge these pointer switches with prior state before preparing owned fields;
	// a supplied false remains distinct from nil so older partial requests retain the saved policy.
	registrationEmailDomainQuotaEnabled := previousSettings.RegistrationEmailDomainQuotaEnabled
	if req.RegistrationEmailDomainQuotaEnabled != nil {
		registrationEmailDomainQuotaEnabled = *req.RegistrationEmailDomainQuotaEnabled
	}
	userEmailChangeEnabled := previousSettings.UserEmailChangeEnabled
	if req.UserEmailChangeEnabled != nil {
		userEmailChangeEnabled = *req.UserEmailChangeEnabled
	}
	// Retain prior session-binding and step-up state for nil inputs so a partial
	// save cannot request a security transition through the struct's decoded zero values.
	sessionBindingEnabled := previousSettings.SessionBindingEnabled
	if req.SessionBindingEnabled != nil {
		sessionBindingEnabled = *req.SessionBindingEnabled
	}
	stepUpEnabled := previousSettings.StepUpEnabled
	if req.StepUpEnabled != nil {
		stepUpEnabled = *req.StepUpEnabled
	}
	forwardedClientIPHeaders := append([]string(nil), previousSettings.ForwardedClientIPHeaders...)
	if req.ForwardedClientIPHeaders != nil {
		forwardedClientIPHeaders = append([]string(nil), *req.ForwardedClientIPHeaders...)
	}

	// Enabling step-up requires the actor's own TOTP to prevent locking the administrator out
	// of sensitive operations. Check the precondition on the false-to-true transition;
	// routine saves with step-up already enabled do not require this check.
	if stepUpEnabled && !previousSettings.StepUpEnabled {
		if !h.ensureActorTotpForStepUp(c) {
			return
		}
	}
	// Disabling step-up requires verification before a session can remove that
	// protection from later sensitive operations.
	// The previous snapshot establishes that the switch is enabled. Use the unconditional check
	// so a second settings read cannot fail open; the frontend handles STEP_UP_REQUIRED and retries.
	if !stepUpEnabled && previousSettings.StepUpEnabled {
		if !identityhttp.EnforceStepUp(c, h.totpService, h.userService, nil) {
			return
		}
	}

	// Normalize registration grants before module preparation: concurrency keeps
	// a usable minimum allowance, and a negative default balance becomes a zero grant.
	if req.DefaultConcurrency < 1 {
		req.DefaultConcurrency = 1
	}
	if req.DefaultBalance < 0 {
		req.DefaultBalance = 0
	}
	if req.DefaultUserAPIKeyLimit != nil && !identity.IsValidUserAPIKeyLimit(*req.DefaultUserAPIKeyLimit) {
		httpx.ErrorFrom(c, identity.ErrUserAPIKeyLimitInvalid)
		return
	}
	normalizedPromotion := promotion.NormalizeAdminSettings(promotion.AdminSettings{AffiliateRebateRate: req.AffiliateRebateRate, AffiliateRebateFreezeHours: req.AffiliateRebateFreezeHours, AffiliateRebateDurationDays: req.AffiliateRebateDurationDays, AffiliateRebatePerInviteeCap: req.AffiliateRebatePerInviteeCap})
	req.AffiliateRebateRate = normalizedPromotion.AffiliateRebateRate
	req.AffiliateRebateFreezeHours = normalizedPromotion.AffiliateRebateFreezeHours
	req.AffiliateRebateDurationDays = normalizedPromotion.AffiliateRebateDurationDays
	req.AffiliateRebatePerInviteeCap = normalizedPromotion.AffiliateRebatePerInviteeCap
	// Clamp supplied negative conversion values to zero, which the marketplace
	// reader treats as unavailable official-price conversion rather than a usable positive rate.
	if req.ReasoningPointRMBUnitPrice != nil && *req.ReasoningPointRMBUnitPrice < 0 {
		*req.ReasoningPointRMBUnitPrice = 0
	}
	if req.USDExchangeRate != nil && *req.USDExchangeRate < 0 {
		*req.USDExchangeRate = 0
	}
	adminRechargeRebateEnabled := previousSettings.AdminRechargeRebateEnabled
	if req.AdminRechargeRebateEnabled != nil {
		adminRechargeRebateEnabled = *req.AdminRechargeRebateEnabled
	}
	// Keep prior table choices when older clients supply no usable row count
	// or omit the list, leaving the owning site normalizer to select effective preferences.
	if req.TableDefaultPageSize <= 0 {
		req.TableDefaultPageSize = previousSettings.TableDefaultPageSize
	}
	if req.TablePageSizeOptions == nil {
		req.TablePageSizeOptions = previousSettings.TablePageSizeOptions
	}
	// Resolve ranking fields together so the selected sorting metric remains
	// visible after partial merging, then retain that normalized result for participant preparation.
	usageRanking, rankingErr := usage.ResolveRankingSettings(usage.UsageRankingSettings{
		Enabled: previousSettings.UsageRankingEnabled, SortBy: usage.UsageRankingSortBy(previousSettings.UsageRankingSortBy), ShowTotalTokens: previousSettings.UsageRankingShowTotalTokens, ShowRequests: previousSettings.UsageRankingShowRequests, ShowActualCost: previousSettings.UsageRankingShowActualCost, Limit: previousSettings.UsageRankingLimit,
	}, usage.RankingSettingsUpdate{Limit: req.UsageRankingLimit, Enabled: req.UsageRankingEnabled, SortBy: req.UsageRankingSortBy, ShowTotalTokens: req.UsageRankingShowTotalTokens, ShowRequests: req.UsageRankingShowRequests, ShowActualCost: req.UsageRankingShowActualCost})
	if rankingErr != nil {
		httpx.BadRequest(c, rankingErr.Error())
		return
	}
	req.UsageRankingLimit = usageRanking.Limit
	// Apply SMTP's separate empty-host and password retention rules before
	// serialization so partial forms can retain their saved connection and authentication settings.
	smtp := notification.NormalizeAdminSMTPSettings(notification.AdminSMTPSettings{SMTPHost: previousSettings.SMTPHost, SMTPPort: previousSettings.SMTPPort, SMTPUsername: previousSettings.SMTPUsername, SMTPPassword: previousSettings.SMTPPassword, SMTPFrom: previousSettings.SMTPFrom, SMTPFromName: previousSettings.SMTPFromName, SMTPUseTLS: previousSettings.SMTPUseTLS}, notification.AdminSMTPSettings{SMTPHost: req.SMTPHost, SMTPPort: req.SMTPPort, SMTPUsername: req.SMTPUsername, SMTPPassword: req.SMTPPassword, SMTPFrom: req.SMTPFrom, SMTPFromName: req.SMTPFromName, SMTPUseTLS: req.SMTPUseTLS})
	req.SMTPHost = smtp.SMTPHost
	req.SMTPPort = smtp.SMTPPort
	req.SMTPUsername = smtp.SMTPUsername
	req.SMTPPassword = smtp.SMTPPassword
	req.SMTPFrom = smtp.SMTPFrom
	req.SMTPFromName = smtp.SMTPFromName
	req.SMTPUseTLS = smtp.SMTPUseTLS
	req.BalanceUnitName = strings.TrimSpace(req.BalanceUnitName)
	req.BalanceUnitSymbol = strings.TrimSpace(req.BalanceUnitSymbol)
	req.BalanceIconSVG = strings.TrimSpace(req.BalanceIconSVG)
	if req.BalanceUnitName == "" {
		req.BalanceUnitName = "USD"
	}
	if req.BalanceUnitSymbol == "" {
		req.BalanceUnitSymbol = "$"
	}
	req.TencentCaptchaAppID = strings.TrimSpace(req.TencentCaptchaAppID)
	req.TencentCaptchaAppSecretKey = strings.TrimSpace(req.TencentCaptchaAppSecretKey)
	req.TencentCaptchaCloudSecretID = strings.TrimSpace(req.TencentCaptchaCloudSecretID)
	req.TencentCaptchaCloudSecretKey = strings.TrimSpace(req.TencentCaptchaCloudSecretKey)
	req.DefaultSubscriptions = normalizeDefaultSubscriptions(req.DefaultSubscriptions)
	req.AuthSourceDefaultEmailSubscriptions = normalizeOptionalDefaultSubscriptions(req.AuthSourceDefaultEmailSubscriptions)
	req.AuthSourceDefaultLinuxDoSubscriptions = normalizeOptionalDefaultSubscriptions(req.AuthSourceDefaultLinuxDoSubscriptions)
	req.AuthSourceDefaultOIDCSubscriptions = normalizeOptionalDefaultSubscriptions(req.AuthSourceDefaultOIDCSubscriptions)
	req.AuthSourceDefaultWeChatSubscriptions = normalizeOptionalDefaultSubscriptions(req.AuthSourceDefaultWeChatSubscriptions)
	req.AuthSourceDefaultGitHubSubscriptions = normalizeOptionalDefaultSubscriptions(req.AuthSourceDefaultGitHubSubscriptions)
	req.AuthSourceDefaultGoogleSubscriptions = normalizeOptionalDefaultSubscriptions(req.AuthSourceDefaultGoogleSubscriptions)
	req.AuthSourceDefaultDingTalkSubscriptions = normalizeOptionalDefaultSubscriptions(req.AuthSourceDefaultDingTalkSubscriptions)

	turnstileEnabled := req.TurnstileEnabled
	if _, sent := sentFields["turnstile_enabled"]; !sent {
		turnstileEnabled = previousSettings.TurnstileEnabled
	}
	tencentCaptchaEnabled := req.TencentCaptchaEnabled
	if _, sent := sentFields["tencent_captcha_enabled"]; !sent {
		tencentCaptchaEnabled = previousSettings.TencentCaptchaEnabled
	}
	aliyunCaptchaEnabled := req.AliyunCaptchaEnabled
	if _, sent := sentFields["aliyun_captcha_enabled"]; !sent {
		aliyunCaptchaEnabled = previousSettings.AliyunCaptchaEnabled
	}
	// Count saved-or-submitted switches together so a partial request cannot
	// enable another challenge provider while an omitted provider remains enabled.
	enabledCaptchaProviders := 0
	for _, enabled := range []bool{turnstileEnabled, tencentCaptchaEnabled, aliyunCaptchaEnabled} {
		if enabled {
			enabledCaptchaProviders++
		}
	}
	if enabledCaptchaProviders > 1 {
		httpx.BadRequest(c, "Multiple captcha providers (Cloudflare Turnstile / Tencent Captcha / Aliyun Captcha) cannot be enabled at the same time")
		return
	}
	// Use the saved Aliyun region when omitted, then select cn for every value
	// except sgp so client configuration and verifier selection share the supported region choice.
	if _, sent := sentFields["aliyun_captcha_region"]; !sent {
		req.AliyunCaptchaRegion = previousSettings.AliyunCaptchaRegion
	}
	if req.AliyunCaptchaRegion != identity.AliyunCaptchaRegionSGP {
		req.AliyunCaptchaRegion = identity.AliyunCaptchaRegionCN
	}
	// Use the saved Tencent region when omitted, then select cn for every value
	// except intl so the widget and backend verifier retain the same supported site selection.
	if _, sent := sentFields["tencent_captcha_region"]; !sent {
		req.TencentCaptchaRegion = previousSettings.TencentCaptchaRegion
	}
	if req.TencentCaptchaRegion != identity.TencentCaptchaRegionINTL {
		req.TencentCaptchaRegion = identity.TencentCaptchaRegionCN
	}

	// Validate enabled Turnstile configuration.
	if req.TurnstileEnabled {
		if req.TurnstileSiteKey == "" {
			httpx.BadRequest(c, "Turnstile Site Key is required when enabled")
			return
		}
		// Empty submitted secret text reuses the saved credential before probing
		// an enabled changed configuration, keeping an unchanged masked form usable for preparation.
		if req.TurnstileSecretKey == "" {
			if previousSettings.TurnstileSecretKey == "" {
				httpx.BadRequest(c, "Turnstile Secret Key is required when enabled")
				return
			}
			req.TurnstileSecretKey = previousSettings.TurnstileSecretKey
		}

		// Either site-key or secret change triggers a secret probe before persistence.
		// The validator receives the secret alone. Its failure stops this batch before
		// the saved login settings can be replaced.
		siteKeyChanged := previousSettings.TurnstileSiteKey != req.TurnstileSiteKey
		secretKeyChanged := previousSettings.TurnstileSecretKey != req.TurnstileSecretKey
		if siteKeyChanged || secretKeyChanged {
			if err := h.turnstileService.ValidateSecretKey(c.Request.Context(), req.TurnstileSecretKey); err != nil {
				httpx.ErrorFrom(c, err)
				return
			}
		}
	}

	// Effective Tencent enablement retains public IDs and blank credentials from
	// saved settings before validating this region's complete application/cloud tuple.
	if tencentCaptchaEnabled {
		if _, sent := sentFields["tencent_captcha_app_id"]; !sent {
			req.TencentCaptchaAppID = previousSettings.TencentCaptchaAppID
		}
		// Treat a zero parsed value as a missing CAPTCHA application identifier.
		appID, err := strconv.ParseUint(req.TencentCaptchaAppID, 10, 64)
		if err != nil || appID == 0 {
			httpx.BadRequest(c, "Tencent Captcha CaptchaAppId must be a positive integer when enabled")
			return
		}
		if req.TencentCaptchaAppSecretKey == "" {
			req.TencentCaptchaAppSecretKey = previousSettings.TencentCaptchaAppSecretKey
		}
		if req.TencentCaptchaCloudSecretID == "" {
			req.TencentCaptchaCloudSecretID = previousSettings.TencentCaptchaCloudSecretID
		}
		if req.TencentCaptchaCloudSecretKey == "" {
			req.TencentCaptchaCloudSecretKey = previousSettings.TencentCaptchaCloudSecretKey
		}
		if req.TencentCaptchaAppSecretKey == "" {
			httpx.BadRequest(c, "Tencent Captcha AppSecretKey is required when enabled")
			return
		}
		if req.TencentCaptchaCloudSecretID == "" {
			httpx.BadRequest(c, "Tencent Cloud SecretId is required when Tencent Captcha is enabled")
			return
		}
		if req.TencentCaptchaCloudSecretKey == "" {
			httpx.BadRequest(c, "Tencent Cloud SecretKey is required when Tencent Captcha is enabled")
			return
		}
	}

	// Retain omitted public Aliyun fields before validating effective enablement,
	// so a partial request is checked against the saved scene, prefix, and access-key ID.
	if aliyunCaptchaEnabled {
		if _, sent := sentFields["aliyun_captcha_scene_id"]; !sent {
			req.AliyunCaptchaSceneID = previousSettings.AliyunCaptchaSceneID
		}
		if _, sent := sentFields["aliyun_captcha_prefix"]; !sent {
			req.AliyunCaptchaPrefix = previousSettings.AliyunCaptchaPrefix
		}
		if _, sent := sentFields["aliyun_captcha_access_key_id"]; !sent {
			req.AliyunCaptchaAccessKeyID = previousSettings.AliyunCaptchaAccessKeyID
		}
		if req.AliyunCaptchaSceneID == "" {
			httpx.BadRequest(c, "Aliyun Captcha Scene ID is required when enabled")
			return
		}
		if req.AliyunCaptchaPrefix == "" {
			httpx.BadRequest(c, "Aliyun Captcha Prefix is required when enabled")
			return
		}
		if req.AliyunCaptchaAccessKeyID == "" {
			httpx.BadRequest(c, "Aliyun Captcha AccessKey ID is required when enabled")
			return
		}
		// Empty access-secret text retains the saved value so a routine settings
		// form can edit public scene or region fields while its credential stays available.
		if req.AliyunCaptchaAccessKeySecret == "" {
			if previousSettings.AliyunCaptchaAccessKeySecret == "" {
				httpx.BadRequest(c, "Aliyun Captcha AccessKey Secret is required when enabled")
				return
			}
			req.AliyunCaptchaAccessKeySecret = previousSettings.AliyunCaptchaAccessKeySecret
		}

		// Probe a changed access-key, scene, or region tuple before batch persistence
		// so verifier failures are reported while the previously saved configuration remains available.
		credentialsChanged := previousSettings.AliyunCaptchaAccessKeyID != req.AliyunCaptchaAccessKeyID ||
			previousSettings.AliyunCaptchaAccessKeySecret != req.AliyunCaptchaAccessKeySecret ||
			previousSettings.AliyunCaptchaSceneID != req.AliyunCaptchaSceneID ||
			previousSettings.AliyunCaptchaRegion != req.AliyunCaptchaRegion
		if credentialsChanged {
			if err := h.aliyunCaptchaService.ValidateCredentials(c.Request.Context(), req.AliyunCaptchaAccessKeyID, req.AliyunCaptchaAccessKeySecret, req.AliyunCaptchaSceneID, req.AliyunCaptchaRegion); err != nil {
				httpx.ErrorFrom(c, err)
				return
			}
		}
	}

	// Enabling TOTP requires a configured encryption key so stored credentials
	// can be decrypted after a later process restart.
	if req.TotpEnabled && !previousSettings.TotpEnabled {
		if !h.settingService.IsTotpEncryptionKeyConfigured() {
			httpx.BadRequest(c, "Cannot enable TOTP: configure a persistent security.secret_encryption_key in config.yaml first. Generate a key with 'openssl rand -hex 32'.")
			return
		}
	}
	// Keep the saved consent mode and update marker when submitted text is empty,
	// then validate the selected mode before legal documents enter module preparation.
	loginAgreementMode := strings.ToLower(strings.TrimSpace(req.LoginAgreementMode))
	if loginAgreementMode == "" {
		loginAgreementMode = strings.ToLower(strings.TrimSpace(previousSettings.LoginAgreementMode))
	}
	switch loginAgreementMode {
	case "", "modal":
		loginAgreementMode = "modal"
	case "checkbox":
	default:
		httpx.BadRequest(c, "Login agreement mode must be modal or checkbox")
		return
	}
	loginAgreementUpdatedAt := strings.TrimSpace(req.LoginAgreementUpdatedAt)
	if loginAgreementUpdatedAt == "" {
		loginAgreementUpdatedAt = strings.TrimSpace(previousSettings.LoginAgreementUpdatedAt)
	}
	// Select editor source content before legal validation, then retain prior
	// documents when conversion yields an empty list, including an explicitly supplied empty list.
	loginAgreementDocuments := loginAgreementDocumentsToService(req.LoginAgreementDocuments)
	if len(loginAgreementDocuments) == 0 {
		loginAgreementDocuments = previousSettings.LoginAgreementDocuments
	}
	for _, doc := range loginAgreementDocuments {
		if strings.TrimSpace(doc.Title) == "" {
			httpx.BadRequest(c, "Login agreement document title is required")
			return
		}
		// Limit the stored title by UTF-8 bytes before it enters legal preparation.
		if len(doc.Title) > 80 {
			httpx.BadRequest(c, "Login agreement document title is too long (max 80 characters)")
			return
		}
		// Bound each submitted Markdown body to two hundred kibibytes.
		if len(doc.ContentMD) > 200*1024 {
			httpx.BadRequest(c, "Login agreement document content is too large (max 200KB)")
			return
		}
	}
	if req.LoginAgreementEnabled && len(loginAgreementDocuments) == 0 {
		httpx.BadRequest(c, "Login agreement documents are required when enabled")
		return
	}

	// Validate enabled LinuxDo Connect configuration.
	if req.LinuxDoConnectEnabled {
		req.LinuxDoConnectClientID = strings.TrimSpace(req.LinuxDoConnectClientID)
		req.LinuxDoConnectClientSecret = strings.TrimSpace(req.LinuxDoConnectClientSecret)
		req.LinuxDoConnectRedirectURL = strings.TrimSpace(req.LinuxDoConnectRedirectURL)

		if req.LinuxDoConnectClientID == "" {
			httpx.BadRequest(c, "LinuxDo Client ID is required when enabled")
			return
		}
		if req.LinuxDoConnectRedirectURL == "" {
			httpx.BadRequest(c, "LinuxDo Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(req.LinuxDoConnectRedirectURL); err != nil {
			httpx.BadRequest(c, "LinuxDo Redirect URL must be an absolute http(s) URL")
			return
		}

		// Empty secret text retains the saved credential for this enabled provider
		// before the normalized request is handed to identity's settings participant.
		if req.LinuxDoConnectClientSecret == "" {
			if previousSettings.LinuxDoConnectClientSecret == "" {
				httpx.BadRequest(c, "LinuxDo Client Secret is required when enabled")
				return
			}
			req.LinuxDoConnectClientSecret = previousSettings.LinuxDoConnectClientSecret
		}
	}

	// Coerce the retired corp_restriction_policy=whitelist value to none
	// so direct API clients cannot write the unsupported policy back to storage.
	req.DingTalkConnectCorpRestrictionPolicy = identity.SettingsCoerceDingTalkCorpPolicyForWrite(req.DingTalkConnectCorpRestrictionPolicy)

	if req.DingTalkConnectEnabled {
		req.DingTalkConnectClientID = strings.TrimSpace(req.DingTalkConnectClientID)
		req.DingTalkConnectClientSecret = strings.TrimSpace(req.DingTalkConnectClientSecret)
		req.DingTalkConnectRedirectURL = strings.TrimSpace(req.DingTalkConnectRedirectURL)
		req.DingTalkConnectCorpRestrictionPolicy = strings.TrimSpace(req.DingTalkConnectCorpRestrictionPolicy)
		req.DingTalkConnectInternalCorpID = strings.TrimSpace(req.DingTalkConnectInternalCorpID)

		if req.DingTalkConnectClientID == "" {
			httpx.BadRequest(c, "DingTalk Client ID is required when enabled")
			return
		}
		if req.DingTalkConnectRedirectURL == "" {
			httpx.BadRequest(c, "DingTalk Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(req.DingTalkConnectRedirectURL); err != nil {
			httpx.BadRequest(c, "DingTalk Redirect URL must be an absolute http(s) URL")
			return
		}

		// Empty secret text retains the saved credential for this enabled provider
		// before the normalized request is handed to identity's settings participant.
		if req.DingTalkConnectClientSecret == "" {
			if previousSettings.DingTalkConnectClientSecret == "" {
				httpx.BadRequest(c, "DingTalk Client Secret is required when enabled")
				return
			}
			req.DingTalkConnectClientSecret = previousSettings.DingTalkConnectClientSecret
		}

		// Validate the corporation policy before persisting it.
		dingTalkCfg := authconfig.DingTalkConnectConfig{
			Enabled:               true,
			DingTalkAppKind:       "internal_app", // the settings API supports internal_app
			AppType:               "internal",     // default for internal_only
			CorpRestrictionPolicy: req.DingTalkConnectCorpRestrictionPolicy,
			InternalCorpID:        req.DingTalkConnectInternalCorpID,
		}
		// Validate against the prior policy when submitted text is empty so app-type
		// checks use the policy that the current settings read supplied for this provider.
		if dingTalkCfg.CorpRestrictionPolicy == "" {
			dingTalkCfg.CorpRestrictionPolicy = previousSettings.DingTalkConnectCorpRestrictionPolicy
		}
		// Select the validation app type from the resolved policy: internal_only
		// requires internal while this settings API supports the internal_app application kind.
		if dingTalkCfg.CorpRestrictionPolicy == "internal_only" {
			dingTalkCfg.AppType = "internal"
		} else {
			dingTalkCfg.AppType = "public"
		}
		if err := authconfig.ValidateDingTalkConfig(dingTalkCfg); err != nil {
			httpx.ErrorWithDetails(c, http.StatusBadRequest, err.Error(), mapDingTalkValidateError(err), nil)
			return
		}

		// Clear bypass and all profile-sync switches outside internal_only so a
		// changed organization policy cannot retain those internal-organization permissions.
		if dingTalkCfg.CorpRestrictionPolicy != "internal_only" {
			req.DingTalkConnectBypassRegistration = false
			req.DingTalkConnectSyncCorpEmail = false
			req.DingTalkConnectSyncDisplayName = false
			req.DingTalkConnectSyncDept = false
		}
		// Fill empty attribute-key inputs within every enabled DingTalk request,
		// including requests whose sync switches are false. Presence filtering later
		// decides which computed keys reach storage; post-commit setup reads saved
		// keys again before preparing enabled attribute definitions.
		req.DingTalkConnectSyncCorpEmailAttrKey = strings.TrimSpace(req.DingTalkConnectSyncCorpEmailAttrKey)
		if req.DingTalkConnectSyncCorpEmailAttrKey == "" {
			req.DingTalkConnectSyncCorpEmailAttrKey = "dingtalk_email"
		}
		req.DingTalkConnectSyncDisplayNameAttrKey = strings.TrimSpace(req.DingTalkConnectSyncDisplayNameAttrKey)
		if req.DingTalkConnectSyncDisplayNameAttrKey == "" {
			req.DingTalkConnectSyncDisplayNameAttrKey = "dingtalk_name"
		}
		req.DingTalkConnectSyncDeptAttrKey = strings.TrimSpace(req.DingTalkConnectSyncDeptAttrKey)
		if req.DingTalkConnectSyncDeptAttrKey == "" {
			req.DingTalkConnectSyncDeptAttrKey = "dingtalk_department"
		}
		// Fill empty display-label inputs within this enabled-provider branch.
		// Presence filtering can discard these defaults; definition setup uses the
		// labels returned by committed readback when sync flags select that work.
		req.DingTalkConnectSyncCorpEmailAttrName = strings.TrimSpace(req.DingTalkConnectSyncCorpEmailAttrName)
		if req.DingTalkConnectSyncCorpEmailAttrName == "" {
			req.DingTalkConnectSyncCorpEmailAttrName = "钉钉企业邮箱"
		}
		req.DingTalkConnectSyncDisplayNameAttrName = strings.TrimSpace(req.DingTalkConnectSyncDisplayNameAttrName)
		if req.DingTalkConnectSyncDisplayNameAttrName == "" {
			req.DingTalkConnectSyncDisplayNameAttrName = "钉钉姓名"
		}
		req.DingTalkConnectSyncDeptAttrName = strings.TrimSpace(req.DingTalkConnectSyncDeptAttrName)
		if req.DingTalkConnectSyncDeptAttrName == "" {
			req.DingTalkConnectSyncDeptAttrName = "钉钉部门"
		}
	}

	if req.WeChatConnectEnabled {
		req.WeChatConnectAppID = strings.TrimSpace(req.WeChatConnectAppID)
		req.WeChatConnectAppSecret = strings.TrimSpace(req.WeChatConnectAppSecret)
		req.WeChatConnectOpenAppID = strings.TrimSpace(req.WeChatConnectOpenAppID)
		req.WeChatConnectOpenAppSecret = strings.TrimSpace(req.WeChatConnectOpenAppSecret)
		req.WeChatConnectMPAppID = strings.TrimSpace(req.WeChatConnectMPAppID)
		req.WeChatConnectMPAppSecret = strings.TrimSpace(req.WeChatConnectMPAppSecret)
		req.WeChatConnectMobileAppID = strings.TrimSpace(req.WeChatConnectMobileAppID)
		req.WeChatConnectMobileAppSecret = strings.TrimSpace(req.WeChatConnectMobileAppSecret)
		req.WeChatConnectMode = strings.ToLower(strings.TrimSpace(req.WeChatConnectMode))
		req.WeChatConnectScopes = strings.TrimSpace(req.WeChatConnectScopes)
		req.WeChatConnectRedirectURL = strings.TrimSpace(req.WeChatConnectRedirectURL)
		req.WeChatConnectFrontendRedirectURL = strings.TrimSpace(req.WeChatConnectFrontendRedirectURL)
		// Retain prior legacy application and callback text when enabled-client input
		// is blank, before channel-specific credentials and web callback validation are selected.
		req.WeChatConnectAppID = strings.TrimSpace(firstNonEmpty(req.WeChatConnectAppID, previousSettings.WeChatConnectAppID))
		req.WeChatConnectRedirectURL = strings.TrimSpace(firstNonEmpty(req.WeChatConnectRedirectURL, previousSettings.WeChatConnectRedirectURL))
		req.WeChatConnectFrontendRedirectURL = strings.TrimSpace(firstNonEmpty(req.WeChatConnectFrontendRedirectURL, previousSettings.WeChatConnectFrontendRedirectURL))
		if req.WeChatConnectMode == "" {
			req.WeChatConnectMode = strings.ToLower(strings.TrimSpace(previousSettings.WeChatConnectMode))
		}
		if req.WeChatConnectScopes == "" {
			req.WeChatConnectScopes = strings.TrimSpace(previousSettings.WeChatConnectScopes)
		}

		if req.WeChatConnectMPEnabled && req.WeChatConnectMobileEnabled {
			httpx.BadRequest(c, "WeChat Official Account and Mobile App cannot be enabled at the same time")
			return
		}
		if req.WeChatConnectMode != "" {
			switch req.WeChatConnectMode {
			case "open", "mp", "mobile":
			default:
				httpx.BadRequest(c, "WeChat mode must be open, mp, or mobile")
				return
			}
		}
		// Legacy mode input selects a channel when all decoded channel switches
		// are false, including explicitly submitted false values. A missing mode is
		// then derived from enabled request channels before credentials are chosen.
		if !req.WeChatConnectOpenEnabled && !req.WeChatConnectMPEnabled && !req.WeChatConnectMobileEnabled {
			switch req.WeChatConnectMode {
			case "mp":
				req.WeChatConnectMPEnabled = true
			case "mobile":
				req.WeChatConnectMobileEnabled = true
			default:
				req.WeChatConnectOpenEnabled = true
			}
		}
		if req.WeChatConnectMode == "" {
			if req.WeChatConnectMPEnabled {
				req.WeChatConnectMode = "mp"
			} else if req.WeChatConnectMobileEnabled {
				req.WeChatConnectMode = "mobile"
			} else {
				req.WeChatConnectMode = "open"
			}
		}

		// Each channel tries submitted channel ID, resolved legacy ID, saved channel
		// ID, then saved legacy ID before identity's final preparation receives these selected values.
		req.WeChatConnectOpenAppID = strings.TrimSpace(firstNonEmpty(req.WeChatConnectOpenAppID, req.WeChatConnectAppID, previousSettings.WeChatConnectOpenAppID, previousSettings.WeChatConnectAppID))
		req.WeChatConnectMPAppID = strings.TrimSpace(firstNonEmpty(req.WeChatConnectMPAppID, req.WeChatConnectAppID, previousSettings.WeChatConnectMPAppID, previousSettings.WeChatConnectAppID))
		req.WeChatConnectMobileAppID = strings.TrimSpace(firstNonEmpty(req.WeChatConnectMobileAppID, req.WeChatConnectAppID, previousSettings.WeChatConnectMobileAppID, previousSettings.WeChatConnectAppID))

		// Blank channel secrets reuse saved channel and legacy credentials before
		// submitted legacy text; a blank legacy secret is then filled from the resolved channels.
		if req.WeChatConnectOpenAppSecret == "" {
			req.WeChatConnectOpenAppSecret = strings.TrimSpace(firstNonEmpty(previousSettings.WeChatConnectOpenAppSecret, previousSettings.WeChatConnectAppSecret, req.WeChatConnectAppSecret))
		}
		if req.WeChatConnectMPAppSecret == "" {
			req.WeChatConnectMPAppSecret = strings.TrimSpace(firstNonEmpty(previousSettings.WeChatConnectMPAppSecret, previousSettings.WeChatConnectAppSecret, req.WeChatConnectAppSecret))
		}
		if req.WeChatConnectMobileAppSecret == "" {
			req.WeChatConnectMobileAppSecret = strings.TrimSpace(firstNonEmpty(previousSettings.WeChatConnectMobileAppSecret, previousSettings.WeChatConnectAppSecret, req.WeChatConnectAppSecret))
		}
		if req.WeChatConnectAppSecret == "" {
			req.WeChatConnectAppSecret = strings.TrimSpace(firstNonEmpty(req.WeChatConnectOpenAppSecret, req.WeChatConnectMPAppSecret, req.WeChatConnectMobileAppSecret, previousSettings.WeChatConnectAppSecret))
		}

		if req.WeChatConnectOpenEnabled {
			if req.WeChatConnectOpenAppID == "" {
				httpx.BadRequest(c, "WeChat PC App ID is required when enabled")
				return
			}
			if req.WeChatConnectOpenAppSecret == "" {
				httpx.BadRequest(c, "WeChat PC App Secret is required when enabled")
				return
			}
		}
		if req.WeChatConnectMPEnabled {
			if req.WeChatConnectMPAppID == "" {
				httpx.BadRequest(c, "WeChat Official Account App ID is required when enabled")
				return
			}
			if req.WeChatConnectMPAppSecret == "" {
				httpx.BadRequest(c, "WeChat Official Account App Secret is required when enabled")
				return
			}
		}
		if req.WeChatConnectMobileEnabled {
			if req.WeChatConnectMobileAppID == "" {
				httpx.BadRequest(c, "WeChat Mobile App ID is required when enabled")
				return
			}
			if req.WeChatConnectMobileAppSecret == "" {
				httpx.BadRequest(c, "WeChat Mobile App Secret is required when enabled")
				return
			}
		}

		// Choose scope defaults after submitted and saved text are empty. Public-
		// account enablement selects mp defaults; otherwise the resolved mode supplies that choice.
		if req.WeChatConnectScopes == "" {
			if req.WeChatConnectMPEnabled {
				req.WeChatConnectScopes = identity.SettingsDefaultWeChatConnectScopesForMode("mp")
			} else {
				req.WeChatConnectScopes = identity.SettingsDefaultWeChatConnectScopesForMode(req.WeChatConnectMode)
			}
		}
		if req.WeChatConnectOpenEnabled || req.WeChatConnectMPEnabled {
			if req.WeChatConnectRedirectURL == "" {
				httpx.BadRequest(c, "WeChat Redirect URL is required when web oauth is enabled")
				return
			}
			if err := authconfig.ValidateAbsoluteHTTPURL(req.WeChatConnectRedirectURL); err != nil {
				httpx.BadRequest(c, "WeChat Redirect URL must be an absolute http(s) URL")
				return
			}
			// Select the built-in callback path when the frontend destination is blank
			// so enabled web OAuth has a supported completion route before redirect validation.
			if req.WeChatConnectFrontendRedirectURL == "" {
				req.WeChatConnectFrontendRedirectURL = "/auth/wechat/callback"
			}
			if err := authconfig.ValidateFrontendRedirectURL(req.WeChatConnectFrontendRedirectURL); err != nil {
				httpx.BadRequest(c, "WeChat Frontend Redirect URL is invalid")
				return
			}
		}
	}

	// Read presence-aware OIDC write defaults on every save before the enabled
	// branch applies its optional pointer overrides and validates the current provider settings.
	oidcUsePKCE, oidcValidateIDToken, err := h.settingService.OIDCSecurityWriteDefaults(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	if req.OIDCConnectEnabled {
		req.OIDCConnectProviderName = strings.TrimSpace(req.OIDCConnectProviderName)
		req.OIDCConnectClientID = strings.TrimSpace(req.OIDCConnectClientID)
		req.OIDCConnectClientSecret = strings.TrimSpace(req.OIDCConnectClientSecret)
		req.OIDCConnectIssuerURL = strings.TrimSpace(req.OIDCConnectIssuerURL)
		req.OIDCConnectDiscoveryURL = strings.TrimSpace(req.OIDCConnectDiscoveryURL)
		req.OIDCConnectAuthorizeURL = strings.TrimSpace(req.OIDCConnectAuthorizeURL)
		req.OIDCConnectTokenURL = strings.TrimSpace(req.OIDCConnectTokenURL)
		req.OIDCConnectUserInfoURL = strings.TrimSpace(req.OIDCConnectUserInfoURL)
		req.OIDCConnectJWKSURL = strings.TrimSpace(req.OIDCConnectJWKSURL)
		req.OIDCConnectScopes = strings.TrimSpace(req.OIDCConnectScopes)
		req.OIDCConnectRedirectURL = strings.TrimSpace(req.OIDCConnectRedirectURL)
		req.OIDCConnectFrontendRedirectURL = strings.TrimSpace(req.OIDCConnectFrontendRedirectURL)
		req.OIDCConnectTokenAuthMethod = strings.ToLower(strings.TrimSpace(req.OIDCConnectTokenAuthMethod))
		req.OIDCConnectAllowedSigningAlgs = strings.TrimSpace(req.OIDCConnectAllowedSigningAlgs)
		req.OIDCConnectUserInfoEmailPath = strings.TrimSpace(req.OIDCConnectUserInfoEmailPath)
		req.OIDCConnectUserInfoIDPath = strings.TrimSpace(req.OIDCConnectUserInfoIDPath)
		req.OIDCConnectUserInfoUsernamePath = strings.TrimSpace(req.OIDCConnectUserInfoUsernamePath)
		// Enabled-provider text uses submitted, saved, then applicable built-in
		// candidates; blank input retains prior endpoint and profile choices in this branch.
		req.OIDCConnectProviderName = strings.TrimSpace(firstNonEmpty(req.OIDCConnectProviderName, previousSettings.OIDCConnectProviderName, "OIDC"))
		req.OIDCConnectClientID = strings.TrimSpace(firstNonEmpty(req.OIDCConnectClientID, previousSettings.OIDCConnectClientID))
		req.OIDCConnectIssuerURL = strings.TrimSpace(firstNonEmpty(req.OIDCConnectIssuerURL, previousSettings.OIDCConnectIssuerURL))
		req.OIDCConnectDiscoveryURL = strings.TrimSpace(firstNonEmpty(req.OIDCConnectDiscoveryURL, previousSettings.OIDCConnectDiscoveryURL))
		req.OIDCConnectAuthorizeURL = strings.TrimSpace(firstNonEmpty(req.OIDCConnectAuthorizeURL, previousSettings.OIDCConnectAuthorizeURL))
		req.OIDCConnectTokenURL = strings.TrimSpace(firstNonEmpty(req.OIDCConnectTokenURL, previousSettings.OIDCConnectTokenURL))
		req.OIDCConnectUserInfoURL = strings.TrimSpace(firstNonEmpty(req.OIDCConnectUserInfoURL, previousSettings.OIDCConnectUserInfoURL))
		req.OIDCConnectJWKSURL = strings.TrimSpace(firstNonEmpty(req.OIDCConnectJWKSURL, previousSettings.OIDCConnectJWKSURL))
		req.OIDCConnectScopes = strings.TrimSpace(firstNonEmpty(req.OIDCConnectScopes, previousSettings.OIDCConnectScopes, "openid email profile"))
		req.OIDCConnectRedirectURL = strings.TrimSpace(firstNonEmpty(req.OIDCConnectRedirectURL, previousSettings.OIDCConnectRedirectURL))
		req.OIDCConnectFrontendRedirectURL = strings.TrimSpace(firstNonEmpty(req.OIDCConnectFrontendRedirectURL, previousSettings.OIDCConnectFrontendRedirectURL, "/auth/oidc/callback"))
		req.OIDCConnectTokenAuthMethod = strings.ToLower(strings.TrimSpace(firstNonEmpty(req.OIDCConnectTokenAuthMethod, previousSettings.OIDCConnectTokenAuthMethod, "client_secret_post")))
		req.OIDCConnectAllowedSigningAlgs = strings.TrimSpace(firstNonEmpty(req.OIDCConnectAllowedSigningAlgs, previousSettings.OIDCConnectAllowedSigningAlgs, "RS256,ES256,PS256"))
		req.OIDCConnectUserInfoEmailPath = strings.TrimSpace(firstNonEmpty(req.OIDCConnectUserInfoEmailPath, previousSettings.OIDCConnectUserInfoEmailPath))
		req.OIDCConnectUserInfoIDPath = strings.TrimSpace(firstNonEmpty(req.OIDCConnectUserInfoIDPath, previousSettings.OIDCConnectUserInfoIDPath))
		req.OIDCConnectUserInfoUsernamePath = strings.TrimSpace(firstNonEmpty(req.OIDCConnectUserInfoUsernamePath, previousSettings.OIDCConnectUserInfoUsernamePath))
		if req.OIDCConnectUsePKCE != nil {
			oidcUsePKCE = *req.OIDCConnectUsePKCE
		}
		if req.OIDCConnectValidateIDToken != nil {
			oidcValidateIDToken = *req.OIDCConnectValidateIDToken
		}
		// This enabled update branch treats zero as a request for the saved skew,
		// then 120 when both values are zero; the standalone runtime reader separately permits zero.
		if req.OIDCConnectClockSkewSeconds == 0 {
			req.OIDCConnectClockSkewSeconds = previousSettings.OIDCConnectClockSkewSeconds
			if req.OIDCConnectClockSkewSeconds == 0 {
				req.OIDCConnectClockSkewSeconds = 120
			}
		}

		if req.OIDCConnectClientID == "" {
			httpx.BadRequest(c, "OIDC Client ID is required when enabled")
			return
		}
		if req.OIDCConnectIssuerURL == "" {
			httpx.BadRequest(c, "OIDC Issuer URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectIssuerURL); err != nil {
			httpx.BadRequest(c, "OIDC Issuer URL must be an absolute http(s) URL")
			return
		}
		if req.OIDCConnectDiscoveryURL != "" {
			if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectDiscoveryURL); err != nil {
				httpx.BadRequest(c, "OIDC Discovery URL must be an absolute http(s) URL")
				return
			}
		}
		if req.OIDCConnectAuthorizeURL != "" {
			if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectAuthorizeURL); err != nil {
				httpx.BadRequest(c, "OIDC Authorize URL must be an absolute http(s) URL")
				return
			}
		}
		if req.OIDCConnectTokenURL != "" {
			if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectTokenURL); err != nil {
				httpx.BadRequest(c, "OIDC Token URL must be an absolute http(s) URL")
				return
			}
		}
		if req.OIDCConnectUserInfoURL != "" {
			if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectUserInfoURL); err != nil {
				httpx.BadRequest(c, "OIDC UserInfo URL must be an absolute http(s) URL")
				return
			}
		}
		if req.OIDCConnectRedirectURL == "" {
			httpx.BadRequest(c, "OIDC Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectRedirectURL); err != nil {
			httpx.BadRequest(c, "OIDC Redirect URL must be an absolute http(s) URL")
			return
		}
		if req.OIDCConnectFrontendRedirectURL == "" {
			httpx.BadRequest(c, "OIDC Frontend Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateFrontendRedirectURL(req.OIDCConnectFrontendRedirectURL); err != nil {
			httpx.BadRequest(c, "OIDC Frontend Redirect URL is invalid")
			return
		}
		if !scopesContainOpenID(req.OIDCConnectScopes) {
			httpx.BadRequest(c, "OIDC scopes must contain openid")
			return
		}
		switch req.OIDCConnectTokenAuthMethod {
		case "", "client_secret_post", "client_secret_basic", "none":
		default:
			httpx.BadRequest(c, "OIDC Token Auth Method must be one of client_secret_post/client_secret_basic/none")
			return
		}
		// Bound enabled-provider token timestamp tolerance before preparing
		// saved OIDC settings for the identity participant.
		if req.OIDCConnectClockSkewSeconds < 0 || req.OIDCConnectClockSkewSeconds > 600 {
			httpx.BadRequest(c, "OIDC clock skew seconds must be between 0 and 600")
			return
		}
		if oidcValidateIDToken && req.OIDCConnectAllowedSigningAlgs == "" {
			httpx.BadRequest(c, "OIDC Allowed Signing Algs is required when validate_id_token=true")
			return
		}
		if req.OIDCConnectJWKSURL != "" {
			if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectJWKSURL); err != nil {
				httpx.BadRequest(c, "OIDC JWKS URL must be an absolute http(s) URL")
				return
			}
		}
		// Secret-bearing authentication methods keep the saved secret when input is
		// blank; none skips this requirement before the normalized request is prepared by identity.
		if req.OIDCConnectTokenAuthMethod == "" || req.OIDCConnectTokenAuthMethod == "client_secret_post" || req.OIDCConnectTokenAuthMethod == "client_secret_basic" {
			if req.OIDCConnectClientSecret == "" {
				if previousSettings.OIDCConnectClientSecret == "" {
					httpx.BadRequest(c, "OIDC Client Secret is required when enabled")
					return
				}
				req.OIDCConnectClientSecret = previousSettings.OIDCConnectClientSecret
			}
		}
	}

	req.GitHubOAuthClientID = strings.TrimSpace(req.GitHubOAuthClientID)
	req.GitHubOAuthClientSecret = strings.TrimSpace(req.GitHubOAuthClientSecret)
	req.GitHubOAuthRedirectURL = strings.TrimSpace(req.GitHubOAuthRedirectURL)
	req.GitHubOAuthFrontendRedirectURL = strings.TrimSpace(req.GitHubOAuthFrontendRedirectURL)
	if req.GitHubOAuthEnabled {
		// Reuse saved client/callback values for blank enabled GitHub fields, then
		// validate selected URLs and require a current or retained exchange secret before preparation.
		req.GitHubOAuthClientID = strings.TrimSpace(firstNonEmpty(req.GitHubOAuthClientID, previousSettings.GitHubOAuthClientID))
		req.GitHubOAuthRedirectURL = strings.TrimSpace(firstNonEmpty(req.GitHubOAuthRedirectURL, previousSettings.GitHubOAuthRedirectURL))
		req.GitHubOAuthFrontendRedirectURL = strings.TrimSpace(firstNonEmpty(req.GitHubOAuthFrontendRedirectURL, previousSettings.GitHubOAuthFrontendRedirectURL, "/auth/oauth/callback"))
		if req.GitHubOAuthClientID == "" {
			httpx.BadRequest(c, "GitHub OAuth Client ID is required when enabled")
			return
		}
		if req.GitHubOAuthClientSecret == "" {
			if previousSettings.GitHubOAuthClientSecret == "" {
				httpx.BadRequest(c, "GitHub OAuth Client Secret is required when enabled")
				return
			}
			req.GitHubOAuthClientSecret = previousSettings.GitHubOAuthClientSecret
		}
		if req.GitHubOAuthRedirectURL == "" {
			httpx.BadRequest(c, "GitHub OAuth Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(req.GitHubOAuthRedirectURL); err != nil {
			httpx.BadRequest(c, "GitHub OAuth Redirect URL must be an absolute http(s) URL")
			return
		}
		if req.GitHubOAuthFrontendRedirectURL == "" {
			httpx.BadRequest(c, "GitHub OAuth Frontend Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateFrontendRedirectURL(req.GitHubOAuthFrontendRedirectURL); err != nil {
			httpx.BadRequest(c, "GitHub OAuth Frontend Redirect URL is invalid")
			return
		}
	}

	req.GoogleOAuthClientID = strings.TrimSpace(req.GoogleOAuthClientID)
	req.GoogleOAuthClientSecret = strings.TrimSpace(req.GoogleOAuthClientSecret)
	req.GoogleOAuthRedirectURL = strings.TrimSpace(req.GoogleOAuthRedirectURL)
	req.GoogleOAuthFrontendRedirectURL = strings.TrimSpace(req.GoogleOAuthFrontendRedirectURL)
	if req.GoogleOAuthEnabled {
		// Reuse saved client/callback values for blank enabled Google fields, then
		// validate selected URLs and require a current or retained exchange secret before preparation.
		req.GoogleOAuthClientID = strings.TrimSpace(firstNonEmpty(req.GoogleOAuthClientID, previousSettings.GoogleOAuthClientID))
		req.GoogleOAuthRedirectURL = strings.TrimSpace(firstNonEmpty(req.GoogleOAuthRedirectURL, previousSettings.GoogleOAuthRedirectURL))
		req.GoogleOAuthFrontendRedirectURL = strings.TrimSpace(firstNonEmpty(req.GoogleOAuthFrontendRedirectURL, previousSettings.GoogleOAuthFrontendRedirectURL, "/auth/oauth/callback"))
		if req.GoogleOAuthClientID == "" {
			httpx.BadRequest(c, "Google OAuth Client ID is required when enabled")
			return
		}
		if req.GoogleOAuthClientSecret == "" {
			if previousSettings.GoogleOAuthClientSecret == "" {
				httpx.BadRequest(c, "Google OAuth Client Secret is required when enabled")
				return
			}
			req.GoogleOAuthClientSecret = previousSettings.GoogleOAuthClientSecret
		}
		if req.GoogleOAuthRedirectURL == "" {
			httpx.BadRequest(c, "Google OAuth Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(req.GoogleOAuthRedirectURL); err != nil {
			httpx.BadRequest(c, "Google OAuth Redirect URL must be an absolute http(s) URL")
			return
		}
		if req.GoogleOAuthFrontendRedirectURL == "" {
			httpx.BadRequest(c, "Google OAuth Frontend Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateFrontendRedirectURL(req.GoogleOAuthFrontendRedirectURL); err != nil {
			httpx.BadRequest(c, "Google OAuth Frontend Redirect URL is invalid")
			return
		}
	}

	// Merge purchase entry and destination before validating them together so
	// disabling the entry can coexist with an empty URL while supplied destinations remain checked.
	purchaseEnabled := previousSettings.PurchaseSubscriptionEnabled
	if req.PurchaseSubscriptionEnabled != nil {
		purchaseEnabled = *req.PurchaseSubscriptionEnabled
	}
	purchaseURL := previousSettings.PurchaseSubscriptionURL
	if req.PurchaseSubscriptionURL != nil {
		purchaseURL = strings.TrimSpace(*req.PurchaseSubscriptionURL)
	}

	// An enabled purchase page requires a nonempty valid URL.
	// When disabled, an empty URL is allowed; a supplied URL still needs validation.
	if purchaseEnabled {
		if purchaseURL == "" {
			httpx.BadRequest(c, "Purchase Subscription URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(purchaseURL); err != nil {
			httpx.BadRequest(c, "Purchase Subscription URL must be an absolute http(s) URL")
			return
		}
	} else if purchaseURL != "" {
		if err := authconfig.ValidateAbsoluteHTTPURL(purchaseURL); err != nil {
			httpx.BadRequest(c, "Purchase Subscription URL must be an absolute http(s) URL")
			return
		}
	}

	// Validate the frontend URL when supplied.
	req.FrontendURL = strings.TrimSpace(req.FrontendURL)
	if req.FrontendURL != "" {
		if err := authconfig.ValidateAbsoluteHTTPURL(req.FrontendURL); err != nil {
			httpx.BadRequest(c, "Frontend URL must be an absolute http(s) URL")
			return
		}
	}

	customMenuJSON, ok := prepareCustomMenuItems(c, req.CustomMenuItems, previousSettings.CustomMenuItems)
	if !ok {
		return
	}

	customEndpointsJSON, ok := prepareCustomEndpoints(c, req.CustomEndpoints, previousSettings.CustomEndpoints)
	if !ok {
		return
	}

	footerLinksJSON, ok := prepareFooterLinks(c, req.FooterLinks, previousSettings.FooterLinks)
	if !ok {
		return
	}

	// Keep the first occurrence of each trimmed model ID so the stored featured
	// list retains administrator order while duplicate selections do not consume display entries.
	// Bound incoming featured selections before duplicate filtering and serialization.
	const maxHomeFeaturedModels = 12
	// Bound each trimmed identifier by UTF-8 byte length.
	const maxHomeFeaturedModelIDLen = 200

	homeFeaturedModelsJSON := previousSettings.HomeFeaturedModels
	if req.HomeFeaturedModels != nil {
		models := *req.HomeFeaturedModels
		if len(models) > maxHomeFeaturedModels {
			httpx.BadRequest(c, "Too many home featured models (max 12)")
			return
		}
		seen := make(map[string]struct{}, len(models))
		normalized := make([]string, 0, len(models))
		for _, model := range models {
			trimmed := strings.TrimSpace(model)
			if trimmed == "" {
				httpx.BadRequest(c, "Home featured model ID is required")
				return
			}
			if len(trimmed) > maxHomeFeaturedModelIDLen {
				httpx.BadRequest(c, "Home featured model ID is too long (max 200 characters)")
				return
			}
			if _, duplicate := seen[trimmed]; duplicate {
				continue
			}
			seen[trimmed] = struct{}{}
			normalized = append(normalized, trimmed)
		}
		modelBytes, err := json.Marshal(normalized)
		if err != nil {
			httpx.BadRequest(c, "Failed to serialize home featured models")
			return
		}
		homeFeaturedModelsJSON = string(modelBytes)
	}

	// Bound the trimmed footer copy by its UTF-8 byte length.
	const maxFooterTextLen = 500
	footerText := previousSettings.FooterText
	if req.FooterText != nil {
		trimmed := strings.TrimSpace(*req.FooterText)
		if len(trimmed) > maxFooterTextLen {
			httpx.BadRequest(c, "Footer text is too long (max 500 characters)")
			return
		}
		footerText = trimmed
	}

	// Bound the requested Ops sampling cadence to sixty through 3,600 seconds
	// before constructing the runtime Snapshot and its participant JSON fields.
	if req.OpsMetricsIntervalSeconds != nil {
		v := min(max(*req.OpsMetricsIntervalSeconds, 60), 3600)
		req.OpsMetricsIntervalSeconds = &v
	}
	defaultSubscriptions := make([]billing.DefaultSubscriptionSetting, 0, len(req.DefaultSubscriptions))
	for _, sub := range req.DefaultSubscriptions {
		defaultSubscriptions = append(defaultSubscriptions, billing.DefaultSubscriptionSetting{
			PlanID: sub.PlanID,
		})
	}

	// An empty minimum version disables the lower bound; otherwise require three numeric components.
	if req.MinClaudeCodeVersion != "" {
		if !semverPattern.MatchString(req.MinClaudeCodeVersion) {
			httpx.Error(c, http.StatusBadRequest, "min_claude_code_version must be empty or a valid semver (e.g. 2.1.63)")
			return
		}
	}

	// An empty maximum version disables the upper bound; otherwise require three numeric components.
	if req.MaxClaudeCodeVersion != "" {
		if !semverPattern.MatchString(req.MaxClaudeCodeVersion) {
			httpx.Error(c, http.StatusBadRequest, "max_claude_code_version must be empty or a valid semver (e.g. 3.0.0)")
			return
		}
	}
	if req.AntigravityUserAgentVersion != nil {
		normalized := strings.TrimSpace(*req.AntigravityUserAgentVersion)
		req.AntigravityUserAgentVersion = &normalized
		if normalized != "" && !semverPattern.MatchString(normalized) {
			httpx.Error(c, http.StatusBadRequest, "antigravity_user_agent_version must be empty or a valid semver (e.g. 1.23.2)")
			return
		}
	}
	if req.OpenAICodexUserAgent != nil {
		normalized := strings.TrimSpace(*req.OpenAICodexUserAgent)
		req.OpenAICodexUserAgent = &normalized
		// Bound the full upstream header by UTF-8 bytes while leaving its version
		// spelling to operators; unlike the Antigravity override, this value is not parsed as semver.
		if len(normalized) > 512 {
			httpx.Error(c, http.StatusBadRequest, "openai_codex_user_agent must be at most 512 characters")
			return
		}
	}

	// When both version bounds are supplied, reject an inverted range before
	// the gateway uses the prepared values to admit later client requests.
	if req.MinClaudeCodeVersion != "" && req.MaxClaudeCodeVersion != "" {
		if clientmeta.CompareVersions(req.MaxClaudeCodeVersion, req.MinClaudeCodeVersion) < 0 {
			httpx.Error(c, http.StatusBadRequest, "max_claude_code_version must be greater than or equal to min_claude_code_version")
			return
		}
	}
	// Reject nonpositive session lifetimes and worker counts before moderation
	// or Creative receives these submitted values for persistent settings preparation.
	if req.CyberSessionBlockTTLSeconds != nil && *req.CyberSessionBlockTTLSeconds <= 0 {
		httpx.BadRequest(c, "cyber_session_block_ttl_seconds must be > 0")
		return
	}
	if req.CreativeWorkerCount != nil && *req.CreativeWorkerCount <= 0 {
		httpx.BadRequest(c, "creative_worker_count must be > 0")
		return
	}

	// Materialize merged scalar, collection, and pointer fields before broad
	// preparation. Nil pointer closures retain prior values; raw-presence filtering
	// later prevents omitted non-pointer zero values from becoming registered writes.
	settings := &composite.Snapshot{
		ProviderSchedulingThresholds: req.ProviderSchedulingThresholds,

		RegistrationEnabled:                 req.RegistrationEnabled,
		EmailVerifyEnabled:                  req.EmailVerifyEnabled,
		RegistrationEmailSuffixWhitelist:    req.RegistrationEmailSuffixWhitelist,
		RegistrationEmailNormalization:      req.RegistrationEmailNormalization,
		RegistrationEmailDomainQuotaEnabled: registrationEmailDomainQuotaEnabled,
		UserEmailChangeEnabled:              userEmailChangeEnabled,
		PromoCodeEnabled:                    req.PromoCodeEnabled,
		PasswordResetEnabled:                req.PasswordResetEnabled,
		FrontendURL:                         req.FrontendURL,
		InvitationCodeEnabled:               req.InvitationCodeEnabled,
		TotpEnabled:                         req.TotpEnabled,
		SessionBindingEnabled:               sessionBindingEnabled,
		StepUpEnabled:                       stepUpEnabled,
		AuditLogRetentionDays:               req.AuditLogRetentionDays,
		LoginAgreementEnabled:               req.LoginAgreementEnabled,
		LoginAgreementMode:                  loginAgreementMode,
		LoginAgreementUpdatedAt:             loginAgreementUpdatedAt,
		LoginAgreementDocuments:             loginAgreementDocuments,
		SMTPHost:                            req.SMTPHost,
		SMTPPort:                            req.SMTPPort,
		SMTPUsername:                        req.SMTPUsername,
		SMTPPassword:                        req.SMTPPassword,
		SMTPFrom:                            req.SMTPFrom,
		SMTPFromName:                        req.SMTPFromName,
		SMTPUseTLS:                          req.SMTPUseTLS,
		TurnstileEnabled:                    req.TurnstileEnabled,
		TurnstileSiteKey:                    req.TurnstileSiteKey,
		TurnstileSecretKey:                  req.TurnstileSecretKey,
		TencentCaptchaEnabled:               req.TencentCaptchaEnabled,
		TencentCaptchaAppID:                 req.TencentCaptchaAppID,
		TencentCaptchaAppSecretKey:          req.TencentCaptchaAppSecretKey,
		TencentCaptchaCloudSecretID:         req.TencentCaptchaCloudSecretID,
		TencentCaptchaCloudSecretKey:        req.TencentCaptchaCloudSecretKey,
		TencentCaptchaRegion:                req.TencentCaptchaRegion,
		AliyunCaptchaEnabled:                req.AliyunCaptchaEnabled,
		AliyunCaptchaAccessKeyID:            req.AliyunCaptchaAccessKeyID,
		AliyunCaptchaAccessKeySecret:        req.AliyunCaptchaAccessKeySecret,
		AliyunCaptchaSceneID:                req.AliyunCaptchaSceneID,
		AliyunCaptchaPrefix:                 req.AliyunCaptchaPrefix,
		AliyunCaptchaRegion:                 req.AliyunCaptchaRegion,
		APIKeyACLTrustForwardedIP: func() bool {
			if req.APIKeyACLTrustForwardedIP != nil {
				return *req.APIKeyACLTrustForwardedIP
			}
			return previousSettings.APIKeyACLTrustForwardedIP
		}(),
		ForwardedClientIPHeaders:               forwardedClientIPHeaders,
		LinuxDoConnectEnabled:                  req.LinuxDoConnectEnabled,
		LinuxDoConnectClientID:                 req.LinuxDoConnectClientID,
		LinuxDoConnectClientSecret:             req.LinuxDoConnectClientSecret,
		LinuxDoConnectRedirectURL:              req.LinuxDoConnectRedirectURL,
		DingTalkConnectEnabled:                 req.DingTalkConnectEnabled,
		DingTalkConnectClientID:                req.DingTalkConnectClientID,
		DingTalkConnectClientSecret:            req.DingTalkConnectClientSecret,
		DingTalkConnectRedirectURL:             req.DingTalkConnectRedirectURL,
		DingTalkConnectCorpRestrictionPolicy:   req.DingTalkConnectCorpRestrictionPolicy,
		DingTalkConnectInternalCorpID:          req.DingTalkConnectInternalCorpID,
		DingTalkConnectBypassRegistration:      req.DingTalkConnectBypassRegistration,
		DingTalkConnectSyncCorpEmail:           req.DingTalkConnectSyncCorpEmail,
		DingTalkConnectSyncDisplayName:         req.DingTalkConnectSyncDisplayName,
		DingTalkConnectSyncDept:                req.DingTalkConnectSyncDept,
		DingTalkConnectSyncCorpEmailAttrKey:    req.DingTalkConnectSyncCorpEmailAttrKey,
		DingTalkConnectSyncDisplayNameAttrKey:  req.DingTalkConnectSyncDisplayNameAttrKey,
		DingTalkConnectSyncDeptAttrKey:         req.DingTalkConnectSyncDeptAttrKey,
		DingTalkConnectSyncCorpEmailAttrName:   req.DingTalkConnectSyncCorpEmailAttrName,
		DingTalkConnectSyncDisplayNameAttrName: req.DingTalkConnectSyncDisplayNameAttrName,
		DingTalkConnectSyncDeptAttrName:        req.DingTalkConnectSyncDeptAttrName,
		WeChatConnectEnabled:                   req.WeChatConnectEnabled,
		WeChatConnectAppID:                     req.WeChatConnectAppID,
		WeChatConnectAppSecret:                 req.WeChatConnectAppSecret,
		WeChatConnectOpenAppID:                 req.WeChatConnectOpenAppID,
		WeChatConnectOpenAppSecret:             req.WeChatConnectOpenAppSecret,
		WeChatConnectMPAppID:                   req.WeChatConnectMPAppID,
		WeChatConnectMPAppSecret:               req.WeChatConnectMPAppSecret,
		WeChatConnectMobileAppID:               req.WeChatConnectMobileAppID,
		WeChatConnectMobileAppSecret:           req.WeChatConnectMobileAppSecret,
		WeChatConnectOpenEnabled:               req.WeChatConnectOpenEnabled,
		WeChatConnectMPEnabled:                 req.WeChatConnectMPEnabled,
		WeChatConnectMobileEnabled:             req.WeChatConnectMobileEnabled,
		WeChatConnectMode:                      req.WeChatConnectMode,
		WeChatConnectScopes:                    req.WeChatConnectScopes,
		WeChatConnectRedirectURL:               req.WeChatConnectRedirectURL,
		WeChatConnectFrontendRedirectURL:       req.WeChatConnectFrontendRedirectURL,
		OIDCConnectEnabled:                     req.OIDCConnectEnabled,
		OIDCConnectProviderName:                req.OIDCConnectProviderName,
		OIDCConnectClientID:                    req.OIDCConnectClientID,
		OIDCConnectClientSecret:                req.OIDCConnectClientSecret,
		OIDCConnectIssuerURL:                   req.OIDCConnectIssuerURL,
		OIDCConnectDiscoveryURL:                req.OIDCConnectDiscoveryURL,
		OIDCConnectAuthorizeURL:                req.OIDCConnectAuthorizeURL,
		OIDCConnectTokenURL:                    req.OIDCConnectTokenURL,
		OIDCConnectUserInfoURL:                 req.OIDCConnectUserInfoURL,
		OIDCConnectJWKSURL:                     req.OIDCConnectJWKSURL,
		OIDCConnectScopes:                      req.OIDCConnectScopes,
		OIDCConnectRedirectURL:                 req.OIDCConnectRedirectURL,
		OIDCConnectFrontendRedirectURL:         req.OIDCConnectFrontendRedirectURL,
		OIDCConnectTokenAuthMethod:             req.OIDCConnectTokenAuthMethod,
		OIDCConnectUsePKCE:                     oidcUsePKCE,
		OIDCConnectValidateIDToken:             oidcValidateIDToken,
		OIDCConnectAllowedSigningAlgs:          req.OIDCConnectAllowedSigningAlgs,
		OIDCConnectClockSkewSeconds:            req.OIDCConnectClockSkewSeconds,
		OIDCConnectRequireEmailVerified:        req.OIDCConnectRequireEmailVerified,
		OIDCConnectUserInfoEmailPath:           req.OIDCConnectUserInfoEmailPath,
		OIDCConnectUserInfoIDPath:              req.OIDCConnectUserInfoIDPath,
		OIDCConnectUserInfoUsernamePath:        req.OIDCConnectUserInfoUsernamePath,
		GitHubOAuthEnabled:                     req.GitHubOAuthEnabled,
		GitHubOAuthClientID:                    req.GitHubOAuthClientID,
		GitHubOAuthClientSecret:                req.GitHubOAuthClientSecret,
		GitHubOAuthRedirectURL:                 req.GitHubOAuthRedirectURL,
		GitHubOAuthFrontendRedirectURL:         req.GitHubOAuthFrontendRedirectURL,
		GoogleOAuthEnabled:                     req.GoogleOAuthEnabled,
		GoogleOneTapEnabled:                    req.GoogleOneTapEnabled,
		GoogleOAuthClientID:                    req.GoogleOAuthClientID,
		GoogleOAuthClientSecret:                req.GoogleOAuthClientSecret,
		GoogleOAuthRedirectURL:                 req.GoogleOAuthRedirectURL,
		GoogleOAuthFrontendRedirectURL:         req.GoogleOAuthFrontendRedirectURL,
		LocalizedSettings:                      req.LocalizedSettings,
		SiteTexts:                              req.SiteTexts,
		DefaultLocale:                          req.DefaultLocale,
		SiteName:                               req.SiteName,
		SiteLogo:                               req.SiteLogo,
		SiteShareImage:                         req.SiteShareImage,
		SiteSubtitle:                           req.SiteSubtitle,
		APIBaseURL:                             req.APIBaseURL,
		ContactInfo:                            req.ContactInfo,
		DocURL:                                 req.DocURL,
		HomeContent:                            req.HomeContent,
		HideCcsImportButton:                    req.HideCcsImportButton,
		PurchaseSubscriptionEnabled:            purchaseEnabled,
		PurchaseSubscriptionURL:                purchaseURL,
		TableDefaultPageSize:                   req.TableDefaultPageSize,
		TablePageSizeOptions:                   req.TablePageSizeOptions,
		UsageRankingLimit:                      usageRanking.Limit,
		UsageRankingEnabled:                    usageRanking.Enabled,
		UsageRankingSortBy:                     string(usageRanking.SortBy),
		UsageRankingShowTotalTokens:            usageRanking.ShowTotalTokens,
		UsageRankingShowRequests:               usageRanking.ShowRequests,
		UsageRankingShowActualCost:             usageRanking.ShowActualCost,
		CustomMenuItems:                        customMenuJSON,
		CustomEndpoints:                        customEndpointsJSON,
		FooterLinks:                            footerLinksJSON,
		FooterText:                             footerText,
		HomeFeaturedModels:                     homeFeaturedModelsJSON,
		DefaultConcurrency:                     req.DefaultConcurrency,
		DefaultBalance:                         req.DefaultBalance,
		AffiliateEnabled:                       req.AffiliateEnabled,
		AffiliateRebateRate:                    req.AffiliateRebateRate,
		AffiliateRebateFreezeHours:             req.AffiliateRebateFreezeHours,
		AffiliateRebateDurationDays:            req.AffiliateRebateDurationDays,
		AffiliateRebatePerInviteeCap:           req.AffiliateRebatePerInviteeCap,
		AdminRechargeRebateEnabled:             adminRechargeRebateEnabled,
		BStatsEnabled: func() bool {
			if req.BStatsEnabled != nil {
				return *req.BStatsEnabled
			}
			return previousSettings.BStatsEnabled
		}(),
		BStatsLogErrors: func() bool {
			if req.BStatsLogErrors != nil {
				return *req.BStatsLogErrors
			}
			return previousSettings.BStatsLogErrors
		}(),
		BStatsLogSentData: func() bool {
			if req.BStatsLogSentData != nil {
				return *req.BStatsLogSentData
			}
			return previousSettings.BStatsLogSentData
		}(),
		BStatsLogResponseStatusText: func() bool {
			if req.BStatsLogResponseStatusText != nil {
				return *req.BStatsLogResponseStatusText
			}
			return previousSettings.BStatsLogResponseStatusText
		}(),
		TeamEnabled: func() bool {
			if req.TeamEnabled != nil {
				return *req.TeamEnabled
			}
			return previousSettings.TeamEnabled
		}(),
		CreativeEnabled: func() bool {
			if req.CreativeEnabled != nil {
				return *req.CreativeEnabled
			}
			return previousSettings.CreativeEnabled
		}(),
		CreativeModelSettings: func() []creative.CreativeModelSetting {
			if req.CreativeModelSettings != nil {
				return *req.CreativeModelSettings
			}
			return previousSettings.CreativeModelSettings
		}(),
		CreativeWorkerCount: func() int {
			if req.CreativeWorkerCount != nil {
				return *req.CreativeWorkerCount
			}
			return previousSettings.CreativeWorkerCount
		}(),
		RiskControlEnabled: func() bool {
			if req.RiskControlEnabled != nil {
				return *req.RiskControlEnabled
			}
			return previousSettings.RiskControlEnabled
		}(),
		CyberSessionBlockEnabled: func() bool {
			if req.CyberSessionBlockEnabled != nil {
				return *req.CyberSessionBlockEnabled
			}
			return previousSettings.CyberSessionBlockEnabled
		}(),
		CyberSessionBlockTTLSeconds: func() int {
			if req.CyberSessionBlockTTLSeconds != nil {
				return *req.CyberSessionBlockTTLSeconds
			}
			return previousSettings.CyberSessionBlockTTLSeconds
		}(),
		DefaultUserRPMLimit:                  req.DefaultUserRPMLimit,
		DefaultUserAPIKeyLimit:               intValueOrDefault(req.DefaultUserAPIKeyLimit, previousSettings.DefaultUserAPIKeyLimit),
		DefaultSubscriptions:                 defaultSubscriptions,
		BalanceUnitName:                      req.BalanceUnitName,
		BalanceUnitSymbol:                    req.BalanceUnitSymbol,
		BalanceIconSVG:                       req.BalanceIconSVG,
		ReasoningPointRMBUnitPrice:           float64ValueOrDefault(req.ReasoningPointRMBUnitPrice, previousSettings.ReasoningPointRMBUnitPrice),
		USDExchangeRate:                      float64ValueOrDefault(req.USDExchangeRate, previousSettings.USDExchangeRate),
		MarketplaceAvailabilityWindowDays:    intValueOrDefault(req.MarketplaceAvailabilityWindowDays, previousSettings.MarketplaceAvailabilityWindowDays),
		MarketplaceAvailabilityBucketMinutes: intValueOrDefault(req.MarketplaceAvailabilityBucketMinutes, previousSettings.MarketplaceAvailabilityBucketMinutes),
		EnableModelFallback:                  req.EnableModelFallback,
		FallbackModelAnthropic:               req.FallbackModelAnthropic,
		FallbackModelOpenAI:                  req.FallbackModelOpenAI,
		FallbackModelGemini:                  req.FallbackModelGemini,
		FallbackModelAntigravity:             req.FallbackModelAntigravity,
		GrokDefaultTextModel: func() string {
			if req.GrokDefaultTextModel != nil {
				return strings.TrimSpace(*req.GrokDefaultTextModel)
			}
			return previousSettings.GrokDefaultTextModel
		}(),
		GrokDefaultBaseURLMode: func() string {
			if req.GrokDefaultBaseURLMode != nil {
				return strings.TrimSpace(*req.GrokDefaultBaseURLMode)
			}
			return previousSettings.GrokDefaultBaseURLMode
		}(),
		EnableIdentityPatch:  req.EnableIdentityPatch,
		IdentityPatchPrompt:  req.IdentityPatchPrompt,
		MinClaudeCodeVersion: req.MinClaudeCodeVersion,
		MaxClaudeCodeVersion: req.MaxClaudeCodeVersion,
		BackendModeEnabled:   req.BackendModeEnabled,
		TTFTMode: func() string {
			if req.TTFTMode != nil {
				return *req.TTFTMode
			}
			return previousSettings.TTFTMode
		}(),
		AllowUserViewErrorRequests: func() bool {
			if req.AllowUserViewErrorRequests != nil {
				return *req.AllowUserViewErrorRequests
			}
			return previousSettings.AllowUserViewErrorRequests
		}(),
		OpsMonitoringEnabled: func() bool {
			if req.OpsMonitoringEnabled != nil {
				return *req.OpsMonitoringEnabled
			}
			return previousSettings.OpsMonitoringEnabled
		}(),
		OpsRealtimeMonitoringEnabled: func() bool {
			if req.OpsRealtimeMonitoringEnabled != nil {
				return *req.OpsRealtimeMonitoringEnabled
			}
			return previousSettings.OpsRealtimeMonitoringEnabled
		}(),
		OpsMetricsIntervalSeconds: func() int {
			if req.OpsMetricsIntervalSeconds != nil {
				return *req.OpsMetricsIntervalSeconds
			}
			return previousSettings.OpsMetricsIntervalSeconds
		}(),
		EnableFingerprintUnification: func() bool {
			if req.EnableFingerprintUnification != nil {
				return *req.EnableFingerprintUnification
			}
			return previousSettings.EnableFingerprintUnification
		}(),
		EnableMetadataPassthrough: func() bool {
			if req.EnableMetadataPassthrough != nil {
				return *req.EnableMetadataPassthrough
			}
			return previousSettings.EnableMetadataPassthrough
		}(),
		EnableCCHSigning: func() bool {
			if req.EnableCCHSigning != nil {
				return *req.EnableCCHSigning
			}
			return previousSettings.EnableCCHSigning
		}(),
		EnableClaudeOAuthSystemPromptInjection: func() bool {
			if req.EnableClaudeOAuthSystemPromptInjection != nil {
				return *req.EnableClaudeOAuthSystemPromptInjection
			}
			return previousSettings.EnableClaudeOAuthSystemPromptInjection
		}(),
		ClaudeOAuthSystemPrompt: func() string {
			if req.ClaudeOAuthSystemPrompt != nil {
				return *req.ClaudeOAuthSystemPrompt
			}
			return previousSettings.ClaudeOAuthSystemPrompt
		}(),
		ClaudeOAuthSystemPromptBlocks: func() string {
			if req.ClaudeOAuthSystemPromptBlocks != nil {
				return *req.ClaudeOAuthSystemPromptBlocks
			}
			return previousSettings.ClaudeOAuthSystemPromptBlocks
		}(),
		EnableAnthropicCacheTTL1hInjection: func() bool {
			if req.EnableAnthropicCacheTTL1hInjection != nil {
				return *req.EnableAnthropicCacheTTL1hInjection
			}
			return previousSettings.EnableAnthropicCacheTTL1hInjection
		}(),
		RewriteMessageCacheControl: func() bool {
			if req.RewriteMessageCacheControl != nil {
				return *req.RewriteMessageCacheControl
			}
			return previousSettings.RewriteMessageCacheControl
		}(),
		EnableClientDatelineNormalization: func() bool {
			if req.EnableClientDatelineNormalization != nil {
				return *req.EnableClientDatelineNormalization
			}
			return previousSettings.EnableClientDatelineNormalization
		}(),
		AntigravityUserAgentVersion: func() string {
			if req.AntigravityUserAgentVersion != nil {
				return *req.AntigravityUserAgentVersion
			}
			return previousSettings.AntigravityUserAgentVersion
		}(),
		OpenAICodexUserAgent: func() string {
			if req.OpenAICodexUserAgent != nil {
				return *req.OpenAICodexUserAgent
			}
			return previousSettings.OpenAICodexUserAgent
		}(),
		OpenAIAllowClaudeCodeCodexPlugin: func() bool {
			if req.OpenAIAllowClaudeCodeCodexPlugin != nil {
				return *req.OpenAIAllowClaudeCodeCodexPlugin
			}
			return previousSettings.OpenAIAllowClaudeCodeCodexPlugin
		}(),
		UserPromptReplacementConfig: func() *promptpolicy.UserPromptReplacementConfig {
			if req.UserPromptReplacementConfig != nil {
				return req.UserPromptReplacementConfig
			}
			return previousSettings.UserPromptReplacementConfig
		}(),
		PaymentVisibleMethodAlipaySource: func() string {
			if req.PaymentVisibleMethodAlipaySource != nil {
				return strings.TrimSpace(*req.PaymentVisibleMethodAlipaySource)
			}
			return previousSettings.PaymentVisibleMethodAlipaySource
		}(),
		PaymentVisibleMethodWxpaySource: func() string {
			if req.PaymentVisibleMethodWxpaySource != nil {
				return strings.TrimSpace(*req.PaymentVisibleMethodWxpaySource)
			}
			return previousSettings.PaymentVisibleMethodWxpaySource
		}(),
		PaymentVisibleMethodAlipayEnabled: func() bool {
			if req.PaymentVisibleMethodAlipayEnabled != nil {
				return *req.PaymentVisibleMethodAlipayEnabled
			}
			return previousSettings.PaymentVisibleMethodAlipayEnabled
		}(),
		PaymentVisibleMethodWxpayEnabled: func() bool {
			if req.PaymentVisibleMethodWxpayEnabled != nil {
				return *req.PaymentVisibleMethodWxpayEnabled
			}
			return previousSettings.PaymentVisibleMethodWxpayEnabled
		}(),
		AdvancedSchedulerStickyWeightedEnabled: func() bool {
			if req.AdvancedSchedulerStickyWeightedEnabled != nil {
				return *req.AdvancedSchedulerStickyWeightedEnabled
			}
			return previousSettings.AdvancedSchedulerStickyWeightedEnabled
		}(),
		AdvancedSchedulerSubscriptionPriorityEnabled: func() bool {
			if req.AdvancedSchedulerSubscriptionPriorityEnabled != nil {
				return *req.AdvancedSchedulerSubscriptionPriorityEnabled
			}
			return previousSettings.AdvancedSchedulerSubscriptionPriorityEnabled
		}(),
		AdvancedSchedulerEWMAErrorRateAlpha: stringSetting(req.AdvancedSchedulerEWMAErrorRateAlpha, previousSettings.AdvancedSchedulerEWMAErrorRateAlpha),
		AdvancedSchedulerEWMATTFTAlpha:      stringSetting(req.AdvancedSchedulerEWMATTFTAlpha, previousSettings.AdvancedSchedulerEWMATTFTAlpha),
		AdvancedSchedulerStickyEscapeEnabled: func() bool {
			if req.AdvancedSchedulerStickyEscapeEnabled != nil {
				return *req.AdvancedSchedulerStickyEscapeEnabled
			}
			return previousSettings.AdvancedSchedulerStickyEscapeEnabled
		}(),
		AdvancedSchedulerStickyEscapeEnabledSet: func() bool {
			if req.AdvancedSchedulerStickyEscapeEnabled != nil {
				return true
			}
			return previousSettings.AdvancedSchedulerStickyEscapeEnabledSet
		}(),
		AdvancedSchedulerStickyEscapeTTFTMs:     stringSetting(req.AdvancedSchedulerStickyEscapeTTFTMs, previousSettings.AdvancedSchedulerStickyEscapeTTFTMs),
		AdvancedSchedulerStickyEscapeErrorRate:  stringSetting(req.AdvancedSchedulerStickyEscapeErrorRate, previousSettings.AdvancedSchedulerStickyEscapeErrorRate),
		AdvancedSchedulerLBTopK:                 stringSetting(req.AdvancedSchedulerLBTopK, previousSettings.AdvancedSchedulerLBTopK),
		AdvancedSchedulerWeightPriority:         stringSetting(req.AdvancedSchedulerWeightPriority, previousSettings.AdvancedSchedulerWeightPriority),
		AdvancedSchedulerWeightLoad:             stringSetting(req.AdvancedSchedulerWeightLoad, previousSettings.AdvancedSchedulerWeightLoad),
		AdvancedSchedulerWeightQueue:            stringSetting(req.AdvancedSchedulerWeightQueue, previousSettings.AdvancedSchedulerWeightQueue),
		AdvancedSchedulerWeightErrorRate:        stringSetting(req.AdvancedSchedulerWeightErrorRate, previousSettings.AdvancedSchedulerWeightErrorRate),
		AdvancedSchedulerWeightTTFT:             stringSetting(req.AdvancedSchedulerWeightTTFT, previousSettings.AdvancedSchedulerWeightTTFT),
		AdvancedSchedulerWeightReset:            stringSetting(req.AdvancedSchedulerWeightReset, previousSettings.AdvancedSchedulerWeightReset),
		AdvancedSchedulerWeightQuotaHeadroom:    stringSetting(req.AdvancedSchedulerWeightQuotaHeadroom, previousSettings.AdvancedSchedulerWeightQuotaHeadroom),
		AdvancedSchedulerWeightPreviousResponse: stringSetting(req.AdvancedSchedulerWeightPreviousResponse, previousSettings.AdvancedSchedulerWeightPreviousResponse),
		AdvancedSchedulerWeightSessionSticky:    stringSetting(req.AdvancedSchedulerWeightSessionSticky, previousSettings.AdvancedSchedulerWeightSessionSticky),
		OpenAIQuotaAutoPauseSettings: func() provider.QuotaAutoPauseSettings {
			if req.OpenAIQuotaAutoPauseSettings != nil {
				return *req.OpenAIQuotaAutoPauseSettings
			}
			return previousSettings.OpenAIQuotaAutoPauseSettings
		}(),
		OpenAIQuotaAutoPauseSettingsSet: req.OpenAIQuotaAutoPauseSettings != nil,
		BalanceLowNotifyEnabled: func() bool {
			if req.BalanceLowNotifyEnabled != nil {
				return *req.BalanceLowNotifyEnabled
			}
			return previousSettings.BalanceLowNotifyEnabled
		}(),
		BalanceLowNotifyThreshold: func() float64 {
			if req.BalanceLowNotifyThreshold != nil {
				return *req.BalanceLowNotifyThreshold
			}
			return previousSettings.BalanceLowNotifyThreshold
		}(),
		BalanceLowNotifyRechargeURL: func() string {
			if req.BalanceLowNotifyRechargeURL != nil {
				return *req.BalanceLowNotifyRechargeURL
			}
			return previousSettings.BalanceLowNotifyRechargeURL
		}(),
		SubscriptionExpiryNotifyEnabled: func() bool {
			if req.SubscriptionExpiryNotifyEnabled != nil {
				return *req.SubscriptionExpiryNotifyEnabled
			}
			return previousSettings.SubscriptionExpiryNotifyEnabled
		}(),
		ProviderQuotaNotifyEnabled: func() bool {
			if req.ProviderQuotaNotifyEnabled != nil {
				return *req.ProviderQuotaNotifyEnabled
			}
			return previousSettings.ProviderQuotaNotifyEnabled
		}(),
		ProviderQuotaNotifyEmails: func() []contact.Entry {
			if req.ProviderQuotaNotifyEmails != nil {
				return identitydto.NotifyEmailEntriesToIdentity(*req.ProviderQuotaNotifyEmails)
			}
			return previousSettings.ProviderQuotaNotifyEmails
		}(),
	}

	authSourceDefaults := &identity.AuthSourceDefaultSettings{
		Email: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultEmailBalance, previousAuthSourceDefaults.Email.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultEmailConcurrency, previousAuthSourceDefaults.Email.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultEmailSubscriptions, previousAuthSourceDefaults.Email.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultEmailGrantOnSignup, previousAuthSourceDefaults.Email.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultEmailGrantOnFirstBind, previousAuthSourceDefaults.Email.GrantOnFirstBind),
		},
		LinuxDo: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultLinuxDoBalance, previousAuthSourceDefaults.LinuxDo.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultLinuxDoConcurrency, previousAuthSourceDefaults.LinuxDo.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultLinuxDoSubscriptions, previousAuthSourceDefaults.LinuxDo.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultLinuxDoGrantOnSignup, previousAuthSourceDefaults.LinuxDo.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultLinuxDoGrantOnFirstBind, previousAuthSourceDefaults.LinuxDo.GrantOnFirstBind),
		},
		OIDC: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultOIDCBalance, previousAuthSourceDefaults.OIDC.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultOIDCConcurrency, previousAuthSourceDefaults.OIDC.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultOIDCSubscriptions, previousAuthSourceDefaults.OIDC.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultOIDCGrantOnSignup, previousAuthSourceDefaults.OIDC.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultOIDCGrantOnFirstBind, previousAuthSourceDefaults.OIDC.GrantOnFirstBind),
		},
		WeChat: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultWeChatBalance, previousAuthSourceDefaults.WeChat.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultWeChatConcurrency, previousAuthSourceDefaults.WeChat.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultWeChatSubscriptions, previousAuthSourceDefaults.WeChat.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultWeChatGrantOnSignup, previousAuthSourceDefaults.WeChat.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultWeChatGrantOnFirstBind, previousAuthSourceDefaults.WeChat.GrantOnFirstBind),
		},
		GitHub: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultGitHubBalance, previousAuthSourceDefaults.GitHub.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultGitHubConcurrency, previousAuthSourceDefaults.GitHub.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultGitHubSubscriptions, previousAuthSourceDefaults.GitHub.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultGitHubGrantOnSignup, previousAuthSourceDefaults.GitHub.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultGitHubGrantOnFirstBind, previousAuthSourceDefaults.GitHub.GrantOnFirstBind),
		},
		Google: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultGoogleBalance, previousAuthSourceDefaults.Google.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultGoogleConcurrency, previousAuthSourceDefaults.Google.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultGoogleSubscriptions, previousAuthSourceDefaults.Google.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultGoogleGrantOnSignup, previousAuthSourceDefaults.Google.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultGoogleGrantOnFirstBind, previousAuthSourceDefaults.Google.GrantOnFirstBind),
		},
		DingTalk: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultDingTalkBalance, previousAuthSourceDefaults.DingTalk.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultDingTalkConcurrency, previousAuthSourceDefaults.DingTalk.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultDingTalkSubscriptions, previousAuthSourceDefaults.DingTalk.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultDingTalkGrantOnSignup, previousAuthSourceDefaults.DingTalk.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultDingTalkGrantOnFirstBind, previousAuthSourceDefaults.DingTalk.GrantOnFirstBind),
		},
		ForceEmailOnThirdPartySignup: boolValueOrDefault(req.ForceEmailOnThirdPartySignup, previousAuthSourceDefaults.ForceEmailOnThirdPartySignup),
	}
	values, err := h.settingService.PrepareSettingsWithAuthSourceDefaults(c.Request.Context(), settings, nil, omitted)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	changes := []runtimesettings.PreparedChange{{Module: "system-values", Values: values}}
	// Use native readback callbacks when offered; the compatibility callback
	// otherwise stops its own application chain on the first failure before
	// publishing the monitoring switch.
	if native, ok := h.settingService.(interface {
		// ApplicationChanges returns post-commit callbacks with update-local readback
		// state. The handler appends them in order for its session to execute sequentially,
		// allowing application errors to retain persisted=true reporting.
		ApplicationChanges() []runtimesettings.PreparedChange
	}); ok {
		changes = append(changes, native.ApplicationChanges()...)
	} else {
		changes = append(changes, runtimesettings.PreparedChange{Module: "system", Apply: func(ctx context.Context) error {
			if err := h.settingService.ApplyPersistedSettings(ctx); err != nil {
				return err
			}
			if h.opsService != nil {
				h.opsService.SetMonitoringEnabled(settings.OpsMonitoringEnabled)
			}
			return nil
		}})
	}

	// Serialize normalized input for every registered owner before the single
	// Commit so one preparation failure leaves the complete stored settings batch untouched.
	rawInput, err := json.Marshal(req)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	var fields runtimesettings.Fields
	if err = json.Unmarshal(rawInput, &fields); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	// Remove absent non-pointer names before owners prepare their fields. Pointer
	// names still carry request serialization here; the module replacements below
	// supply their merged values and preserve field-specific presence rules.
	for name := range settingKeyByJSONName {
		if _, sent := sentFields[name]; !sent {
			delete(fields, name)
		}
	}
	// Serialize these merged scalar values on every save, even when omitted, so
	// their registered owners receive the resolved prior-or-submitted state. These
	// bool and string values encode directly; the assignments discard marshal errors.
	fields["affiliate_admin_recharge_enabled"], _ = json.Marshal(settings.AdminRechargeRebateEnabled)
	fields["usage_ranking_enabled"], _ = json.Marshal(settings.UsageRankingEnabled)
	fields["usage_ranking_sort_by"], _ = json.Marshal(settings.UsageRankingSortBy)
	fields["usage_ranking_show_total_tokens"], _ = json.Marshal(settings.UsageRankingShowTotalTokens)
	fields["usage_ranking_show_requests"], _ = json.Marshal(settings.UsageRankingShowRequests)
	fields["usage_ranking_show_actual_cost"], _ = json.Marshal(settings.UsageRankingShowActualCost)
	fields["allow_user_view_error_requests"], _ = json.Marshal(settings.AllowUserViewErrorRequests)
	// Replace identity inputs after security checks and source-grant merging so
	// that owner receives normalized policy values; still skip omitted non-pointer identity names.
	identityRaw, err := json.Marshal(settings.IdentityAdminSettings())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	var identityFields runtimesettings.Fields
	if err = json.Unmarshal(identityRaw, &identityFields); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	maps.Copy(identityFields, identity.AuthSourceParticipantFields(authSourceDefaults))
	mergePresentSettingsFields(fields, identityFields, sentFields)
	// Encode merged scalar and collection fields for their registered owners.
	// Current values use JSON-supported types, so encoding errors are discarded
	// here before the owning preparers validate and select their stored representation.
	fields["bstats_enabled"], _ = json.Marshal(settings.BStatsEnabled)
	fields["bstats_log_errors"], _ = json.Marshal(settings.BStatsLogErrors)
	fields["bstats_log_sent_data"], _ = json.Marshal(settings.BStatsLogSentData)
	fields["bstats_log_response_status_text"], _ = json.Marshal(settings.BStatsLogResponseStatusText)
	fields["team_enabled"], _ = json.Marshal(settings.TeamEnabled)
	fields["risk_control_enabled"], _ = json.Marshal(settings.RiskControlEnabled)
	fields["cyber_session_block_enabled"], _ = json.Marshal(settings.CyberSessionBlockEnabled)
	fields["cyber_session_block_ttl_seconds"], _ = json.Marshal(settings.CyberSessionBlockTTLSeconds)
	fields["creative_enabled"], _ = json.Marshal(settings.CreativeEnabled)
	fields["creative_worker_count"], _ = json.Marshal(settings.CreativeWorkerCount)
	fields["creative_model_settings"], _ = json.Marshal(settings.CreativeModelSettings)
	fields["api_key_acl_trust_forwarded_ip"], _ = json.Marshal(settings.APIKeyACLTrustForwardedIP)
	fields["forwarded_client_ip_headers"], _ = json.Marshal(settings.ForwardedClientIPHeaders)
	if err := mergeNormalizedSettingsFields(fields, settings.BillingAdminSettings(), sentFields); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	schedulerFields, err := settings.SchedulerAdminSettings().ParticipantFields()
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	// Remove the initial pointer serialization before scheduler fields retain
	// their normalized presence information, so omission can retain inherited sticky-escape policy.
	delete(fields, "advanced_scheduler_sticky_escape_enabled")
	mergePresentSettingsFields(fields, schedulerFields, sentFields)
	if err := mergeNormalizedSettingsFields(fields, settings.RoutingAdminSettings(), sentFields); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	if err := mergeNormalizedSettingsFields(fields, settings.SiteAdminSettings(), sentFields); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	for name, value := range map[string]any{"provider_quota_notify_enabled": settings.ProviderQuotaNotifyEnabled, "provider_quota_notify_emails": settings.ProviderQuotaNotifyEmails, "provider_scheduling_thresholds": settings.ProviderSchedulingThresholds} {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			httpx.ErrorFrom(c, marshalErr)
			return
		}
		fields[name] = raw
	}
	for name, value := range map[string]any{
		"payment_visible_method_alipay_source": settings.PaymentVisibleMethodAlipaySource, "payment_visible_method_wxpay_source": settings.PaymentVisibleMethodWxpaySource, "payment_visible_method_alipay_enabled": settings.PaymentVisibleMethodAlipayEnabled, "payment_visible_method_wxpay_enabled": settings.PaymentVisibleMethodWxpayEnabled,
		"ops_monitoring_enabled": settings.OpsMonitoringEnabled, "ops_realtime_monitoring_enabled": settings.OpsRealtimeMonitoringEnabled, "ops_metrics_interval_seconds": settings.OpsMetricsIntervalSeconds,
	} {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			httpx.ErrorFrom(c, marshalErr)
			return
		}
		fields[name] = raw
	}
	// Keep the quota object absent until its nonnil request presence flag admits
	// an update, leaving wider saved Ops settings outside an omitted quota change.
	delete(fields, "openai_provider_quota_auto_pause")
	if settings.OpenAIQuotaAutoPauseSettingsSet {
		// Encode the supplied quota object for its participant; a discarded marshal
		// error leaves nil bytes that its JSON decoding rejects before persistence.
		fields["openai_provider_quota_auto_pause"], _ = json.Marshal(settings.OpenAIQuotaAutoPauseSettings)
	}
	if err := mergeNormalizedSettingsFields(fields, settings.GatewayAdminSettings(), sentFields); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	prepared, err := h.preparedParticipants(update.Context(), fields, values, previousSettings.StoredValues)
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	changes = append(changes, prepared...)
	// Commit this complete prepared value batch before its application callbacks
	// and audit event. A rejected write leaves those later update phases uncalled.
	if err := update.Commit(changes...); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	h.auditSettingsUpdate(c, previousSettings, settings, previousAuthSourceDefaults, authSourceDefaults, auditReq)

	// Read back after completed persistence and audit so the response reflects
	// saved configuration; a failure here returns an error after that batch is already stored.
	updatedSettings, err := h.settingService.GetAllSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	h.ensureDingTalkSyncAttributes(c.Request.Context(), updatedSettings)
	updatedAuthSourceDefaults, err := h.settingService.GetAuthSourceDefaultSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}
	updatedDefaultSubscriptions := make([]billinghttp.DefaultSubscriptionSetting, 0, len(updatedSettings.DefaultSubscriptions))
	for _, sub := range updatedSettings.DefaultSubscriptions {
		updatedDefaultSubscriptions = append(updatedDefaultSubscriptions, billinghttp.DefaultSubscriptionSetting{
			PlanID: sub.PlanID,
		})
	}

	// PUT readback propagates a payment-read error after commit, unlike GET
	// recovery; a nil successful result still supplies empty configuration to the response mapper.
	var updatedPaymentCfg *payment.PaymentConfig
	if h.paymentConfigService != nil {
		updatedPaymentCfg, err = h.paymentConfigService.GetPaymentConfig(c.Request.Context())
		if err != nil {
			httpx.ErrorFrom(c, err)
			return
		}
	}
	if updatedPaymentCfg == nil {
		updatedPaymentCfg = &payment.PaymentConfig{}
	}

	payload := buildSystemSettingsResponse(updatedSettings, updatedPaymentCfg, updatedDefaultSubscriptions, h.settingService.IsTotpEncryptionKeyConfigured())
	payload.OpsMonitoringEnabled = updatedSettings.OpsMonitoringEnabled
	// Log optional policy read failures after commit and keep the response's
	// policy field absent; this read cannot undo the already persisted settings batch.
	if fastPolicy, err := h.settingService.GetOpenAIFastPolicySettings(c.Request.Context()); err != nil {
		slog.Error("openai_fast_policy_settings_get_failed", "error", err)
	} else if fastPolicy != nil {
		payload.OpenAIFastPolicySettings = openaiFastPolicySettingsToDTO(fastPolicy)
	}

	httpx.Success(c, systemSettingsResponseData(payload, updatedAuthSourceDefaults))
}

// prepareCustomMenuItems validates menu entries and returns their settings JSON.
// A nil input retains previous JSON, while a supplied slice is serialized even
// when empty. URL normalization and generated IDs modify the caller's slice,
// which also updates the shallow request copy retained for audit processing.
//
// Validation proceeds in entry order before the duplicate-ID pass. Mutations
// completed before a later error remain visible to the caller, including a
// trimmed URL on an entry whose visibility or ID is subsequently rejected.
// False means the function has written the first HTTP error response.
func prepareCustomMenuItems(c *gin.Context, input *[]sitedto.CustomMenuItem, previous string) (string, bool) {
	// Bound menu count, text, icons, and IDs before producing stored navigation JSON.
	const (
		maxCustomMenuItems    = 20
		maxMenuItemLabelLen   = 50
		maxMenuItemURLLen     = 2048
		maxMenuItemIconSVGLen = 10 * 1024 // maximum SVG bytes (ten kibibytes)
		maxMenuItemIDLen      = 32
	)

	customMenuJSON := previous
	if input != nil {
		items := *input
		if len(items) > maxCustomMenuItems {
			httpx.BadRequest(c, "Too many custom menu items (max 20)")
			return "", false
		}
		for i, item := range items {
			if strings.TrimSpace(item.Label) == "" {
				httpx.BadRequest(c, "Custom menu item label is required")
				return "", false
			}
			if len(item.Label) > maxMenuItemLabelLen {
				httpx.BadRequest(c, "Custom menu item label is too long (max 50 characters)")
				return "", false
			}
			urlTrimmed := strings.TrimSpace(item.URL)
			if slug, ok := strings.CutPrefix(urlTrimmed, "md:"); ok {
				// Apply the page reader's bounded ASCII slug rules so the saved md:
				// destination names a lookup key accepted by the protected page route.
				if slug == "" {
					httpx.BadRequest(c, "Custom menu item markdown slug cannot be empty (use md:slug format)")
					return "", false
				}
				if len(slug) > 64 || !markdownMenuSlugPattern.MatchString(slug) {
					httpx.BadRequest(c, "Custom menu item markdown slug contains invalid characters")
					return "", false
				}
			} else {
				if urlTrimmed == "" {
					httpx.BadRequest(c, "Custom menu item URL is required (use md:slug for markdown pages)")
					return "", false
				}
				if len(urlTrimmed) > maxMenuItemURLLen {
					httpx.BadRequest(c, "Custom menu item URL is too long (max 2048 characters)")
					return "", false
				}
				if err := authconfig.ValidateAbsoluteHTTPURL(urlTrimmed); err != nil {
					httpx.BadRequest(c, "Custom menu item URL must be an absolute http(s) URL or md:<slug>")
					return "", false
				}
			}
			// Save the recognized trimmed destination so leading whitespace cannot hide
			// its md: prefix from the later site page and visibility resolution helpers.
			items[i].URL = urlTrimmed
			if item.Visibility != "user" && item.Visibility != "admin" {
				httpx.BadRequest(c, "Custom menu item visibility must be 'user' or 'admin'")
				return "", false
			}
			if len(item.IconSVG) > maxMenuItemIconSVGLen {
				httpx.BadRequest(c, "Custom menu item icon SVG is too large (max 10KB)")
				return "", false
			}
			// Give a missing item a saved stable ID so later localization can match
			// its identity after reordering; the next pass still checks within-list uniqueness.
			if strings.TrimSpace(item.ID) == "" {
				id, err := generateMenuItemID()
				if err != nil {
					httpx.Error(c, http.StatusInternalServerError, "Failed to generate menu item ID")
					return "", false
				}
				items[i].ID = id
			} else if len(item.ID) > maxMenuItemIDLen {
				httpx.BadRequest(c, "Custom menu item ID is too long (max 32 characters)")
				return "", false
			} else if !menuItemIDPattern.MatchString(item.ID) {
				httpx.BadRequest(c, "Custom menu item ID contains invalid characters (only a-z, A-Z, 0-9, - and _ are allowed)")
				return "", false
			}
		}
		// Reject duplicate item IDs after missing IDs are filled so one localized
		// entry cannot be selected for two submitted menu records during later site preparation.
		seen := make(map[string]struct{}, len(items))
		for _, item := range items {
			if _, exists := seen[item.ID]; exists {
				httpx.BadRequest(c, "Duplicate custom menu item ID: "+item.ID)
				return "", false
			}
			seen[item.ID] = struct{}{}
		}
		menuBytes, err := json.Marshal(items)
		if err != nil {
			httpx.BadRequest(c, "Failed to serialize custom menu items")
			return "", false
		}
		customMenuJSON = string(menuBytes)
	}
	return customMenuJSON, true
}

// prepareCustomEndpoints validates endpoint entries and returns their settings
// JSON, retaining previous JSON for nil input. URL checks trim whitespace while
// serialization retains the supplied strings, and false reports an HTTP error
// already written for the first invalid field.
func prepareCustomEndpoints(c *gin.Context, input *[]sitedto.CustomEndpoint, previous string) (string, bool) {
	// Bound endpoint counts and untrimmed text before producing their saved JSON records.
	const (
		maxCustomEndpoints        = 10
		maxEndpointNameLen        = 50
		maxEndpointURLLen         = 2048
		maxEndpointDescriptionLen = 200
	)

	customEndpointsJSON := previous
	if input != nil {
		endpoints := *input
		if len(endpoints) > maxCustomEndpoints {
			httpx.BadRequest(c, "Too many custom endpoints (max 10)")
			return "", false
		}
		for _, ep := range endpoints {
			if strings.TrimSpace(ep.Name) == "" {
				httpx.BadRequest(c, "Custom endpoint name is required")
				return "", false
			}
			if len(ep.Name) > maxEndpointNameLen {
				httpx.BadRequest(c, "Custom endpoint name is too long (max 50 characters)")
				return "", false
			}
			if strings.TrimSpace(ep.Endpoint) == "" {
				httpx.BadRequest(c, "Custom endpoint URL is required")
				return "", false
			}
			if len(ep.Endpoint) > maxEndpointURLLen {
				httpx.BadRequest(c, "Custom endpoint URL is too long (max 2048 characters)")
				return "", false
			}
			if err := authconfig.ValidateAbsoluteHTTPURL(strings.TrimSpace(ep.Endpoint)); err != nil {
				httpx.BadRequest(c, "Custom endpoint URL must be an absolute http(s) URL")
				return "", false
			}
			if len(ep.Description) > maxEndpointDescriptionLen {
				httpx.BadRequest(c, "Custom endpoint description is too long (max 200 characters)")
				return "", false
			}
		}
		endpointBytes, err := json.Marshal(endpoints)
		if err != nil {
			httpx.BadRequest(c, "Failed to serialize custom endpoints")
			return "", false
		}
		customEndpointsJSON = string(endpointBytes)
	}
	return customEndpointsJSON, true
}

// prepareFooterLinks validates footer groups and returns their settings JSON,
// retaining previous JSON for nil input. Slash-prefixed destinations are accepted
// after trimming for validation, while serialization retains supplied whitespace.
// False reports an HTTP error already written for the first invalid field.
func prepareFooterLinks(c *gin.Context, input *[]sitedto.FooterLinkGroup, previous string) (string, bool) {
	// Bound footer columns, nested links, and display text before preparing their stored JSON.
	const (
		maxFooterGroups      = 6
		maxFooterLinksPerGrp = 10
		maxFooterLabelLen    = 50
		maxFooterURLLen      = 2048
	)

	footerLinksJSON := previous
	if input != nil {
		groups := *input
		if len(groups) > maxFooterGroups {
			httpx.BadRequest(c, "Too many footer link groups (max 6)")
			return "", false
		}
		for _, group := range groups {
			if strings.TrimSpace(group.Title) == "" {
				httpx.BadRequest(c, "Footer link group title is required")
				return "", false
			}
			if len(group.Title) > maxFooterLabelLen {
				httpx.BadRequest(c, "Footer link group title is too long (max 50 characters)")
				return "", false
			}
			if len(group.Links) > maxFooterLinksPerGrp {
				httpx.BadRequest(c, "Too many links in footer group (max 10)")
				return "", false
			}
			for _, link := range group.Links {
				if strings.TrimSpace(link.Label) == "" {
					httpx.BadRequest(c, "Footer link label is required")
					return "", false
				}
				if len(link.Label) > maxFooterLabelLen {
					httpx.BadRequest(c, "Footer link label is too long (max 50 characters)")
					return "", false
				}
				trimmedURL := strings.TrimSpace(link.URL)
				if trimmedURL == "" {
					httpx.BadRequest(c, "Footer link URL is required")
					return "", false
				}
				if len(trimmedURL) > maxFooterURLLen {
					httpx.BadRequest(c, "Footer link URL is too long (max 2048 characters)")
					return "", false
				}
				// Permit slash-prefixed footer destinations, including protocol-relative text,
				// while other values must pass the absolute HTTP(S) check before they are serialized.
				if !strings.HasPrefix(trimmedURL, "/") {
					if err := authconfig.ValidateAbsoluteHTTPURL(trimmedURL); err != nil {
						httpx.BadRequest(c, "Footer link URL must be an absolute http(s) URL or a path starting with /")
						return "", false
					}
				}
			}
		}
		groupBytes, err := json.Marshal(groups)
		if err != nil {
			httpx.BadRequest(c, "Failed to serialize footer links")
			return "", false
		}
		footerLinksJSON = string(groupBytes)
	}
	return footerLinksJSON, true
}

// mergeNormalizedSettingsFields serializes a module value into flat request
// field JSON and applies the participant presence rules to the supplied map.
// Encoding or decoding failures return before mutation; successful conversion
// delegates omission handling to mergePresentSettingsFields for the next
// preparation phase.
func mergeNormalizedSettingsFields(fields runtimesettings.Fields, normalized any, sentFields map[string]json.RawMessage) error {
	raw, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	var normalizedFields runtimesettings.Fields
	if err := json.Unmarshal(raw, &normalizedFields); err != nil {
		return err
	}
	mergePresentSettingsFields(fields, normalizedFields, sentFields)
	return nil
}

// mergePresentSettingsFields merges encoded participant values into the pending
// settings update while preserving omitted scalar fields. The original request
// presence controls non-pointer names, so normalization cannot turn an unrelated
// partial update into an overwrite with a default, false, or empty value.
//
// Pointer-backed names already carry their owner's resolved values and are copied
// even when the raw request omits them. Omitted scalar names leave any existing
// destination value untouched. Assigned RawMessage values share their backing
// bytes with normalized; this function performs no encoding, validation, or commit.
func mergePresentSettingsFields(fields, normalized runtimesettings.Fields, sentFields map[string]json.RawMessage) {
	for name, value := range normalized {
		if _, nonPointer := settingKeyByJSONName[name]; nonPointer {
			if _, sent := sentFields[name]; !sent {
				continue
			}
		}
		fields[name] = value
	}
}

// rejectDeprecatedAdvancedSchedulerRequestFields rejects the retired global
// scheduler switch and openai_advanced_scheduler_ names whenever present. It writes
// the bad-request reason with the encountered field and returns true; other shared
// scheduler parameters continue through the handler.
func rejectDeprecatedAdvancedSchedulerRequestFields(c *gin.Context, sentFields map[string]json.RawMessage) bool {
	for field := range sentFields {
		if field == "advanced_scheduler_enabled" || strings.HasPrefix(field, "openai_advanced_scheduler_") {
			httpx.ErrorWithDetails(c, http.StatusBadRequest,
				"Deprecated advanced scheduler setting: "+field+"; select the scheduler type on each group and use advanced_scheduler_* settings",
				"DEPRECATED_ADVANCED_SCHEDULER_SETTING", map[string]string{"field": field})
			return true
		}
	}
	return false
}

// mapDingTalkValidateError selects the two known application-rule reason codes
// through errors.Is. Other validation failures use the corporation-config reason,
// allowing the response to retain the supplied error text alongside a known category.
func mapDingTalkValidateError(err error) string {
	switch {
	case errors.Is(err, authconfig.ErrDingTalkV1AppTypeMismatch):
		return "dingtalk_apptype_mismatch"
	case errors.Is(err, authconfig.ErrDingTalkV4InvalidAppKind):
		return "dingtalk_app_kind_invalid"
	default:
		return "dingtalk_corp_config_invalid"
	}
}

// ensureDingTalkSyncAttributes prepares enabled profile-sync attribute definitions
// after settings readback under internal_only policy. Missing service or Snapshot
// skips work. Lookup failure selects creation; update and create failures are
// logged by each helper, while later enabled attributes can still be attempted.
func (h *Handler) ensureDingTalkSyncAttributes(ctx context.Context, settings *composite.Snapshot) {
	if h.userAttributeService == nil || settings == nil {
		return
	}
	if settings.DingTalkConnectCorpRestrictionPolicy != "internal_only" {
		return
	}
	if settings.DingTalkConnectSyncDisplayName {
		h.ensureUserAttributeDefinition(ctx, settings.DingTalkConnectSyncDisplayNameAttrKey, settings.DingTalkConnectSyncDisplayNameAttrName, "钉钉 internal_only 登录时同步的钉钉姓名", identity.AttributeTypeText)
	}
	if settings.DingTalkConnectSyncCorpEmail {
		h.ensureUserAttributeDefinition(ctx, settings.DingTalkConnectSyncCorpEmailAttrKey, settings.DingTalkConnectSyncCorpEmailAttrName, "钉钉 internal_only 登录时同步的企业邮箱", identity.AttributeTypeEmail)
	}
	if settings.DingTalkConnectSyncDept {
		h.ensureUserAttributeDefinition(ctx, settings.DingTalkConnectSyncDeptAttrKey, settings.DingTalkConnectSyncDeptAttrName, "钉钉 internal_only 登录时同步的完整部门路径（如：公司/研发部）", identity.AttributeTypeText)
	}
}

// ensureUserAttributeDefinition resolves a trimmed nonempty attribute key after
// the settings commit. A found definition can receive a nonempty changed display
// name; its existing type, options, and required flags remain as stored.
//
// An absent definition or lookup error selects creation using the supplied
// name, description, type, and enabled flag. Update and creation failures are
// logged and stop this helper while the saved settings batch remains committed.
// The caller decides eligibility for profile-sync setup before invoking these
// separate attribute writes.
func (h *Handler) ensureUserAttributeDefinition(ctx context.Context, key, name, description string, attrType identity.UserAttributeType) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	// Lookup failure selects creation, and failed updates or creates are logged
	// after the settings batch is committed. The caller can continue setting up
	// the other enabled attributes after this helper returns.
	existing, err := h.userAttributeService.GetDefinitionByKey(ctx, key)
	if err == nil && existing != nil {
		if strings.TrimSpace(name) != "" && existing.Name != name {
			if _, err := h.userAttributeService.UpdateDefinition(ctx, existing.ID, identity.UpdateAttributeDefinitionInput{
				Name: &name,
			}); err != nil {
				slog.Warn("dingtalk: update user attribute definition name failed", "key", key, "err", err.Error())
				return
			}
			slog.Info("dingtalk: updated user attribute definition name", "key", key, "name", name)
		}
		return
	}
	if _, err := h.userAttributeService.CreateDefinition(ctx, identity.CreateAttributeDefinitionInput{
		Key:         key,
		Name:        name,
		Description: description,
		Type:        attrType,
		Enabled:     true,
	}); err != nil {
		slog.Warn("dingtalk: ensure user attribute definition failed", "key", key, "err", err.Error())
		return
	}
	slog.Info("dingtalk: created user attribute definition", "key", key, "name", name, "type", attrType)
}

// rejectRemovedPlatformQuotaFields rejects the default or provider-specific
// platform-quota names when they occur in the raw request. It writes the
// removed-field reason with the encountered name and returns true, including
// for supplied JSON null.
func rejectRemovedPlatformQuotaFields(c *gin.Context, fields map[string]json.RawMessage) bool {
	for name := range fields {
		if name == "default_platform_quotas" || strings.HasPrefix(name, "auth_source_default_") && strings.HasSuffix(name, "_platform_quotas") {
			httpx.ErrorWithDetails(c, http.StatusBadRequest, "User platform quotas have been removed", "REMOVED_SETTING_FIELD", map[string]string{"field": name})
			return true
		}
	}
	return false
}

// rejectRemovedUngroupedKeySchedulingField checks raw presence of the retired
// ungrouped-key switch. Presence writes its bad-request removal response and returns
// true for any value; absence returns false before the handler accesses settings.
func rejectRemovedUngroupedKeySchedulingField(c *gin.Context, fields map[string]json.RawMessage) bool {
	const field = "allow_ungrouped_key_scheduling"
	if _, present := fields[field]; !present {
		return false
	}
	httpx.ErrorWithDetails(c, http.StatusBadRequest, "API keys require an explicit group", "REMOVED_SETTING_FIELD", map[string]string{"field": field})
	return true
}
