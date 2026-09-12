// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	context "context"
	errors "errors"
	fmt "fmt"
	strings "strings"
)

type DingTalkOAuthOptions struct {
	Enabled             bool   `mapstructure:"enabled"`
	ClientID            string `mapstructure:"client_id"`
	ClientSecret        string `mapstructure:"client_secret"`
	AuthorizeURL        string `mapstructure:"authorize_url"`
	TokenURL            string `mapstructure:"token_url"`
	UserInfoURL         string `mapstructure:"userinfo_url"`
	Scopes              string `mapstructure:"scopes"`
	RedirectURL         string `mapstructure:"redirect_url"`
	FrontendRedirectURL string `mapstructure:"frontend_redirect_url"`

	// 平台底座 + 业务行为
	DingTalkAppKind string `mapstructure:"dingtalk_app_kind"` // 仅 "internal_app"（V4 fail-closed）
	AppType         string `mapstructure:"app_type"`          // "public" (default) | "internal"

	// Corp 限定（none | internal_only）
	CorpRestrictionPolicy   string `mapstructure:"corp_restriction_policy"`
	InternalCorpID          string `mapstructure:"internal_corp_id"`
	BypassRegistration      bool   `mapstructure:"bypass_registration"`
	SyncCorpEmail           bool   `mapstructure:"sync_corp_email"`
	SyncDisplayName         bool   `mapstructure:"sync_display_name"`
	SyncDept                bool   `mapstructure:"sync_dept"`
	SyncCorpEmailAttrKey    string `mapstructure:"sync_corp_email_attr_key"`
	SyncDisplayNameAttrKey  string `mapstructure:"sync_display_name_attr_key"`
	SyncDeptAttrKey         string `mapstructure:"sync_dept_attr_key"`
	SyncCorpEmailAttrName   string `mapstructure:"sync_corp_email_attr_name"`
	SyncDisplayNameAttrName string `mapstructure:"sync_display_name_attr_name"`
	SyncDeptAttrName        string `mapstructure:"sync_dept_attr_name"`

	// 邮箱 + Username
	RequireEmail            bool   `mapstructure:"require_email"`
	UsernameOverwritePolicy string `mapstructure:"username_overwrite_policy"`

	// Attribute（私有版扩展点；开源版仅声明）
	UsernameAttributeKey         string   `mapstructure:"username_attribute_key"`
	EnableAttributeMatching      bool     `mapstructure:"enable_attribute_matching"`
	EnableAttributeSync          bool     `mapstructure:"enable_attribute_sync"`
	AttributeSyncFields          []string `mapstructure:"attribute_sync_fields"`
	AttributeSyncOverwritePolicy string   `mapstructure:"attribute_sync_overwrite_policy"`
}
type DingTalkUserTokenResp struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpireIn     int64  `json:"expireIn"`
	CorpID       string `json:"corpId"`
}
type DingTalkAPIError struct {
	Code    string
	Message string
	HTTP    int
}

func (e *DingTalkAPIError) Error() string {
	return fmt.Sprintf("dingtalk api error code=%s msg=%s http=%d", e.Code, e.Message, e.HTTP)
}

// DingTalkOAuthClient 暴露原四步验证链与部门读取，令牌缓存仍由 provider 唯一持有。
type DingTalkOAuthClient interface {
	ExchangeCodeForUserToken(context.Context, string) (*DingTalkUserTokenResp, error)
	GetUnionIdByUserToken(context.Context, string) (string, string, error)
	GetUserIdByUnionId(context.Context, string) (string, error)
	GetStaffInfoByUserId(context.Context, string) (*DingTalkProfileSnapshot, error)
	DingTalkDepartmentReader
}

func DingTalkSyntheticEmail(userID string) string {
	return "dingtalk-" + strings.ToLower(strings.TrimSpace(userID)) + DingTalkConnectSyntheticEmailDomain
}
func DingTalkBindLoginCompletionResponse(redirectTo string) map[string]any {
	return map[string]any{
		"step":                      "bind_login_required",
		"existing_account_bindable": true,
		"create_account_allowed":    false,
		"redirect":                  redirectTo,
	}
}
func DingTalkUpstreamClaims(staff *DingTalkProfileSnapshot, unionID, corpID string) map[string]any {
	primaryDeptID := int64(0)
	if len(staff.DeptIDs) > 0 {
		primaryDeptID = staff.DeptIDs[0]
	}
	return map[string]any{
		"email":           staff.Email,
		"username":        staff.Name,
		"nickname":        staff.Nickname,
		"subject":         unionID,      // 与 identityKey.ProviderSubject 保持一致（全局唯一 unionID）
		"corp_user_id":    staff.UserID, // 企业 userid（跨组织时为空），保留作独立字段用于 audit
		"union_id":        unionID,
		"corp_id":         corpID,
		"primary_dept_id": primaryDeptID, // 首个部门 ID，用于 internal_only 同步路径
	}
}
func DingTalkCorpAllowed(cfg DingTalkOAuthOptions, corpID string) bool {
	switch cfg.CorpRestrictionPolicy {
	case "internal_only":
		// 方案 A：完全跳过 corpID 字段校验，由 step 3 `GetUserIdByUnionId` 做真实判定。
		// 原因：钉钉 /v1.0/oauth2/userAccessToken 在部分授权场景（扫码登录、非企业工作台入口）
		// 不会返回 corpId 字段。而 step 3 用本企业 appToken 查 unionId→userId 映射，
		// 跨企业用户会被钉钉拒绝（错误码 60011/60121），mapDingTalkErrorCode 已将其映射回 "corp_rejected"。
		// AppType=internal 已由 ValidateDingTalkConfig 强制保证应用属性。
		return true
	case "none", "":
		return true
	default:
		return false
	}
}

// DingTalkStep34Strategy 根据 policy 和 Step 3/4 运行时错误决定处理方式。
// 返回 (proceed bool, fatal bool)：
//   - proceed=true：继续处理（step 成功或降级）
//   - fatal=true：应 hard fail（upstream_error）
//
// 此 helper 从主链中提取，便于 unit test 独立验证策略决策逻辑。
func DingTalkStep34Strategy(policy string, stepErr error) (shouldFallback bool, isFatal bool) {
	if stepErr == nil {
		return false, false // 成功，不需要降级
	}
	switch policy {
	case "internal_only":
		return false, true // 硬失败：同企业第 3/4 步必须成功
	case "none", "":
		return true, false // 降级：公网场景跨组织用户失败属正常预期
	default:
		return false, true // 未知 policy，视为 hard fail
	}
}

// DingTalkErrorCode 把 DingTalkAPIError 映射到 redirectOAuthError 用的字符串 code
func DingTalkErrorCode(err error) string {
	var apiErr *DingTalkAPIError
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
	// 注册被拦：无论是否匹配到 compat email user，都跳过 choice，直接进 bind_login。
	// "开放注册" 关闭 且 "钉钉企业模式豁免" 也关闭时，唯一合法出口是绑定已有账户，
	// 不应该让用户看到"创建新账户"按钮；compat user 命中只是让 bind_login 的邮箱字段预填得更准。
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

// DingTalkSignupBlocked 只判断已经读取的注册开关，HTTP 保留动态读取时机。
func DingTalkSignupBlocked(cfg DingTalkOAuthOptions, enabled bool) bool {
	return !enabled && (!cfg.BypassRegistration || cfg.CorpRestrictionPolicy != "internal_only")
}
