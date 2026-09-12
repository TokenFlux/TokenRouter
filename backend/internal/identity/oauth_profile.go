// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	context "context"
	strings "strings"
)

// EmailOAuthProfile 是已经验证的邮箱提供方资料，不包含 HTTP 客户端或配置。
type EmailOAuthProfile struct {
	Subject, Email                   string
	EmailVerified                    bool
	Username, DisplayName, AvatarURL string
	Metadata                         map[string]any
}
type GoogleIDTokenClaims struct {
	Subject, Email                                 string
	EmailVerified                                  bool
	Name, GivenName, Picture, Locale, HostedDomain string
}
type GoogleIDTokenVerifier interface {
	Verify(context.Context, string, string) (*GoogleIDTokenClaims, error)
}

// OAuthPendingDraft 保存准备写入现有 pending session 的身份意图和前端状态。
type OAuthPendingDraft struct {
	Intent                                       string
	Identity                                     PendingAuthIdentityKey
	TargetUserID                                 *int64
	ResolvedEmail, RedirectTo, BrowserSessionKey string
	UpstreamIdentityClaims, CompletionResponse   map[string]any
	PromoCode                                    string
}

func EmailOAuthIdentityFromProfile(provider string, profile *EmailOAuthProfile) EmailOAuthIdentityInput {
	if profile == nil {
		return EmailOAuthIdentityInput{}
	}
	return EmailOAuthIdentityInput{
		ProviderType:     provider,
		ProviderKey:      provider,
		ProviderSubject:  profile.Subject,
		Email:            profile.Email,
		EmailVerified:    profile.EmailVerified,
		Username:         profile.Username,
		DisplayName:      profile.DisplayName,
		AvatarURL:        profile.AvatarURL,
		UpstreamMetadata: profile.Metadata,
	}
}

// PrepareEmailRegistrationDraft 保留邮箱提供方必须补充密码/邀请码的选择状态与元数据优先级。
func PrepareEmailRegistrationDraft(provider, frontendCallback, redirectTo, browserSessionKey string, profile *EmailOAuthProfile, affCode, promoCode string, invitationRequired bool) OAuthPendingDraft {
	email := strings.TrimSpace(strings.ToLower(profile.Email))
	username := strings.TrimSpace(profile.Username)
	affCode = strings.TrimSpace(affCode)
	upstreamClaims := map[string]any{
		"email":            email,
		"email_verified":   profile.EmailVerified,
		"username":         username,
		"provider":         provider,
		"provider_key":     provider,
		"provider_subject": strings.TrimSpace(profile.Subject),
	}
	if strings.TrimSpace(profile.DisplayName) != "" {
		upstreamClaims["suggested_display_name"] = strings.TrimSpace(profile.DisplayName)
	}
	if strings.TrimSpace(profile.AvatarURL) != "" {
		upstreamClaims["suggested_avatar_url"] = strings.TrimSpace(profile.AvatarURL)
	}
	if affCode != "" {
		upstreamClaims["aff_code"] = affCode
	}
	for key, value := range profile.Metadata {
		if _, exists := upstreamClaims[key]; !exists {
			upstreamClaims[key] = value
		}
	}

	pendingError := "registration_completion_required"
	choiceReason := "registration_completion_required"
	if invitationRequired {
		pendingError = "invitation_required"
		choiceReason = "invitation_required"
	}
	completionResponse := map[string]any{
		"step":                      OAuthPendingChoiceStep,
		"error":                     pendingError,
		"choice_reason":             choiceReason,
		"adoption_required":         false,
		"create_account_allowed":    true,
		"existing_account_bindable": false,
		"force_email_on_signup":     true,
		"invitation_required":       invitationRequired,
		"email":                     email,
		"resolved_email":            email,
		"provider":                  provider,
		"redirect":                  redirectTo,
	}
	if strings.TrimSpace(frontendCallback) != "" {
		completionResponse["frontend_callback"] = strings.TrimSpace(frontendCallback)
	}

	return OAuthPendingDraft{
		Intent:                 OAuthIntentLogin,
		Identity:               PendingAuthIdentityKey{ProviderType: provider, ProviderKey: provider, ProviderSubject: strings.TrimSpace(profile.Subject)},
		ResolvedEmail:          email,
		RedirectTo:             redirectTo,
		BrowserSessionKey:      browserSessionKey,
		UpstreamIdentityClaims: upstreamClaims,
		CompletionResponse:     completionResponse,
		PromoCode:              strings.TrimSpace(promoCode),
	}
}

// GoogleEmailProfile 只投影严格验证后的声明，避免 HTTP 回调复制接纳规则。
func GoogleEmailProfile(c *GoogleIDTokenClaims) *EmailOAuthProfile {
	metadata := map[string]any{"email_verified": true}
	if c.Locale != "" {
		metadata["locale"] = c.Locale
	}
	if c.HostedDomain != "" {
		metadata["hosted_domain"] = c.HostedDomain
	}
	return &EmailOAuthProfile{Subject: c.Subject, Email: c.Email, EmailVerified: true, Username: OAuthFirstNonEmpty(c.GivenName, c.Name, c.Email), DisplayName: c.Name, AvatarURL: c.Picture, Metadata: metadata}
}
