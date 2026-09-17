package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/authconfig"
	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"

	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	settingsdto "github.com/TokenFlux/TokenRouter/internal/settings/httpapi/dto"
	sitedto "github.com/TokenFlux/TokenRouter/internal/site/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	runtimesettings "github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// UpdateSettingsRequest 更新设置请求
type UpdateSettingsRequest = settingsdto.UpdateSettingsRequest

// UpdateSettings 更新系统设置
// PUT /api/v1/admin/settings
// ensureActorTotpForStepUp 校验当前操作者具备开启 step-up 门控的条件：
// 必须是真人管理员会话（admin API key 无法完成 TOTP step-up，拒绝）且本人已启用 TOTP。
// 校验失败时写入错误响应并返回 false。
func (h *Handler) ensureActorTotpForStepUp(c *gin.Context) bool {
	if c.GetString("auth_method") == audit.AuditAuthMethodAdminAPIKey {
		response.ErrorWithDetails(c, http.StatusForbidden,
			"Admin API key cannot enable step-up verification; use an admin session with TOTP enabled",
			"STEP_UP_ADMIN_API_KEY_FORBIDDEN", nil)
		return false
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.ErrorWithDetails(c, http.StatusForbidden,
			"Enabling step-up verification requires an authenticated admin session",
			"STEP_UP_ENABLE_REQUIRES_TOTP", nil)
		return false
	}
	if h.userService == nil {
		response.InternalError(c, "Step-up precondition check unavailable")
		return false
	}
	user, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return false
	}
	if !user.TotpEnabled {
		response.ErrorWithDetails(c, http.StatusBadRequest,
			"Enable two-factor authentication (TOTP) for your account before turning on step-up verification",
			"STEP_UP_ENABLE_REQUIRES_TOTP", nil)
		return false
	}
	return true
}

// settingKeyJSONAliases 记录 JSON 名称与持久化设置键不同的请求字段。
// UpdateSettingsRequest 的其他字段均使用对应设置键作为 JSON 名称。
var settingKeyJSONAliases = map[string]string{
	"smtp_from_email": notification.SettingKeySMTPFrom,
}

// settingKeyByJSONName 将 UpdateSettingsRequest 中非指针顶层 JSON 字段映射到其写入的设置键。
// 该映射只根据结构体标签构建一次，使新增字段无需修改此处也能自动纳入处理。
//
// 指针字段会被刻意排除：UpdateSettings 已通过指针为它们实现“省略即保留存储值”的合并语义，
// 部分字段还依赖每次保存时重新写入，以重新规范化故障关闭的安全状态，参见
// TestUpdateSettingsMalformedForwardedClientIPHeadersRemainFailClosedWhenOmitted。
// 只有非指针字段无法区分省略与主动清空。
var settingKeyByJSONName = buildSettingKeyByJSONName()

func buildSettingKeyByJSONName() map[string]string {
	t := reflect.TypeOf(UpdateSettingsRequest{})
	out := make(map[string]string, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
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

// omittedSettingKeys 返回当前载荷未包含的设置键。
// 设置保存接口采用整份文档 PUT 语义；若不排除这些键，只发送单个字段的客户端会把其余字段重置为零值。
func omittedSettingKeys(sentFields map[string]json.RawMessage) runtimesettings.OmittedKeys {
	omitted := make(runtimesettings.OmittedKeys, len(settingKeyByJSONName))
	for jsonName, settingKey := range settingKeyByJSONName {
		if _, sent := sentFields[jsonName]; !sent {
			omitted[settingKey] = struct{}{}
		}
	}
	return omitted
}

func SettingsAuditRequest(req UpdateSettingsRequest) UpdateSettingsRequest {
	req.TencentCaptchaAppSecretKey = strings.TrimSpace(req.TencentCaptchaAppSecretKey)
	req.TencentCaptchaCloudSecretID = strings.TrimSpace(req.TencentCaptchaCloudSecretID)
	req.TencentCaptchaCloudSecretKey = strings.TrimSpace(req.TencentCaptchaCloudSecretKey)
	req.AliyunCaptchaAccessKeySecret = strings.TrimSpace(req.AliyunCaptchaAccessKeySecret)
	return req
}

func (h *Handler) UpdateSettings(c *gin.Context) {
	var sentFields map[string]json.RawMessage
	if err := c.ShouldBindBodyWith(&sentFields, binding.JSON); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if rejectDeprecatedAdvancedSchedulerRequestFields(c, sentFields) {
		return
	}
	var req UpdateSettingsRequest
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	update, err := h.settingService.BeginSettingsUpdate(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer update.Close()
	c.Request = c.Request.WithContext(update.Context())

	// 管理端保存白名单时按实际分组平台收敛能力，清理已下线的 Gemini inpaint。
	if req.CreativeModelSettings != nil {
		if sanitizer, ok := h.creativeModelReader.(interface {
			NormalizeCreativeModelSettingsForSave(context.Context, []creative.CreativeModelSetting) ([]creative.CreativeModelSetting, error)
		}); ok {
			normalized, normalizeErr := sanitizer.NormalizeCreativeModelSettingsForSave(c.Request.Context(), *req.CreativeModelSettings)
			if normalizeErr != nil {
				response.BadRequest(c, "Invalid creative model settings: "+normalizeErr.Error())
				return
			}
			req.CreativeModelSettings = &normalized
		}
	}
	auditReq := SettingsAuditRequest(req)
	omitted := omittedSettingKeys(sentFields)

	previousSettings, err := h.settingService.GetAllSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	previousAuthSourceDefaults, err := h.settingService.GetAuthSourceDefaultSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// 新增开关使用指针字段：省略字段=保持现值，避免旧客户端或脚本全量保存时静默重置。
	registrationEmailDomainQuotaEnabled := previousSettings.RegistrationEmailDomainQuotaEnabled
	if req.RegistrationEmailDomainQuotaEnabled != nil {
		registrationEmailDomainQuotaEnabled = *req.RegistrationEmailDomainQuotaEnabled
	}
	userEmailChangeEnabled := previousSettings.UserEmailChangeEnabled
	if req.UserEmailChangeEnabled != nil {
		userEmailChangeEnabled = *req.UserEmailChangeEnabled
	}
	// 两个安全开关同样保留省略字段语义。
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
		forwardedClientIPHeaders = append([]string(nil), (*req.ForwardedClientIPHeaders)...)
	}

	// 开启敏感操作 step-up 门控属自锁风险操作：仅允许本人已启用 TOTP 的管理员会话开启，
	// 否则开启后操作者立即被挡在所有敏感操作之外。仅在 false→true 的开启瞬间校验，
	// 保持开启状态的常规设置保存不受影响。
	if stepUpEnabled && !previousSettings.StepUpEnabled {
		if !h.ensureActorTotpForStepUp(c) {
			return
		}
	}
	// 关闭 step-up 门控本身就是敏感操作：防止拿到管理员会话的攻击者先关闸再执行导出/备份。
	// previousSettings 已证实开关处于开启状态，使用无条件门控变体，
	// 避免门控内部二次读取开关时因存储故障 fail-open（前端捕获 STEP_UP_REQUIRED 弹码重试）。
	if !stepUpEnabled && previousSettings.StepUpEnabled {
		if !identityhttp.EnforceStepUp(c, h.totpService, h.userService, nil) {
			return
		}
	}

	// 验证参数
	if req.DefaultConcurrency < 1 {
		req.DefaultConcurrency = 1
	}
	if req.DefaultBalance < 0 {
		req.DefaultBalance = 0
	}
	if req.DefaultUserAPIKeyLimit != nil && !identity.IsValidUserAPIKeyLimit(*req.DefaultUserAPIKeyLimit) {
		response.ErrorFrom(c, identity.ErrUserAPIKeyLimitInvalid)
		return
	}
	normalizedPromotion := promotion.NormalizeAdminSettings(promotion.AdminSettings{AffiliateRebateRate: req.AffiliateRebateRate, AffiliateRebateFreezeHours: req.AffiliateRebateFreezeHours, AffiliateRebateDurationDays: req.AffiliateRebateDurationDays, AffiliateRebatePerInviteeCap: req.AffiliateRebatePerInviteeCap})
	req.AffiliateRebateRate = normalizedPromotion.AffiliateRebateRate
	req.AffiliateRebateFreezeHours = normalizedPromotion.AffiliateRebateFreezeHours
	req.AffiliateRebateDurationDays = normalizedPromotion.AffiliateRebateDurationDays
	req.AffiliateRebatePerInviteeCap = normalizedPromotion.AffiliateRebatePerInviteeCap
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
	// 通用表格配置：兼容旧客户端未传字段时保留当前值。
	if req.TableDefaultPageSize <= 0 {
		req.TableDefaultPageSize = previousSettings.TableDefaultPageSize
	}
	if req.TablePageSizeOptions == nil {
		req.TablePageSizeOptions = previousSettings.TablePageSizeOptions
	}
	// 用量领域解释存在性、排序与展示规则，HTTP 只投影请求。
	usageRanking, rankingErr := usage.ResolveRankingSettings(usage.UsageRankingSettings{
		Enabled: previousSettings.UsageRankingEnabled, SortBy: usage.UsageRankingSortBy(previousSettings.UsageRankingSortBy), ShowTotalTokens: previousSettings.UsageRankingShowTotalTokens, ShowRequests: previousSettings.UsageRankingShowRequests, ShowActualCost: previousSettings.UsageRankingShowActualCost, Limit: previousSettings.UsageRankingLimit,
	}, usage.RankingSettingsUpdate{Limit: req.UsageRankingLimit, Enabled: req.UsageRankingEnabled, SortBy: req.UsageRankingSortBy, ShowTotalTokens: req.UsageRankingShowTotalTokens, ShowRequests: req.UsageRankingShowRequests, ShowActualCost: req.UsageRankingShowActualCost})
	if rankingErr != nil {
		response.BadRequest(c, rankingErr.Error())
		return
	}
	req.UsageRankingLimit = usageRanking.Limit
	// 管理文档的 SMTP 保护由通知模块解释。
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
	enabledCaptchaProviders := 0
	for _, enabled := range []bool{turnstileEnabled, tencentCaptchaEnabled, aliyunCaptchaEnabled} {
		if enabled {
			enabledCaptchaProviders++
		}
	}
	if enabledCaptchaProviders > 1 {
		response.BadRequest(c, "Multiple captcha providers (Cloudflare Turnstile / Tencent Captcha / Aliyun Captcha) cannot be enabled at the same time")
		return
	}
	// 规范化阿里云地域：未发送时保留已存值，非法值一律按中国内地落库。
	if _, sent := sentFields["aliyun_captcha_region"]; !sent {
		req.AliyunCaptchaRegion = previousSettings.AliyunCaptchaRegion
	}
	if req.AliyunCaptchaRegion != identity.AliyunCaptchaRegionSGP {
		req.AliyunCaptchaRegion = identity.AliyunCaptchaRegionCN
	}
	// 天御站点 normalize：未发送保留已存值，非法值一律按中国站落库
	if _, sent := sentFields["tencent_captcha_region"]; !sent {
		req.TencentCaptchaRegion = previousSettings.TencentCaptchaRegion
	}
	if req.TencentCaptchaRegion != identity.TencentCaptchaRegionINTL {
		req.TencentCaptchaRegion = identity.TencentCaptchaRegionCN
	}

	// Turnstile 参数验证
	if req.TurnstileEnabled {
		// 检查必填字段
		if req.TurnstileSiteKey == "" {
			response.BadRequest(c, "Turnstile Site Key is required when enabled")
			return
		}
		// 如果未提供 secret key，使用已保存的值（留空保留当前值）
		if req.TurnstileSecretKey == "" {
			if previousSettings.TurnstileSecretKey == "" {
				response.BadRequest(c, "Turnstile Secret Key is required when enabled")
				return
			}
			req.TurnstileSecretKey = previousSettings.TurnstileSecretKey
		}

		// 当 site_key 或 secret_key 任一变化时验证（避免配置错误导致无法登录）
		siteKeyChanged := previousSettings.TurnstileSiteKey != req.TurnstileSiteKey
		secretKeyChanged := previousSettings.TurnstileSecretKey != req.TurnstileSecretKey
		if siteKeyChanged || secretKeyChanged {
			if err := h.turnstileService.ValidateSecretKey(c.Request.Context(), req.TurnstileSecretKey); err != nil {
				response.ErrorFrom(c, err)
				return
			}
		}
	}

	if tencentCaptchaEnabled {
		if _, sent := sentFields["tencent_captcha_app_id"]; !sent {
			req.TencentCaptchaAppID = previousSettings.TencentCaptchaAppID
		}
		appID, err := strconv.ParseUint(req.TencentCaptchaAppID, 10, 64)
		if err != nil || appID == 0 {
			response.BadRequest(c, "Tencent Captcha CaptchaAppId must be a positive integer when enabled")
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
			response.BadRequest(c, "Tencent Captcha AppSecretKey is required when enabled")
			return
		}
		if req.TencentCaptchaCloudSecretID == "" {
			response.BadRequest(c, "Tencent Cloud SecretId is required when Tencent Captcha is enabled")
			return
		}
		if req.TencentCaptchaCloudSecretKey == "" {
			response.BadRequest(c, "Tencent Cloud SecretKey is required when Tencent Captcha is enabled")
			return
		}
	}

	// 阿里云验证码 2.0 参数验证
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
			response.BadRequest(c, "Aliyun Captcha Scene ID is required when enabled")
			return
		}
		if req.AliyunCaptchaPrefix == "" {
			response.BadRequest(c, "Aliyun Captcha Prefix is required when enabled")
			return
		}
		if req.AliyunCaptchaAccessKeyID == "" {
			response.BadRequest(c, "Aliyun Captcha AccessKey ID is required when enabled")
			return
		}
		// 如果未提供 AccessKey Secret，使用已保存的值（留空保留当前值）
		if req.AliyunCaptchaAccessKeySecret == "" {
			if previousSettings.AliyunCaptchaAccessKeySecret == "" {
				response.BadRequest(c, "Aliyun Captcha AccessKey Secret is required when enabled")
				return
			}
			req.AliyunCaptchaAccessKeySecret = previousSettings.AliyunCaptchaAccessKeySecret
		}

		// 凭证任一变化时真实调用一次阿里云校验（避免配置错误导致无法登录）
		credentialsChanged := previousSettings.AliyunCaptchaAccessKeyID != req.AliyunCaptchaAccessKeyID ||
			previousSettings.AliyunCaptchaAccessKeySecret != req.AliyunCaptchaAccessKeySecret ||
			previousSettings.AliyunCaptchaSceneID != req.AliyunCaptchaSceneID ||
			previousSettings.AliyunCaptchaRegion != req.AliyunCaptchaRegion
		if credentialsChanged {
			if err := h.aliyunCaptchaService.ValidateCredentials(c.Request.Context(), req.AliyunCaptchaAccessKeyID, req.AliyunCaptchaAccessKeySecret, req.AliyunCaptchaSceneID, req.AliyunCaptchaRegion); err != nil {
				response.ErrorFrom(c, err)
				return
			}
		}
	}

	// TOTP 双因素认证参数验证
	// 只有手动配置了加密密钥才允许启用 TOTP 功能
	if req.TotpEnabled && !previousSettings.TotpEnabled {
		// 尝试启用 TOTP，检查加密密钥是否已手动配置
		if !h.settingService.IsTotpEncryptionKeyConfigured() {
			response.BadRequest(c, "Cannot enable TOTP: TOTP_ENCRYPTION_KEY environment variable must be configured first. Generate a key with 'openssl rand -hex 32' and set it in your environment.")
			return
		}
	}
	loginAgreementMode := strings.ToLower(strings.TrimSpace(req.LoginAgreementMode))
	if loginAgreementMode == "" {
		loginAgreementMode = strings.ToLower(strings.TrimSpace(previousSettings.LoginAgreementMode))
	}
	switch loginAgreementMode {
	case "", "modal":
		loginAgreementMode = "modal"
	case "checkbox":
	default:
		response.BadRequest(c, "Login agreement mode must be modal or checkbox")
		return
	}
	loginAgreementUpdatedAt := strings.TrimSpace(req.LoginAgreementUpdatedAt)
	if loginAgreementUpdatedAt == "" {
		loginAgreementUpdatedAt = strings.TrimSpace(previousSettings.LoginAgreementUpdatedAt)
	}
	loginAgreementDocuments := loginAgreementDocumentsToService(req.LoginAgreementDocuments)
	if len(loginAgreementDocuments) == 0 {
		loginAgreementDocuments = previousSettings.LoginAgreementDocuments
	}
	for _, doc := range loginAgreementDocuments {
		if strings.TrimSpace(doc.Title) == "" {
			response.BadRequest(c, "Login agreement document title is required")
			return
		}
		if len(doc.Title) > 80 {
			response.BadRequest(c, "Login agreement document title is too long (max 80 characters)")
			return
		}
		if len(doc.ContentMD) > 200*1024 {
			response.BadRequest(c, "Login agreement document content is too large (max 200KB)")
			return
		}
	}
	if req.LoginAgreementEnabled && len(loginAgreementDocuments) == 0 {
		response.BadRequest(c, "Login agreement documents are required when enabled")
		return
	}

	// LinuxDo Connect 参数验证
	if req.LinuxDoConnectEnabled {
		req.LinuxDoConnectClientID = strings.TrimSpace(req.LinuxDoConnectClientID)
		req.LinuxDoConnectClientSecret = strings.TrimSpace(req.LinuxDoConnectClientSecret)
		req.LinuxDoConnectRedirectURL = strings.TrimSpace(req.LinuxDoConnectRedirectURL)

		if req.LinuxDoConnectClientID == "" {
			response.BadRequest(c, "LinuxDo Client ID is required when enabled")
			return
		}
		if req.LinuxDoConnectRedirectURL == "" {
			response.BadRequest(c, "LinuxDo Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(req.LinuxDoConnectRedirectURL); err != nil {
			response.BadRequest(c, "LinuxDo Redirect URL must be an absolute http(s) URL")
			return
		}

		// 如果未提供 client_secret，则保留现有值（如有）。
		if req.LinuxDoConnectClientSecret == "" {
			if previousSettings.LinuxDoConnectClientSecret == "" {
				response.BadRequest(c, "LinuxDo Client Secret is required when enabled")
				return
			}
			req.LinuxDoConnectClientSecret = previousSettings.LinuxDoConnectClientSecret
		}
	}

	// DingTalk Connect 参数验证
	// 防御性：任何写入路径上把已废弃的 corp_restriction_policy=whitelist 入参 coerce 为 none，
	// 避免任何直连 admin API 的客户端把死值写回 DB（前端 UI 已无此选项）。
	req.DingTalkConnectCorpRestrictionPolicy = identity.SettingsCoerceDingTalkCorpPolicyForWrite(req.DingTalkConnectCorpRestrictionPolicy)

	if req.DingTalkConnectEnabled {
		req.DingTalkConnectClientID = strings.TrimSpace(req.DingTalkConnectClientID)
		req.DingTalkConnectClientSecret = strings.TrimSpace(req.DingTalkConnectClientSecret)
		req.DingTalkConnectRedirectURL = strings.TrimSpace(req.DingTalkConnectRedirectURL)
		req.DingTalkConnectCorpRestrictionPolicy = strings.TrimSpace(req.DingTalkConnectCorpRestrictionPolicy)
		req.DingTalkConnectInternalCorpID = strings.TrimSpace(req.DingTalkConnectInternalCorpID)

		if req.DingTalkConnectClientID == "" {
			response.BadRequest(c, "DingTalk Client ID is required when enabled")
			return
		}
		if req.DingTalkConnectRedirectURL == "" {
			response.BadRequest(c, "DingTalk Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(req.DingTalkConnectRedirectURL); err != nil {
			response.BadRequest(c, "DingTalk Redirect URL must be an absolute http(s) URL")
			return
		}

		// 如果未提供 client_secret，则保留现有值（如有）。
		if req.DingTalkConnectClientSecret == "" {
			if previousSettings.DingTalkConnectClientSecret == "" {
				response.BadRequest(c, "DingTalk Client Secret is required when enabled")
				return
			}
			req.DingTalkConnectClientSecret = previousSettings.DingTalkConnectClientSecret
		}

		// Corp 策略校验（V1/V4 fail-closed）
		dingTalkCfg := authconfig.DingTalkConnectConfig{
			Enabled:               true,
			DingTalkAppKind:       "internal_app", // 硬编码：settings 层仅支持 internal_app
			AppType:               "internal",     // 对于 internal_only 策略的默认值
			CorpRestrictionPolicy: req.DingTalkConnectCorpRestrictionPolicy,
			InternalCorpID:        req.DingTalkConnectInternalCorpID,
		}
		// 若未填 corp_restriction_policy，保留已有配置
		if dingTalkCfg.CorpRestrictionPolicy == "" {
			dingTalkCfg.CorpRestrictionPolicy = previousSettings.DingTalkConnectCorpRestrictionPolicy
		}
		// 对于 internal_only 策略，app_type 必须为 internal（V1 校验）
		if dingTalkCfg.CorpRestrictionPolicy == "internal_only" {
			dingTalkCfg.AppType = "internal"
		} else {
			dingTalkCfg.AppType = "public"
		}
		if err := authconfig.ValidateDingTalkConfig(dingTalkCfg); err != nil {
			response.ErrorWithDetails(c, http.StatusBadRequest, err.Error(), mapDingTalkValidateError(err), nil)
			return
		}

		// bypass_registration 仅在 internal_only 模式下有意义；其它策略下强制为 false，
		// 防止 admin 在切换 policy 时把 bypass 残留在 DB 中（前端 UI 也已隐藏该开关）。
		if dingTalkCfg.CorpRestrictionPolicy != "internal_only" {
			req.DingTalkConnectBypassRegistration = false
			// 身份同步三开关同理：仅 internal_only 模式下有意义，其它策略强制 false。
			req.DingTalkConnectSyncCorpEmail = false
			req.DingTalkConnectSyncDisplayName = false
			req.DingTalkConnectSyncDept = false
		}
		// 身份同步目标 attr key：trimSpace + 空值 fallback 到默认值
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
		// 身份同步目标 attr 显示名称：trim + 空值 fallback 到默认中文名
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
			response.BadRequest(c, "WeChat Official Account and Mobile App cannot be enabled at the same time")
			return
		}
		if req.WeChatConnectMode != "" {
			switch req.WeChatConnectMode {
			case "open", "mp", "mobile":
			default:
				response.BadRequest(c, "WeChat mode must be open, mp, or mobile")
				return
			}
		}
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

		req.WeChatConnectOpenAppID = strings.TrimSpace(firstNonEmpty(req.WeChatConnectOpenAppID, req.WeChatConnectAppID, previousSettings.WeChatConnectOpenAppID, previousSettings.WeChatConnectAppID))
		req.WeChatConnectMPAppID = strings.TrimSpace(firstNonEmpty(req.WeChatConnectMPAppID, req.WeChatConnectAppID, previousSettings.WeChatConnectMPAppID, previousSettings.WeChatConnectAppID))
		req.WeChatConnectMobileAppID = strings.TrimSpace(firstNonEmpty(req.WeChatConnectMobileAppID, req.WeChatConnectAppID, previousSettings.WeChatConnectMobileAppID, previousSettings.WeChatConnectAppID))

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
				response.BadRequest(c, "WeChat PC App ID is required when enabled")
				return
			}
			if req.WeChatConnectOpenAppSecret == "" {
				response.BadRequest(c, "WeChat PC App Secret is required when enabled")
				return
			}
		}
		if req.WeChatConnectMPEnabled {
			if req.WeChatConnectMPAppID == "" {
				response.BadRequest(c, "WeChat Official Account App ID is required when enabled")
				return
			}
			if req.WeChatConnectMPAppSecret == "" {
				response.BadRequest(c, "WeChat Official Account App Secret is required when enabled")
				return
			}
		}
		if req.WeChatConnectMobileEnabled {
			if req.WeChatConnectMobileAppID == "" {
				response.BadRequest(c, "WeChat Mobile App ID is required when enabled")
				return
			}
			if req.WeChatConnectMobileAppSecret == "" {
				response.BadRequest(c, "WeChat Mobile App Secret is required when enabled")
				return
			}
		}

		if req.WeChatConnectScopes == "" {
			if req.WeChatConnectMPEnabled {
				req.WeChatConnectScopes = identity.SettingsDefaultWeChatConnectScopesForMode("mp")
			} else {
				req.WeChatConnectScopes = identity.SettingsDefaultWeChatConnectScopesForMode(req.WeChatConnectMode)
			}
		}
		if req.WeChatConnectOpenEnabled || req.WeChatConnectMPEnabled {
			if req.WeChatConnectRedirectURL == "" {
				response.BadRequest(c, "WeChat Redirect URL is required when web oauth is enabled")
				return
			}
			if err := authconfig.ValidateAbsoluteHTTPURL(req.WeChatConnectRedirectURL); err != nil {
				response.BadRequest(c, "WeChat Redirect URL must be an absolute http(s) URL")
				return
			}
			if req.WeChatConnectFrontendRedirectURL == "" {
				req.WeChatConnectFrontendRedirectURL = "/auth/wechat/callback"
			}
			if err := authconfig.ValidateFrontendRedirectURL(req.WeChatConnectFrontendRedirectURL); err != nil {
				response.BadRequest(c, "WeChat Frontend Redirect URL is invalid")
				return
			}
		}
	}

	// Generic OIDC 参数验证
	oidcUsePKCE, oidcValidateIDToken, err := h.settingService.OIDCSecurityWriteDefaults(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
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
		if req.OIDCConnectClockSkewSeconds == 0 {
			req.OIDCConnectClockSkewSeconds = previousSettings.OIDCConnectClockSkewSeconds
			if req.OIDCConnectClockSkewSeconds == 0 {
				req.OIDCConnectClockSkewSeconds = 120
			}
		}

		if req.OIDCConnectClientID == "" {
			response.BadRequest(c, "OIDC Client ID is required when enabled")
			return
		}
		if req.OIDCConnectIssuerURL == "" {
			response.BadRequest(c, "OIDC Issuer URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectIssuerURL); err != nil {
			response.BadRequest(c, "OIDC Issuer URL must be an absolute http(s) URL")
			return
		}
		if req.OIDCConnectDiscoveryURL != "" {
			if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectDiscoveryURL); err != nil {
				response.BadRequest(c, "OIDC Discovery URL must be an absolute http(s) URL")
				return
			}
		}
		if req.OIDCConnectAuthorizeURL != "" {
			if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectAuthorizeURL); err != nil {
				response.BadRequest(c, "OIDC Authorize URL must be an absolute http(s) URL")
				return
			}
		}
		if req.OIDCConnectTokenURL != "" {
			if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectTokenURL); err != nil {
				response.BadRequest(c, "OIDC Token URL must be an absolute http(s) URL")
				return
			}
		}
		if req.OIDCConnectUserInfoURL != "" {
			if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectUserInfoURL); err != nil {
				response.BadRequest(c, "OIDC UserInfo URL must be an absolute http(s) URL")
				return
			}
		}
		if req.OIDCConnectRedirectURL == "" {
			response.BadRequest(c, "OIDC Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectRedirectURL); err != nil {
			response.BadRequest(c, "OIDC Redirect URL must be an absolute http(s) URL")
			return
		}
		if req.OIDCConnectFrontendRedirectURL == "" {
			response.BadRequest(c, "OIDC Frontend Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateFrontendRedirectURL(req.OIDCConnectFrontendRedirectURL); err != nil {
			response.BadRequest(c, "OIDC Frontend Redirect URL is invalid")
			return
		}
		if !scopesContainOpenID(req.OIDCConnectScopes) {
			response.BadRequest(c, "OIDC scopes must contain openid")
			return
		}
		switch req.OIDCConnectTokenAuthMethod {
		case "", "client_secret_post", "client_secret_basic", "none":
		default:
			response.BadRequest(c, "OIDC Token Auth Method must be one of client_secret_post/client_secret_basic/none")
			return
		}
		if req.OIDCConnectClockSkewSeconds < 0 || req.OIDCConnectClockSkewSeconds > 600 {
			response.BadRequest(c, "OIDC clock skew seconds must be between 0 and 600")
			return
		}
		if oidcValidateIDToken && req.OIDCConnectAllowedSigningAlgs == "" {
			response.BadRequest(c, "OIDC Allowed Signing Algs is required when validate_id_token=true")
			return
		}
		if req.OIDCConnectJWKSURL != "" {
			if err := authconfig.ValidateAbsoluteHTTPURL(req.OIDCConnectJWKSURL); err != nil {
				response.BadRequest(c, "OIDC JWKS URL must be an absolute http(s) URL")
				return
			}
		}
		if req.OIDCConnectTokenAuthMethod == "" || req.OIDCConnectTokenAuthMethod == "client_secret_post" || req.OIDCConnectTokenAuthMethod == "client_secret_basic" {
			if req.OIDCConnectClientSecret == "" {
				if previousSettings.OIDCConnectClientSecret == "" {
					response.BadRequest(c, "OIDC Client Secret is required when enabled")
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
		req.GitHubOAuthClientID = strings.TrimSpace(firstNonEmpty(req.GitHubOAuthClientID, previousSettings.GitHubOAuthClientID))
		req.GitHubOAuthRedirectURL = strings.TrimSpace(firstNonEmpty(req.GitHubOAuthRedirectURL, previousSettings.GitHubOAuthRedirectURL))
		req.GitHubOAuthFrontendRedirectURL = strings.TrimSpace(firstNonEmpty(req.GitHubOAuthFrontendRedirectURL, previousSettings.GitHubOAuthFrontendRedirectURL, "/auth/oauth/callback"))
		if req.GitHubOAuthClientID == "" {
			response.BadRequest(c, "GitHub OAuth Client ID is required when enabled")
			return
		}
		if req.GitHubOAuthClientSecret == "" {
			if previousSettings.GitHubOAuthClientSecret == "" {
				response.BadRequest(c, "GitHub OAuth Client Secret is required when enabled")
				return
			}
			req.GitHubOAuthClientSecret = previousSettings.GitHubOAuthClientSecret
		}
		if req.GitHubOAuthRedirectURL == "" {
			response.BadRequest(c, "GitHub OAuth Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(req.GitHubOAuthRedirectURL); err != nil {
			response.BadRequest(c, "GitHub OAuth Redirect URL must be an absolute http(s) URL")
			return
		}
		if req.GitHubOAuthFrontendRedirectURL == "" {
			response.BadRequest(c, "GitHub OAuth Frontend Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateFrontendRedirectURL(req.GitHubOAuthFrontendRedirectURL); err != nil {
			response.BadRequest(c, "GitHub OAuth Frontend Redirect URL is invalid")
			return
		}
	}

	req.GoogleOAuthClientID = strings.TrimSpace(req.GoogleOAuthClientID)
	req.GoogleOAuthClientSecret = strings.TrimSpace(req.GoogleOAuthClientSecret)
	req.GoogleOAuthRedirectURL = strings.TrimSpace(req.GoogleOAuthRedirectURL)
	req.GoogleOAuthFrontendRedirectURL = strings.TrimSpace(req.GoogleOAuthFrontendRedirectURL)
	if req.GoogleOAuthEnabled {
		req.GoogleOAuthClientID = strings.TrimSpace(firstNonEmpty(req.GoogleOAuthClientID, previousSettings.GoogleOAuthClientID))
		req.GoogleOAuthRedirectURL = strings.TrimSpace(firstNonEmpty(req.GoogleOAuthRedirectURL, previousSettings.GoogleOAuthRedirectURL))
		req.GoogleOAuthFrontendRedirectURL = strings.TrimSpace(firstNonEmpty(req.GoogleOAuthFrontendRedirectURL, previousSettings.GoogleOAuthFrontendRedirectURL, "/auth/oauth/callback"))
		if req.GoogleOAuthClientID == "" {
			response.BadRequest(c, "Google OAuth Client ID is required when enabled")
			return
		}
		if req.GoogleOAuthClientSecret == "" {
			if previousSettings.GoogleOAuthClientSecret == "" {
				response.BadRequest(c, "Google OAuth Client Secret is required when enabled")
				return
			}
			req.GoogleOAuthClientSecret = previousSettings.GoogleOAuthClientSecret
		}
		if req.GoogleOAuthRedirectURL == "" {
			response.BadRequest(c, "Google OAuth Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(req.GoogleOAuthRedirectURL); err != nil {
			response.BadRequest(c, "Google OAuth Redirect URL must be an absolute http(s) URL")
			return
		}
		if req.GoogleOAuthFrontendRedirectURL == "" {
			response.BadRequest(c, "Google OAuth Frontend Redirect URL is required when enabled")
			return
		}
		if err := authconfig.ValidateFrontendRedirectURL(req.GoogleOAuthFrontendRedirectURL); err != nil {
			response.BadRequest(c, "Google OAuth Frontend Redirect URL is invalid")
			return
		}
	}

	// “购买订阅”页面配置验证
	purchaseEnabled := previousSettings.PurchaseSubscriptionEnabled
	if req.PurchaseSubscriptionEnabled != nil {
		purchaseEnabled = *req.PurchaseSubscriptionEnabled
	}
	purchaseURL := previousSettings.PurchaseSubscriptionURL
	if req.PurchaseSubscriptionURL != nil {
		purchaseURL = strings.TrimSpace(*req.PurchaseSubscriptionURL)
	}

	// - 启用时要求 URL 合法且非空
	// - 禁用时允许为空；若提供了 URL 也做基本校验，避免误配置
	if purchaseEnabled {
		if purchaseURL == "" {
			response.BadRequest(c, "Purchase Subscription URL is required when enabled")
			return
		}
		if err := authconfig.ValidateAbsoluteHTTPURL(purchaseURL); err != nil {
			response.BadRequest(c, "Purchase Subscription URL must be an absolute http(s) URL")
			return
		}
	} else if purchaseURL != "" {
		if err := authconfig.ValidateAbsoluteHTTPURL(purchaseURL); err != nil {
			response.BadRequest(c, "Purchase Subscription URL must be an absolute http(s) URL")
			return
		}
	}

	// Frontend URL 验证
	req.FrontendURL = strings.TrimSpace(req.FrontendURL)
	if req.FrontendURL != "" {
		if err := authconfig.ValidateAbsoluteHTTPURL(req.FrontendURL); err != nil {
			response.BadRequest(c, "Frontend URL must be an absolute http(s) URL")
			return
		}
	}

	// 自定义菜单项验证
	const (
		maxCustomMenuItems    = 20
		maxMenuItemLabelLen   = 50
		maxMenuItemURLLen     = 2048
		maxMenuItemIconSVGLen = 10 * 1024 // 10KB
		maxMenuItemIDLen      = 32
	)

	customMenuJSON := previousSettings.CustomMenuItems
	if req.CustomMenuItems != nil {
		items := *req.CustomMenuItems
		if len(items) > maxCustomMenuItems {
			response.BadRequest(c, "Too many custom menu items (max 20)")
			return
		}
		for i, item := range items {
			if strings.TrimSpace(item.Label) == "" {
				response.BadRequest(c, "Custom menu item label is required")
				return
			}
			if len(item.Label) > maxMenuItemLabelLen {
				response.BadRequest(c, "Custom menu item label is too long (max 50 characters)")
				return
			}
			urlTrimmed := strings.TrimSpace(item.URL)
			if strings.HasPrefix(urlTrimmed, "md:") {
				// Markdown 页面模式使用 md:<slug>，slug 规则与 /api/v1/pages/:slug 保持一致。
				slug := strings.TrimPrefix(urlTrimmed, "md:")
				if slug == "" {
					response.BadRequest(c, "Custom menu item markdown slug cannot be empty (use md:slug format)")
					return
				}
				if len(slug) > 64 || !markdownMenuSlugPattern.MatchString(slug) {
					response.BadRequest(c, "Custom menu item markdown slug contains invalid characters")
					return
				}
			} else {
				if urlTrimmed == "" {
					response.BadRequest(c, "Custom menu item URL is required (use md:slug for markdown pages)")
					return
				}
				if len(urlTrimmed) > maxMenuItemURLLen {
					response.BadRequest(c, "Custom menu item URL is too long (max 2048 characters)")
					return
				}
				if err := authconfig.ValidateAbsoluteHTTPURL(urlTrimmed); err != nil {
					response.BadRequest(c, "Custom menu item URL must be an absolute http(s) URL or md:<slug>")
					return
				}
			}
			// 保存规范化后的 URL，避免前后空白导致运行时 md: 页面识别失败。
			items[i].URL = urlTrimmed
			if item.Visibility != "user" && item.Visibility != "admin" {
				response.BadRequest(c, "Custom menu item visibility must be 'user' or 'admin'")
				return
			}
			if len(item.IconSVG) > maxMenuItemIconSVGLen {
				response.BadRequest(c, "Custom menu item icon SVG is too large (max 10KB)")
				return
			}
			// Auto-generate ID if missing
			if strings.TrimSpace(item.ID) == "" {
				id, err := generateMenuItemID()
				if err != nil {
					response.Error(c, http.StatusInternalServerError, "Failed to generate menu item ID")
					return
				}
				items[i].ID = id
			} else if len(item.ID) > maxMenuItemIDLen {
				response.BadRequest(c, "Custom menu item ID is too long (max 32 characters)")
				return
			} else if !menuItemIDPattern.MatchString(item.ID) {
				response.BadRequest(c, "Custom menu item ID contains invalid characters (only a-z, A-Z, 0-9, - and _ are allowed)")
				return
			}
		}
		// ID uniqueness check
		seen := make(map[string]struct{}, len(items))
		for _, item := range items {
			if _, exists := seen[item.ID]; exists {
				response.BadRequest(c, "Duplicate custom menu item ID: "+item.ID)
				return
			}
			seen[item.ID] = struct{}{}
		}
		menuBytes, err := json.Marshal(items)
		if err != nil {
			response.BadRequest(c, "Failed to serialize custom menu items")
			return
		}
		customMenuJSON = string(menuBytes)
	}

	// 自定义端点验证
	const (
		maxCustomEndpoints        = 10
		maxEndpointNameLen        = 50
		maxEndpointURLLen         = 2048
		maxEndpointDescriptionLen = 200
	)

	customEndpointsJSON := previousSettings.CustomEndpoints
	if req.CustomEndpoints != nil {
		endpoints := *req.CustomEndpoints
		if len(endpoints) > maxCustomEndpoints {
			response.BadRequest(c, "Too many custom endpoints (max 10)")
			return
		}
		for _, ep := range endpoints {
			if strings.TrimSpace(ep.Name) == "" {
				response.BadRequest(c, "Custom endpoint name is required")
				return
			}
			if len(ep.Name) > maxEndpointNameLen {
				response.BadRequest(c, "Custom endpoint name is too long (max 50 characters)")
				return
			}
			if strings.TrimSpace(ep.Endpoint) == "" {
				response.BadRequest(c, "Custom endpoint URL is required")
				return
			}
			if len(ep.Endpoint) > maxEndpointURLLen {
				response.BadRequest(c, "Custom endpoint URL is too long (max 2048 characters)")
				return
			}
			if err := authconfig.ValidateAbsoluteHTTPURL(strings.TrimSpace(ep.Endpoint)); err != nil {
				response.BadRequest(c, "Custom endpoint URL must be an absolute http(s) URL")
				return
			}
			if len(ep.Description) > maxEndpointDescriptionLen {
				response.BadRequest(c, "Custom endpoint description is too long (max 200 characters)")
				return
			}
		}
		endpointBytes, err := json.Marshal(endpoints)
		if err != nil {
			response.BadRequest(c, "Failed to serialize custom endpoints")
			return
		}
		customEndpointsJSON = string(endpointBytes)
	}

	// 底栏链接分组验证
	const (
		maxFooterGroups      = 6
		maxFooterLinksPerGrp = 10
		maxFooterLabelLen    = 50
		maxFooterURLLen      = 2048
		maxFooterTextLen     = 500
	)

	footerLinksJSON := previousSettings.FooterLinks
	if req.FooterLinks != nil {
		groups := *req.FooterLinks
		if len(groups) > maxFooterGroups {
			response.BadRequest(c, "Too many footer link groups (max 6)")
			return
		}
		for _, group := range groups {
			if strings.TrimSpace(group.Title) == "" {
				response.BadRequest(c, "Footer link group title is required")
				return
			}
			if len(group.Title) > maxFooterLabelLen {
				response.BadRequest(c, "Footer link group title is too long (max 50 characters)")
				return
			}
			if len(group.Links) > maxFooterLinksPerGrp {
				response.BadRequest(c, "Too many links in footer group (max 10)")
				return
			}
			for _, link := range group.Links {
				if strings.TrimSpace(link.Label) == "" {
					response.BadRequest(c, "Footer link label is required")
					return
				}
				if len(link.Label) > maxFooterLabelLen {
					response.BadRequest(c, "Footer link label is too long (max 50 characters)")
					return
				}
				trimmedURL := strings.TrimSpace(link.URL)
				if trimmedURL == "" {
					response.BadRequest(c, "Footer link URL is required")
					return
				}
				if len(trimmedURL) > maxFooterURLLen {
					response.BadRequest(c, "Footer link URL is too long (max 2048 characters)")
					return
				}
				// 允许绝对 http(s) URL 或站内相对路径（以 / 开头）
				if !strings.HasPrefix(trimmedURL, "/") {
					if err := authconfig.ValidateAbsoluteHTTPURL(trimmedURL); err != nil {
						response.BadRequest(c, "Footer link URL must be an absolute http(s) URL or a path starting with /")
						return
					}
				}
			}
		}
		groupBytes, err := json.Marshal(groups)
		if err != nil {
			response.BadRequest(c, "Failed to serialize footer links")
			return
		}
		footerLinksJSON = string(groupBytes)
	}

	// 首页展示模型列表验证：限制数量、去空白、去重，保持管理员配置的顺序
	const maxHomeFeaturedModels = 12
	const maxHomeFeaturedModelIDLen = 200

	homeFeaturedModelsJSON := previousSettings.HomeFeaturedModels
	if req.HomeFeaturedModels != nil {
		models := *req.HomeFeaturedModels
		if len(models) > maxHomeFeaturedModels {
			response.BadRequest(c, "Too many home featured models (max 12)")
			return
		}
		seen := make(map[string]struct{}, len(models))
		normalized := make([]string, 0, len(models))
		for _, model := range models {
			trimmed := strings.TrimSpace(model)
			if trimmed == "" {
				response.BadRequest(c, "Home featured model ID is required")
				return
			}
			if len(trimmed) > maxHomeFeaturedModelIDLen {
				response.BadRequest(c, "Home featured model ID is too long (max 200 characters)")
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
			response.BadRequest(c, "Failed to serialize home featured models")
			return
		}
		homeFeaturedModelsJSON = string(modelBytes)
	}

	footerText := previousSettings.FooterText
	if req.FooterText != nil {
		trimmed := strings.TrimSpace(*req.FooterText)
		if len(trimmed) > maxFooterTextLen {
			response.BadRequest(c, "Footer text is too long (max 500 characters)")
			return
		}
		footerText = trimmed
	}

	// Ops metrics collector interval validation (seconds).
	if req.OpsMetricsIntervalSeconds != nil {
		v := *req.OpsMetricsIntervalSeconds
		if v < 60 {
			v = 60
		}
		if v > 3600 {
			v = 3600
		}
		req.OpsMetricsIntervalSeconds = &v
	}
	defaultSubscriptions := make([]billing.DefaultSubscriptionSetting, 0, len(req.DefaultSubscriptions))
	for _, sub := range req.DefaultSubscriptions {
		defaultSubscriptions = append(defaultSubscriptions, billing.DefaultSubscriptionSetting{
			PlanID: sub.PlanID,
		})
	}

	// 验证最低版本号格式（空字符串=禁用，或合法 semver）
	if req.MinClaudeCodeVersion != "" {
		if !semverPattern.MatchString(req.MinClaudeCodeVersion) {
			response.Error(c, http.StatusBadRequest, "min_claude_code_version must be empty or a valid semver (e.g. 2.1.63)")
			return
		}
	}

	// 验证最高版本号格式（空字符串=禁用，或合法 semver）
	if req.MaxClaudeCodeVersion != "" {
		if !semverPattern.MatchString(req.MaxClaudeCodeVersion) {
			response.Error(c, http.StatusBadRequest, "max_claude_code_version must be empty or a valid semver (e.g. 3.0.0)")
			return
		}
	}
	if req.AntigravityUserAgentVersion != nil {
		normalized := strings.TrimSpace(*req.AntigravityUserAgentVersion)
		req.AntigravityUserAgentVersion = &normalized
		if normalized != "" && !semverPattern.MatchString(normalized) {
			response.Error(c, http.StatusBadRequest, "antigravity_user_agent_version must be empty or a valid semver (e.g. 1.23.2)")
			return
		}
	}
	if req.OpenAICodexUserAgent != nil {
		normalized := strings.TrimSpace(*req.OpenAICodexUserAgent)
		req.OpenAICodexUserAgent = &normalized
		// 仅做长度上限保护，不限制具体格式（运维需要可自由调整 codex 版本号）
		if len(normalized) > 512 {
			response.Error(c, http.StatusBadRequest, "openai_codex_user_agent must be at most 512 characters")
			return
		}
	}

	// 交叉验证：如果同时设置了最低和最高版本号，最高版本号必须 >= 最低版本号
	if req.MinClaudeCodeVersion != "" && req.MaxClaudeCodeVersion != "" {
		if clientmeta.CompareVersions(req.MaxClaudeCodeVersion, req.MinClaudeCodeVersion) < 0 {
			response.Error(c, http.StatusBadRequest, "max_claude_code_version must be greater than or equal to min_claude_code_version")
			return
		}
	}
	if req.CyberSessionBlockTTLSeconds != nil && *req.CyberSessionBlockTTLSeconds <= 0 {
		response.BadRequest(c, "cyber_session_block_ttl_seconds must be > 0")
		return
	}
	if req.CreativeWorkerCount != nil && *req.CreativeWorkerCount <= 0 {
		response.BadRequest(c, "creative_worker_count must be > 0")
		return
	}

	settings := &composite.Snapshot{
		// 系统全局 platform quota 默认值（整体替换语义）
		DefaultPlatformQuotas:       req.DefaultPlatformQuotas,
		AccountSchedulingThresholds: req.AccountSchedulingThresholds,

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
		SiteName:                               req.SiteName,
		SiteLogo:                               req.SiteLogo,
		SiteSubtitle:                           req.SiteSubtitle,
		SiteNameZh:                             req.SiteNameZh,
		SiteNameEn:                             req.SiteNameEn,
		SiteTitleZh:                            req.SiteTitleZh,
		SiteTitleEn:                            req.SiteTitleEn,
		SiteSubtitleZh:                         req.SiteSubtitleZh,
		SiteSubtitleEn:                         req.SiteSubtitleEn,
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
		GrokCrossClientModelMapEnabled: func() bool {
			if req.GrokCrossClientModelMapEnabled != nil {
				return *req.GrokCrossClientModelMapEnabled
			}
			return previousSettings.GrokCrossClientModelMapEnabled
		}(),
		GrokDefaultBaseURLMode: func() string {
			if req.GrokDefaultBaseURLMode != nil {
				return strings.TrimSpace(*req.GrokDefaultBaseURLMode)
			}
			return previousSettings.GrokDefaultBaseURLMode
		}(),
		EnableIdentityPatch:         req.EnableIdentityPatch,
		IdentityPatchPrompt:         req.IdentityPatchPrompt,
		MinClaudeCodeVersion:        req.MinClaudeCodeVersion,
		MaxClaudeCodeVersion:        req.MaxClaudeCodeVersion,
		AllowUngroupedKeyScheduling: req.AllowUngroupedKeyScheduling,
		BackendModeEnabled:          req.BackendModeEnabled,
		OpenAITTFTMode: func() string {
			if req.OpenAITTFTMode != nil {
				return *req.OpenAITTFTMode
			}
			return previousSettings.OpenAITTFTMode
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
		OpenAIQuotaAutoPauseSettings: func() account.QuotaAutoPauseSettings {
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
		AccountQuotaNotifyEnabled: func() bool {
			if req.AccountQuotaNotifyEnabled != nil {
				return *req.AccountQuotaNotifyEnabled
			}
			return previousSettings.AccountQuotaNotifyEnabled
		}(),
		AccountQuotaNotifyEmails: func() []contact.Entry {
			if req.AccountQuotaNotifyEmails != nil {
				return identitydto.NotifyEmailEntriesToIdentity(*req.AccountQuotaNotifyEmails)
			}
			return previousSettings.AccountQuotaNotifyEmails
		}(),
	}

	// req.AuthSourceXxxPlatformQuotas 为 nil 表示本次请求未包含该 source 的 quota 配置（保留 previousAuthSourceDefaults 中的值）；
	// non-nil（含 empty map）表示整体覆盖：empty map = 清空该 source 的所有 quota 配置。
	authSourceDefaults := &identity.AuthSourceDefaultSettings{
		Email: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultEmailBalance, previousAuthSourceDefaults.Email.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultEmailConcurrency, previousAuthSourceDefaults.Email.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultEmailSubscriptions, previousAuthSourceDefaults.Email.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultEmailGrantOnSignup, previousAuthSourceDefaults.Email.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultEmailGrantOnFirstBind, previousAuthSourceDefaults.Email.GrantOnFirstBind),
			PlatformQuotas:   platformQuotasValueOrDefault(req.AuthSourceEmailPlatformQuotas, previousAuthSourceDefaults.Email.PlatformQuotas),
		},
		LinuxDo: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultLinuxDoBalance, previousAuthSourceDefaults.LinuxDo.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultLinuxDoConcurrency, previousAuthSourceDefaults.LinuxDo.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultLinuxDoSubscriptions, previousAuthSourceDefaults.LinuxDo.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultLinuxDoGrantOnSignup, previousAuthSourceDefaults.LinuxDo.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultLinuxDoGrantOnFirstBind, previousAuthSourceDefaults.LinuxDo.GrantOnFirstBind),
			PlatformQuotas:   platformQuotasValueOrDefault(req.AuthSourceLinuxDoPlatformQuotas, previousAuthSourceDefaults.LinuxDo.PlatformQuotas),
		},
		OIDC: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultOIDCBalance, previousAuthSourceDefaults.OIDC.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultOIDCConcurrency, previousAuthSourceDefaults.OIDC.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultOIDCSubscriptions, previousAuthSourceDefaults.OIDC.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultOIDCGrantOnSignup, previousAuthSourceDefaults.OIDC.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultOIDCGrantOnFirstBind, previousAuthSourceDefaults.OIDC.GrantOnFirstBind),
			PlatformQuotas:   platformQuotasValueOrDefault(req.AuthSourceOIDCPlatformQuotas, previousAuthSourceDefaults.OIDC.PlatformQuotas),
		},
		WeChat: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultWeChatBalance, previousAuthSourceDefaults.WeChat.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultWeChatConcurrency, previousAuthSourceDefaults.WeChat.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultWeChatSubscriptions, previousAuthSourceDefaults.WeChat.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultWeChatGrantOnSignup, previousAuthSourceDefaults.WeChat.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultWeChatGrantOnFirstBind, previousAuthSourceDefaults.WeChat.GrantOnFirstBind),
			PlatformQuotas:   platformQuotasValueOrDefault(req.AuthSourceWeChatPlatformQuotas, previousAuthSourceDefaults.WeChat.PlatformQuotas),
		},
		GitHub: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultGitHubBalance, previousAuthSourceDefaults.GitHub.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultGitHubConcurrency, previousAuthSourceDefaults.GitHub.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultGitHubSubscriptions, previousAuthSourceDefaults.GitHub.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultGitHubGrantOnSignup, previousAuthSourceDefaults.GitHub.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultGitHubGrantOnFirstBind, previousAuthSourceDefaults.GitHub.GrantOnFirstBind),
			PlatformQuotas:   platformQuotasValueOrDefault(req.AuthSourceGitHubPlatformQuotas, previousAuthSourceDefaults.GitHub.PlatformQuotas),
		},
		Google: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultGoogleBalance, previousAuthSourceDefaults.Google.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultGoogleConcurrency, previousAuthSourceDefaults.Google.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultGoogleSubscriptions, previousAuthSourceDefaults.Google.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultGoogleGrantOnSignup, previousAuthSourceDefaults.Google.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultGoogleGrantOnFirstBind, previousAuthSourceDefaults.Google.GrantOnFirstBind),
			PlatformQuotas:   platformQuotasValueOrDefault(req.AuthSourceGooglePlatformQuotas, previousAuthSourceDefaults.Google.PlatformQuotas),
		},
		DingTalk: identity.ProviderDefaultGrantSettings{
			Balance:          float64ValueOrDefault(req.AuthSourceDefaultDingTalkBalance, previousAuthSourceDefaults.DingTalk.Balance),
			Concurrency:      intValueOrDefault(req.AuthSourceDefaultDingTalkConcurrency, previousAuthSourceDefaults.DingTalk.Concurrency),
			Subscriptions:    defaultSubscriptionsValueOrDefault(req.AuthSourceDefaultDingTalkSubscriptions, previousAuthSourceDefaults.DingTalk.Subscriptions),
			GrantOnSignup:    boolValueOrDefault(req.AuthSourceDefaultDingTalkGrantOnSignup, previousAuthSourceDefaults.DingTalk.GrantOnSignup),
			GrantOnFirstBind: boolValueOrDefault(req.AuthSourceDefaultDingTalkGrantOnFirstBind, previousAuthSourceDefaults.DingTalk.GrantOnFirstBind),
			PlatformQuotas:   platformQuotasValueOrDefault(req.AuthSourceDingTalkPlatformQuotas, previousAuthSourceDefaults.DingTalk.PlatformQuotas),
		},
		ForceEmailOnThirdPartySignup: boolValueOrDefault(req.ForceEmailOnThirdPartySignup, previousAuthSourceDefaults.ForceEmailOnThirdPartySignup),
	}
	values, err := h.settingService.PrepareSettingsWithAuthSourceDefaults(c.Request.Context(), settings, nil, omitted)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	changes := []runtimesettings.PreparedChange{{Module: "system-values", Values: values}}
	if native, ok := h.settingService.(interface {
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

	// 已提取模块由静态参与者统一准备；保持全部准备成功后才执行唯一批量提交。
	rawInput, err := json.Marshal(req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var fields runtimesettings.Fields
	if err = json.Unmarshal(rawInput, &fields); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// 非指针字段省略时仍保留原持久值；指针字段沿用已有的合并和安全规范化流程。
	for name := range settingKeyByJSONName {
		if _, sent := sentFields[name]; !sent {
			delete(fields, name)
		}
	}
	// 此指针字段原先每次保存都会写回合并后的当前值，继续保留该行为。
	fields["affiliate_admin_recharge_enabled"], _ = json.Marshal(settings.AdminRechargeRebateEnabled)
	fields["usage_ranking_enabled"], _ = json.Marshal(settings.UsageRankingEnabled)
	fields["usage_ranking_sort_by"], _ = json.Marshal(settings.UsageRankingSortBy)
	fields["usage_ranking_show_total_tokens"], _ = json.Marshal(settings.UsageRankingShowTotalTokens)
	fields["usage_ranking_show_requests"], _ = json.Marshal(settings.UsageRankingShowRequests)
	fields["usage_ranking_show_actual_cost"], _ = json.Marshal(settings.UsageRankingShowActualCost)
	fields["allow_user_view_error_requests"], _ = json.Marshal(settings.AllowUserViewErrorRequests)
	// 身份字段只投影已经通过原权限/兼容合并的值，仍按原非指针省略规则过滤。
	identityRaw, err := json.Marshal(settings.IdentityAdminSettings())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var identityFields runtimesettings.Fields
	if err = json.Unmarshal(identityRaw, &identityFields); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	for name, value := range identity.AuthSourceParticipantFields(authSourceDefaults) {
		identityFields[name] = value
	}
	for name, value := range identityFields {
		if _, nonPointer := settingKeyByJSONName[name]; nonPointer {
			if _, sent := sentFields[name]; !sent {
				continue
			}
		}
		fields[name] = value
	}
	fields["team_enabled"], _ = json.Marshal(settings.TeamEnabled)
	fields["risk_control_enabled"], _ = json.Marshal(settings.RiskControlEnabled)
	fields["cyber_session_block_enabled"], _ = json.Marshal(settings.CyberSessionBlockEnabled)
	fields["cyber_session_block_ttl_seconds"], _ = json.Marshal(settings.CyberSessionBlockTTLSeconds)
	fields["creative_enabled"], _ = json.Marshal(settings.CreativeEnabled)
	fields["creative_worker_count"], _ = json.Marshal(settings.CreativeWorkerCount)
	fields["creative_model_settings"], _ = json.Marshal(settings.CreativeModelSettings)
	fields["api_key_acl_trust_forwarded_ip"], _ = json.Marshal(settings.APIKeyACLTrustForwardedIP)
	fields["forwarded_client_ip_headers"], _ = json.Marshal(settings.ForwardedClientIPHeaders)
	billingRaw, err := json.Marshal(settings.BillingAdminSettings())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var billingFields runtimesettings.Fields
	if err = json.Unmarshal(billingRaw, &billingFields); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	for name, value := range billingFields {
		if _, nonPointer := settingKeyByJSONName[name]; nonPointer {
			if _, sent := sentFields[name]; !sent {
				continue
			}
		}
		fields[name] = value
	}
	schedulerFields, err := settings.SchedulerAdminSettings().ParticipantFields()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	delete(fields, "advanced_scheduler_sticky_escape_enabled")
	for name, value := range schedulerFields {
		if _, nonPointer := settingKeyByJSONName[name]; nonPointer {
			if _, sent := sentFields[name]; !sent {
				continue
			}
		}
		fields[name] = value
	}
	routingRaw, err := json.Marshal(settings.RoutingAdminSettings())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var routingFields runtimesettings.Fields
	if err = json.Unmarshal(routingRaw, &routingFields); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	for name, value := range routingFields {
		if _, nonPointer := settingKeyByJSONName[name]; nonPointer {
			if _, sent := sentFields[name]; !sent {
				continue
			}
		}
		fields[name] = value
	}
	siteRaw, err := json.Marshal(settings.SiteAdminSettings())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var siteFields runtimesettings.Fields
	if err = json.Unmarshal(siteRaw, &siteFields); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	for name, value := range siteFields {
		if _, nonPointer := settingKeyByJSONName[name]; nonPointer {
			if _, sent := sentFields[name]; !sent {
				continue
			}
		}
		fields[name] = value
	}
	for name, value := range map[string]any{"account_quota_notify_enabled": settings.AccountQuotaNotifyEnabled, "account_quota_notify_emails": settings.AccountQuotaNotifyEmails, "account_scheduling_thresholds": settings.AccountSchedulingThresholds} {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			response.ErrorFrom(c, marshalErr)
			return
		}
		fields[name] = raw
	}
	for name, value := range map[string]any{
		"payment_visible_method_alipay_source": settings.PaymentVisibleMethodAlipaySource, "payment_visible_method_wxpay_source": settings.PaymentVisibleMethodWxpaySource, "payment_visible_method_alipay_enabled": settings.PaymentVisibleMethodAlipayEnabled, "payment_visible_method_wxpay_enabled": settings.PaymentVisibleMethodWxpayEnabled,
		"ops_monitoring_enabled": settings.OpsMonitoringEnabled, "ops_realtime_monitoring_enabled": settings.OpsRealtimeMonitoringEnabled, "ops_metrics_interval_seconds": settings.OpsMetricsIntervalSeconds} {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			response.ErrorFrom(c, marshalErr)
			return
		}
		fields[name] = raw
	}
	delete(fields, "openai_account_quota_auto_pause")
	if settings.OpenAIQuotaAutoPauseSettingsSet {
		fields["openai_account_quota_auto_pause"], _ = json.Marshal(settings.OpenAIQuotaAutoPauseSettings)
	}
	gatewayRaw, err := json.Marshal(settings.GatewayAdminSettings())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var gatewayFields runtimesettings.Fields
	if err = json.Unmarshal(gatewayRaw, &gatewayFields); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	for name, value := range gatewayFields {
		if _, nonPointer := settingKeyByJSONName[name]; nonPointer {
			if _, sent := sentFields[name]; !sent {
				continue
			}
		}
		fields[name] = value
	}
	prepared, err := h.preparedParticipants(update.Context(), fields, values)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	changes = append(changes, prepared...)
	if err := update.Commit(changes...); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	h.auditSettingsUpdate(c, previousSettings, settings, previousAuthSourceDefaults, authSourceDefaults, auditReq)

	// 重新获取设置返回
	updatedSettings, err := h.settingService.GetAllSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.ensureDingTalkSyncAttributes(c.Request.Context(), updatedSettings)
	updatedAuthSourceDefaults, err := h.settingService.GetAuthSourceDefaultSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	updatedDefaultSubscriptions := make([]billinghttp.DefaultSubscriptionSetting, 0, len(updatedSettings.DefaultSubscriptions))
	for _, sub := range updatedSettings.DefaultSubscriptions {
		updatedDefaultSubscriptions = append(updatedDefaultSubscriptions, billinghttp.DefaultSubscriptionSetting{
			PlanID: sub.PlanID,
		})
	}

	// Reload payment config for response
	var updatedPaymentCfg *payment.PaymentConfig
	if h.paymentConfigService != nil {
		updatedPaymentCfg, err = h.paymentConfigService.GetPaymentConfig(c.Request.Context())
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	if updatedPaymentCfg == nil {
		updatedPaymentCfg = &payment.PaymentConfig{}
	}

	payload := settingsdto.SystemSettings{
		RegistrationEnabled:                              updatedSettings.RegistrationEnabled,
		EmailVerifyEnabled:                               updatedSettings.EmailVerifyEnabled,
		RegistrationEmailSuffixWhitelist:                 updatedSettings.RegistrationEmailSuffixWhitelist,
		RegistrationEmailNormalization:                   updatedSettings.RegistrationEmailNormalization,
		RegistrationEmailDomainQuotaEnabled:              updatedSettings.RegistrationEmailDomainQuotaEnabled,
		UserEmailChangeEnabled:                           updatedSettings.UserEmailChangeEnabled,
		PromoCodeEnabled:                                 updatedSettings.PromoCodeEnabled,
		PasswordResetEnabled:                             updatedSettings.PasswordResetEnabled,
		FrontendURL:                                      updatedSettings.FrontendURL,
		InvitationCodeEnabled:                            updatedSettings.InvitationCodeEnabled,
		TotpEnabled:                                      updatedSettings.TotpEnabled,
		TotpEncryptionKeyConfigured:                      h.settingService.IsTotpEncryptionKeyConfigured(),
		SessionBindingEnabled:                            updatedSettings.SessionBindingEnabled,
		StepUpEnabled:                                    updatedSettings.StepUpEnabled,
		AuditLogRetentionDays:                            updatedSettings.AuditLogRetentionDays,
		LoginAgreementEnabled:                            updatedSettings.LoginAgreementEnabled,
		LoginAgreementMode:                               updatedSettings.LoginAgreementMode,
		LoginAgreementUpdatedAt:                          updatedSettings.LoginAgreementUpdatedAt,
		LoginAgreementDocuments:                          loginAgreementDocumentsToDTO(updatedSettings.LoginAgreementDocuments),
		SMTPHost:                                         updatedSettings.SMTPHost,
		SMTPPort:                                         updatedSettings.SMTPPort,
		SMTPUsername:                                     updatedSettings.SMTPUsername,
		SMTPPasswordConfigured:                           updatedSettings.SMTPPasswordConfigured,
		SMTPFrom:                                         updatedSettings.SMTPFrom,
		SMTPFromName:                                     updatedSettings.SMTPFromName,
		SMTPUseTLS:                                       updatedSettings.SMTPUseTLS,
		TurnstileEnabled:                                 updatedSettings.TurnstileEnabled,
		TurnstileSiteKey:                                 updatedSettings.TurnstileSiteKey,
		TurnstileSecretKeyConfigured:                     updatedSettings.TurnstileSecretKeyConfigured,
		TencentCaptchaEnabled:                            updatedSettings.TencentCaptchaEnabled,
		TencentCaptchaAppID:                              updatedSettings.TencentCaptchaAppID,
		TencentCaptchaAppSecretKeyConfigured:             updatedSettings.TencentCaptchaAppSecretKeyConfigured,
		TencentCaptchaCloudSecretIDConfigured:            updatedSettings.TencentCaptchaCloudSecretIDConfigured,
		TencentCaptchaCloudSecretKeyConfigured:           updatedSettings.TencentCaptchaCloudSecretKeyConfigured,
		TencentCaptchaRegion:                             updatedSettings.TencentCaptchaRegion,
		AliyunCaptchaEnabled:                             updatedSettings.AliyunCaptchaEnabled,
		AliyunCaptchaAccessKeyID:                         updatedSettings.AliyunCaptchaAccessKeyID,
		AliyunCaptchaAccessKeySecretConfigured:           updatedSettings.AliyunCaptchaAccessKeySecretConfigured,
		AliyunCaptchaSceneID:                             updatedSettings.AliyunCaptchaSceneID,
		AliyunCaptchaPrefix:                              updatedSettings.AliyunCaptchaPrefix,
		AliyunCaptchaRegion:                              updatedSettings.AliyunCaptchaRegion,
		APIKeyACLTrustForwardedIP:                        updatedSettings.APIKeyACLTrustForwardedIP,
		ForwardedClientIPHeaders:                         updatedSettings.ForwardedClientIPHeaders,
		LinuxDoConnectEnabled:                            updatedSettings.LinuxDoConnectEnabled,
		LinuxDoConnectClientID:                           updatedSettings.LinuxDoConnectClientID,
		LinuxDoConnectClientSecretConfigured:             updatedSettings.LinuxDoConnectClientSecretConfigured,
		LinuxDoConnectRedirectURL:                        updatedSettings.LinuxDoConnectRedirectURL,
		DingTalkConnectEnabled:                           updatedSettings.DingTalkConnectEnabled,
		DingTalkConnectClientID:                          updatedSettings.DingTalkConnectClientID,
		DingTalkConnectClientSecretConfigured:            updatedSettings.DingTalkConnectClientSecretConfigured,
		DingTalkConnectRedirectURL:                       updatedSettings.DingTalkConnectRedirectURL,
		DingTalkConnectCorpRestrictionPolicy:             updatedSettings.DingTalkConnectCorpRestrictionPolicy,
		DingTalkConnectInternalCorpID:                    updatedSettings.DingTalkConnectInternalCorpID,
		DingTalkConnectBypassRegistration:                updatedSettings.DingTalkConnectBypassRegistration,
		DingTalkConnectSyncCorpEmail:                     updatedSettings.DingTalkConnectSyncCorpEmail,
		DingTalkConnectSyncDisplayName:                   updatedSettings.DingTalkConnectSyncDisplayName,
		DingTalkConnectSyncDept:                          updatedSettings.DingTalkConnectSyncDept,
		DingTalkConnectSyncCorpEmailAttrKey:              updatedSettings.DingTalkConnectSyncCorpEmailAttrKey,
		DingTalkConnectSyncDisplayNameAttrKey:            updatedSettings.DingTalkConnectSyncDisplayNameAttrKey,
		DingTalkConnectSyncDeptAttrKey:                   updatedSettings.DingTalkConnectSyncDeptAttrKey,
		DingTalkConnectSyncCorpEmailAttrName:             updatedSettings.DingTalkConnectSyncCorpEmailAttrName,
		DingTalkConnectSyncDisplayNameAttrName:           updatedSettings.DingTalkConnectSyncDisplayNameAttrName,
		DingTalkConnectSyncDeptAttrName:                  updatedSettings.DingTalkConnectSyncDeptAttrName,
		WeChatConnectEnabled:                             updatedSettings.WeChatConnectEnabled,
		WeChatConnectAppID:                               updatedSettings.WeChatConnectAppID,
		WeChatConnectAppSecretConfigured:                 updatedSettings.WeChatConnectAppSecretConfigured,
		WeChatConnectOpenAppID:                           updatedSettings.WeChatConnectOpenAppID,
		WeChatConnectOpenAppSecretConfigured:             updatedSettings.WeChatConnectOpenAppSecretConfigured,
		WeChatConnectMPAppID:                             updatedSettings.WeChatConnectMPAppID,
		WeChatConnectMPAppSecretConfigured:               updatedSettings.WeChatConnectMPAppSecretConfigured,
		WeChatConnectMobileAppID:                         updatedSettings.WeChatConnectMobileAppID,
		WeChatConnectMobileAppSecretConfigured:           updatedSettings.WeChatConnectMobileAppSecretConfigured,
		WeChatConnectOpenEnabled:                         updatedSettings.WeChatConnectOpenEnabled,
		WeChatConnectMPEnabled:                           updatedSettings.WeChatConnectMPEnabled,
		WeChatConnectMobileEnabled:                       updatedSettings.WeChatConnectMobileEnabled,
		WeChatConnectMode:                                updatedSettings.WeChatConnectMode,
		WeChatConnectScopes:                              updatedSettings.WeChatConnectScopes,
		WeChatConnectRedirectURL:                         updatedSettings.WeChatConnectRedirectURL,
		WeChatConnectFrontendRedirectURL:                 updatedSettings.WeChatConnectFrontendRedirectURL,
		OIDCConnectEnabled:                               updatedSettings.OIDCConnectEnabled,
		OIDCConnectProviderName:                          updatedSettings.OIDCConnectProviderName,
		OIDCConnectClientID:                              updatedSettings.OIDCConnectClientID,
		OIDCConnectClientSecretConfigured:                updatedSettings.OIDCConnectClientSecretConfigured,
		OIDCConnectIssuerURL:                             updatedSettings.OIDCConnectIssuerURL,
		OIDCConnectDiscoveryURL:                          updatedSettings.OIDCConnectDiscoveryURL,
		OIDCConnectAuthorizeURL:                          updatedSettings.OIDCConnectAuthorizeURL,
		OIDCConnectTokenURL:                              updatedSettings.OIDCConnectTokenURL,
		OIDCConnectUserInfoURL:                           updatedSettings.OIDCConnectUserInfoURL,
		OIDCConnectJWKSURL:                               updatedSettings.OIDCConnectJWKSURL,
		OIDCConnectScopes:                                updatedSettings.OIDCConnectScopes,
		OIDCConnectRedirectURL:                           updatedSettings.OIDCConnectRedirectURL,
		OIDCConnectFrontendRedirectURL:                   updatedSettings.OIDCConnectFrontendRedirectURL,
		OIDCConnectTokenAuthMethod:                       updatedSettings.OIDCConnectTokenAuthMethod,
		OIDCConnectUsePKCE:                               updatedSettings.OIDCConnectUsePKCE,
		OIDCConnectValidateIDToken:                       updatedSettings.OIDCConnectValidateIDToken,
		OIDCConnectAllowedSigningAlgs:                    updatedSettings.OIDCConnectAllowedSigningAlgs,
		OIDCConnectClockSkewSeconds:                      updatedSettings.OIDCConnectClockSkewSeconds,
		OIDCConnectRequireEmailVerified:                  updatedSettings.OIDCConnectRequireEmailVerified,
		OIDCConnectUserInfoEmailPath:                     updatedSettings.OIDCConnectUserInfoEmailPath,
		OIDCConnectUserInfoIDPath:                        updatedSettings.OIDCConnectUserInfoIDPath,
		OIDCConnectUserInfoUsernamePath:                  updatedSettings.OIDCConnectUserInfoUsernamePath,
		GitHubOAuthEnabled:                               updatedSettings.GitHubOAuthEnabled,
		GitHubOAuthClientID:                              updatedSettings.GitHubOAuthClientID,
		GitHubOAuthClientSecretConfigured:                updatedSettings.GitHubOAuthClientSecretConfigured,
		GitHubOAuthRedirectURL:                           updatedSettings.GitHubOAuthRedirectURL,
		GitHubOAuthFrontendRedirectURL:                   updatedSettings.GitHubOAuthFrontendRedirectURL,
		GoogleOAuthEnabled:                               updatedSettings.GoogleOAuthEnabled,
		GoogleOneTapEnabled:                              updatedSettings.GoogleOneTapEnabled,
		GoogleOAuthClientID:                              updatedSettings.GoogleOAuthClientID,
		GoogleOAuthClientSecretConfigured:                updatedSettings.GoogleOAuthClientSecretConfigured,
		GoogleOAuthRedirectURL:                           updatedSettings.GoogleOAuthRedirectURL,
		GoogleOAuthFrontendRedirectURL:                   updatedSettings.GoogleOAuthFrontendRedirectURL,
		SiteName:                                         updatedSettings.SiteName,
		SiteLogo:                                         updatedSettings.SiteLogo,
		SiteSubtitle:                                     updatedSettings.SiteSubtitle,
		SiteNameZh:                                       updatedSettings.SiteNameZh,
		SiteNameEn:                                       updatedSettings.SiteNameEn,
		SiteTitleZh:                                      updatedSettings.SiteTitleZh,
		SiteTitleEn:                                      updatedSettings.SiteTitleEn,
		SiteSubtitleZh:                                   updatedSettings.SiteSubtitleZh,
		SiteSubtitleEn:                                   updatedSettings.SiteSubtitleEn,
		APIBaseURL:                                       updatedSettings.APIBaseURL,
		ContactInfo:                                      updatedSettings.ContactInfo,
		DocURL:                                           updatedSettings.DocURL,
		HomeContent:                                      updatedSettings.HomeContent,
		HideCcsImportButton:                              updatedSettings.HideCcsImportButton,
		PurchaseSubscriptionEnabled:                      updatedSettings.PurchaseSubscriptionEnabled,
		PurchaseSubscriptionURL:                          updatedSettings.PurchaseSubscriptionURL,
		TableDefaultPageSize:                             updatedSettings.TableDefaultPageSize,
		TablePageSizeOptions:                             updatedSettings.TablePageSizeOptions,
		UsageRankingLimit:                                updatedSettings.UsageRankingLimit,
		UsageRankingEnabled:                              updatedSettings.UsageRankingEnabled,
		UsageRankingSortBy:                               updatedSettings.UsageRankingSortBy,
		UsageRankingShowTotalTokens:                      updatedSettings.UsageRankingShowTotalTokens,
		UsageRankingShowRequests:                         updatedSettings.UsageRankingShowRequests,
		UsageRankingShowActualCost:                       updatedSettings.UsageRankingShowActualCost,
		CustomMenuItems:                                  sitedto.ParseCustomMenuItems(updatedSettings.CustomMenuItems),
		CustomEndpoints:                                  sitedto.ParseCustomEndpoints(updatedSettings.CustomEndpoints),
		FooterLinks:                                      sitedto.ParseFooterLinks(updatedSettings.FooterLinks),
		FooterText:                                       updatedSettings.FooterText,
		HomeFeaturedModels:                               sitedto.ParseHomeFeaturedModels(updatedSettings.HomeFeaturedModels),
		DefaultConcurrency:                               updatedSettings.DefaultConcurrency,
		DefaultBalance:                                   updatedSettings.DefaultBalance,
		TeamEnabled:                                      updatedSettings.TeamEnabled,
		CreativeEnabled:                                  updatedSettings.CreativeEnabled,
		CreativeModelSettings:                            updatedSettings.CreativeModelSettings,
		CreativeWorkerCount:                              updatedSettings.CreativeWorkerCount,
		RiskControlEnabled:                               updatedSettings.RiskControlEnabled,
		CyberSessionBlockEnabled:                         updatedSettings.CyberSessionBlockEnabled,
		CyberSessionBlockTTLSeconds:                      updatedSettings.CyberSessionBlockTTLSeconds,
		AffiliateEnabled:                                 updatedSettings.AffiliateEnabled,
		AffiliateRebateRate:                              updatedSettings.AffiliateRebateRate,
		AffiliateRebateFreezeHours:                       updatedSettings.AffiliateRebateFreezeHours,
		AffiliateRebateDurationDays:                      updatedSettings.AffiliateRebateDurationDays,
		AffiliateRebatePerInviteeCap:                     updatedSettings.AffiliateRebatePerInviteeCap,
		AdminRechargeRebateEnabled:                       updatedSettings.AdminRechargeRebateEnabled,
		DefaultUserRPMLimit:                              updatedSettings.DefaultUserRPMLimit,
		DefaultUserAPIKeyLimit:                           updatedSettings.DefaultUserAPIKeyLimit,
		DefaultSubscriptions:                             updatedDefaultSubscriptions,
		BalanceUnitName:                                  updatedSettings.BalanceUnitName,
		BalanceUnitSymbol:                                updatedSettings.BalanceUnitSymbol,
		BalanceIconSVG:                                   updatedSettings.BalanceIconSVG,
		ReasoningPointRMBUnitPrice:                       updatedSettings.ReasoningPointRMBUnitPrice,
		USDExchangeRate:                                  updatedSettings.USDExchangeRate,
		MarketplaceAvailabilityWindowDays:                updatedSettings.MarketplaceAvailabilityWindowDays,
		MarketplaceAvailabilityBucketMinutes:             updatedSettings.MarketplaceAvailabilityBucketMinutes,
		EnableModelFallback:                              updatedSettings.EnableModelFallback,
		FallbackModelAnthropic:                           updatedSettings.FallbackModelAnthropic,
		FallbackModelOpenAI:                              updatedSettings.FallbackModelOpenAI,
		FallbackModelGemini:                              updatedSettings.FallbackModelGemini,
		FallbackModelAntigravity:                         updatedSettings.FallbackModelAntigravity,
		GrokDefaultTextModel:                             updatedSettings.GrokDefaultTextModel,
		GrokCrossClientModelMapEnabled:                   updatedSettings.GrokCrossClientModelMapEnabled,
		GrokDefaultBaseURLMode:                           updatedSettings.GrokDefaultBaseURLMode,
		EnableIdentityPatch:                              updatedSettings.EnableIdentityPatch,
		IdentityPatchPrompt:                              updatedSettings.IdentityPatchPrompt,
		OpsMonitoringEnabled:                             updatedSettings.OpsMonitoringEnabled,
		OpsRealtimeMonitoringEnabled:                     updatedSettings.OpsRealtimeMonitoringEnabled,
		OpsMetricsIntervalSeconds:                        updatedSettings.OpsMetricsIntervalSeconds,
		MinClaudeCodeVersion:                             updatedSettings.MinClaudeCodeVersion,
		MaxClaudeCodeVersion:                             updatedSettings.MaxClaudeCodeVersion,
		AllowUngroupedKeyScheduling:                      updatedSettings.AllowUngroupedKeyScheduling,
		BackendModeEnabled:                               updatedSettings.BackendModeEnabled,
		OpenAITTFTMode:                                   updatedSettings.OpenAITTFTMode,
		EnableFingerprintUnification:                     updatedSettings.EnableFingerprintUnification,
		EnableMetadataPassthrough:                        updatedSettings.EnableMetadataPassthrough,
		EnableCCHSigning:                                 updatedSettings.EnableCCHSigning,
		EnableClaudeOAuthSystemPromptInjection:           updatedSettings.EnableClaudeOAuthSystemPromptInjection,
		ClaudeOAuthSystemPrompt:                          updatedSettings.ClaudeOAuthSystemPrompt,
		ClaudeOAuthSystemPromptBlocks:                    updatedSettings.ClaudeOAuthSystemPromptBlocks,
		EnableAnthropicCacheTTL1hInjection:               updatedSettings.EnableAnthropicCacheTTL1hInjection,
		RewriteMessageCacheControl:                       updatedSettings.RewriteMessageCacheControl,
		EnableClientDatelineNormalization:                updatedSettings.EnableClientDatelineNormalization,
		AntigravityUserAgentVersion:                      updatedSettings.AntigravityUserAgentVersion,
		OpenAICodexUserAgent:                             updatedSettings.OpenAICodexUserAgent,
		OpenAIAllowClaudeCodeCodexPlugin:                 updatedSettings.OpenAIAllowClaudeCodeCodexPlugin,
		UserPromptReplacementConfig:                      updatedSettings.UserPromptReplacementConfig,
		WebSearchEmulationEnabled:                        updatedSettings.WebSearchEmulationEnabled,
		PaymentVisibleMethodAlipaySource:                 updatedSettings.PaymentVisibleMethodAlipaySource,
		PaymentVisibleMethodWxpaySource:                  updatedSettings.PaymentVisibleMethodWxpaySource,
		PaymentVisibleMethodAlipayEnabled:                updatedSettings.PaymentVisibleMethodAlipayEnabled,
		PaymentVisibleMethodWxpayEnabled:                 updatedSettings.PaymentVisibleMethodWxpayEnabled,
		AdvancedSchedulerStickyWeightedEnabled:           updatedSettings.AdvancedSchedulerStickyWeightedEnabled,
		AdvancedSchedulerSubscriptionPriorityEnabled:     updatedSettings.AdvancedSchedulerSubscriptionPriorityEnabled,
		AdvancedSchedulerEWMAErrorRateAlpha:              updatedSettings.AdvancedSchedulerEWMAErrorRateAlpha,
		AdvancedSchedulerEWMATTFTAlpha:                   updatedSettings.AdvancedSchedulerEWMATTFTAlpha,
		AdvancedSchedulerStickyEscapeEnabled:             updatedSettings.AdvancedSchedulerStickyEscapeEnabled,
		AdvancedSchedulerStickyEscapeTTFTMs:              updatedSettings.AdvancedSchedulerStickyEscapeTTFTMs,
		AdvancedSchedulerStickyEscapeErrorRate:           updatedSettings.AdvancedSchedulerStickyEscapeErrorRate,
		AdvancedSchedulerLBTopK:                          updatedSettings.AdvancedSchedulerLBTopK,
		AdvancedSchedulerWeightPriority:                  updatedSettings.AdvancedSchedulerWeightPriority,
		AdvancedSchedulerWeightLoad:                      updatedSettings.AdvancedSchedulerWeightLoad,
		AdvancedSchedulerWeightQueue:                     updatedSettings.AdvancedSchedulerWeightQueue,
		AdvancedSchedulerWeightErrorRate:                 updatedSettings.AdvancedSchedulerWeightErrorRate,
		AdvancedSchedulerWeightTTFT:                      updatedSettings.AdvancedSchedulerWeightTTFT,
		AdvancedSchedulerWeightReset:                     updatedSettings.AdvancedSchedulerWeightReset,
		AdvancedSchedulerWeightQuotaHeadroom:             updatedSettings.AdvancedSchedulerWeightQuotaHeadroom,
		AdvancedSchedulerWeightPreviousResponse:          updatedSettings.AdvancedSchedulerWeightPreviousResponse,
		AdvancedSchedulerWeightSessionSticky:             updatedSettings.AdvancedSchedulerWeightSessionSticky,
		AdvancedSchedulerEffectiveLBTopK:                 updatedSettings.AdvancedSchedulerEffectiveLBTopK,
		AdvancedSchedulerEffectiveWeightPriority:         updatedSettings.AdvancedSchedulerEffectiveWeightPriority,
		AdvancedSchedulerEffectiveWeightLoad:             updatedSettings.AdvancedSchedulerEffectiveWeightLoad,
		AdvancedSchedulerEffectiveWeightQueue:            updatedSettings.AdvancedSchedulerEffectiveWeightQueue,
		AdvancedSchedulerEffectiveWeightErrorRate:        updatedSettings.AdvancedSchedulerEffectiveWeightErrorRate,
		AdvancedSchedulerEffectiveWeightTTFT:             updatedSettings.AdvancedSchedulerEffectiveWeightTTFT,
		AdvancedSchedulerEffectiveWeightReset:            updatedSettings.AdvancedSchedulerEffectiveWeightReset,
		AdvancedSchedulerEffectiveWeightQuotaHeadroom:    updatedSettings.AdvancedSchedulerEffectiveWeightQuotaHeadroom,
		AdvancedSchedulerEffectiveWeightPreviousResponse: updatedSettings.AdvancedSchedulerEffectiveWeightPreviousResponse,
		AdvancedSchedulerEffectiveWeightSessionSticky:    updatedSettings.AdvancedSchedulerEffectiveWeightSessionSticky,
		AdvancedSchedulerEffectiveEWMAErrorRateAlpha:     updatedSettings.AdvancedSchedulerEffectiveEWMAErrorRateAlpha,
		AdvancedSchedulerEffectiveEWMATTFTAlpha:          updatedSettings.AdvancedSchedulerEffectiveEWMATTFTAlpha,
		AdvancedSchedulerEffectiveStickyEscapeEnabled:    updatedSettings.AdvancedSchedulerEffectiveStickyEscapeEnabled,
		AdvancedSchedulerEffectiveStickyEscapeTTFTMs:     updatedSettings.AdvancedSchedulerEffectiveStickyEscapeTTFTMs,
		AdvancedSchedulerEffectiveStickyEscapeErrorRate:  updatedSettings.AdvancedSchedulerEffectiveStickyEscapeErrorRate,
		OpenAIQuotaAutoPauseSettings:                     updatedSettings.OpenAIQuotaAutoPauseSettings,
		BalanceLowNotifyEnabled:                          updatedSettings.BalanceLowNotifyEnabled,
		BalanceLowNotifyThreshold:                        updatedSettings.BalanceLowNotifyThreshold,
		BalanceLowNotifyRechargeURL:                      updatedSettings.BalanceLowNotifyRechargeURL,
		SubscriptionExpiryNotifyEnabled:                  updatedSettings.SubscriptionExpiryNotifyEnabled,
		AccountQuotaNotifyEnabled:                        updatedSettings.AccountQuotaNotifyEnabled,
		AccountQuotaNotifyEmails:                         identitydto.NotifyEmailEntriesFromIdentity(updatedSettings.AccountQuotaNotifyEmails),
		PaymentEnabled:                                   updatedPaymentCfg.Enabled,
		PaymentMinAmount:                                 updatedPaymentCfg.MinAmount,
		PaymentMaxAmount:                                 updatedPaymentCfg.MaxAmount,
		PaymentDailyLimit:                                updatedPaymentCfg.DailyLimit,
		PaymentOrderTimeoutMin:                           updatedPaymentCfg.OrderTimeoutMin,
		PaymentMaxPendingOrders:                          updatedPaymentCfg.MaxPendingOrders,
		PaymentEnabledTypes:                              updatedPaymentCfg.EnabledTypes,
		PaymentBalanceDisabled:                           updatedPaymentCfg.BalanceDisabled,
		PaymentBalanceRechargeMultiplier:                 updatedPaymentCfg.BalanceRechargeMultiplier,
		PaymentSubscriptionUSDToCNYRate:                  updatedPaymentCfg.SubscriptionUSDToCNYRate,
		PaymentRechargeFeeRate:                           updatedPaymentCfg.RechargeFeeRate,
		PaymentMethodFees:                                updatedPaymentCfg.MethodFees,
		PaymentLoadBalanceStrat:                          updatedPaymentCfg.LoadBalanceStrategy,
		PaymentProductNamePrefix:                         updatedPaymentCfg.ProductNamePrefix,
		PaymentProductNameSuffix:                         updatedPaymentCfg.ProductNameSuffix,
		PaymentHelpImageURL:                              updatedPaymentCfg.HelpImageURL,
		PaymentHelpText:                                  updatedPaymentCfg.HelpText,
		PaymentCancelRateLimitEnabled:                    updatedPaymentCfg.CancelRateLimitEnabled,
		PaymentCancelRateLimitMax:                        updatedPaymentCfg.CancelRateLimitMax,
		PaymentCancelRateLimitWindow:                     updatedPaymentCfg.CancelRateLimitWindow,
		PaymentCancelRateLimitUnit:                       updatedPaymentCfg.CancelRateLimitUnit,
		PaymentCancelRateLimitMode:                       updatedPaymentCfg.CancelRateLimitMode,
		PaymentAlipayForceQRCode:                         updatedPaymentCfg.AlipayForceQRCode,
		PaymentAlipayMobilePrecreateDeepLink:             updatedPaymentCfg.AlipayMobilePrecreateDeepLink,
		AllowUserViewErrorRequests:                       updatedSettings.AllowUserViewErrorRequests,
	}
	if fastPolicy, err := h.settingService.GetOpenAIFastPolicySettings(c.Request.Context()); err != nil {
		slog.Error("openai_fast_policy_settings_get_failed", "error", err)
	} else if fastPolicy != nil {
		payload.OpenAIFastPolicySettings = openaiFastPolicySettingsToDTO(fastPolicy)
	}

	// 默认平台限额（JSON map）：与 GetSettings 一致，避免保存后响应缺失该字段。
	if platformQuotas, err := h.settingService.GetDefaultPlatformQuotas(c.Request.Context()); err != nil {
		slog.Error("default_platform_quotas_get_failed", "error", err)
	} else {
		payload.DefaultPlatformQuotas = platformQuotas
	}
	response.Success(c, systemSettingsResponseData(payload, updatedAuthSourceDefaults))
}

// rejectDeprecatedAdvancedSchedulerRequestFields 阻止旧版 OpenAI 实验调度字段被静默忽略。
// 高级调度器已改为分组级开关，设置接口只接受 advanced_scheduler_* 的通用参数。
func rejectDeprecatedAdvancedSchedulerRequestFields(c *gin.Context, sentFields map[string]json.RawMessage) bool {
	for field := range sentFields {
		if field == "advanced_scheduler_enabled" || strings.HasPrefix(field, "openai_advanced_scheduler_") {
			response.ErrorWithDetails(c, http.StatusBadRequest,
				"Deprecated advanced scheduler setting: "+field+"; select the scheduler type on each group and use advanced_scheduler_* settings",
				"DEPRECATED_ADVANCED_SCHEDULER_SETTING", map[string]string{"field": field})
			return true
		}
	}
	return false
}

// hasPaymentFields returns true if any payment-related field was explicitly provided.
// mapDingTalkValidateError maps ValidateDingTalkConfig errors to machine-readable reason codes.
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

// ensureDingTalkSyncAttributes 在保存 settings 后，按 admin 配置的 (attr key, attr name)
// 兜底 upsert 对应 user attribute definition：不存在则创建；存在但 name 不同则更新 name
// （type/options/required 不变）。仅 internal_only + 对应 sync 开关开启时执行。
// 失败仅记录日志，不阻塞 settings 保存。
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

func (h *Handler) ensureUserAttributeDefinition(ctx context.Context, key, name, description string, attrType identity.UserAttributeType) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
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
