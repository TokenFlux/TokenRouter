// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	context "context"
	errors "errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	strings "strings"
)

// WeChatOAuthOptions 是一次授权所需的配置投影，模式与地址解析仍由 HTTP 负责。
type WeChatOAuthOptions struct {
	Mode, AppID, AppSecret, AuthorizeURL, Scope, RedirectURI, FrontendCallback, APIBaseURL string
	OpenEnabled, MPEnabled                                                                 bool
}

func (c WeChatOAuthOptions) RequiresUnionID() bool { return c.OpenEnabled && c.MPEnabled }

type WeChatOAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	OpenID       string `json:"openid"`
	Scope        string `json:"scope"`
	UnionID      string `json:"unionid"`
	ErrCode      int64  `json:"errcode"`
	ErrMsg       string `json:"errmsg"`
}

type WeChatOAuthUserInfoResponse struct {
	OpenID     string `json:"openid"`
	Nickname   string `json:"nickname"`
	HeadImgURL string `json:"headimgurl"`
	UnionID    string `json:"unionid"`
	ErrCode    int64  `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

type WeChatOAuthClient interface {
	FetchIdentity(context.Context, WeChatOAuthOptions, string) (*WeChatOAuthTokenResponse, *WeChatOAuthUserInfoResponse, error)
}

func WeChatSyntheticEmail(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return ""
	}
	return "wechat-" + subject + WeChatConnectSyntheticEmailDomain
}
func WeChatFallbackUsername(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "wechat_user"
	}
	// 保留旧授权流程的 512 字节 UTF-8 安全截断边界。
	return "wechat_" + logredact.TruncateUTF8Value(subject, 512)
}
func PrepareWeChatPending(
	intent string,
	providerSubject string,
	email string,
	redirectTo string,
	browserSessionKey string,
	upstreamClaims map[string]any,
	tokenPair *TokenPair,
	authErr error,
	targetUserID *int64,
) (OAuthPendingDraft, error) {
	completionResponse := map[string]any{
		"redirect": redirectTo,
	}
	if authErr != nil {
		if errors.Is(authErr, ErrOAuthInvitationRequired) {
			completionResponse["error"] = "invitation_required"
		} else {
			return OAuthPendingDraft{}, authErr
		}
	} else if tokenPair != nil {
		completionResponse["access_token"] = tokenPair.AccessToken
		completionResponse["refresh_token"] = tokenPair.RefreshToken
		completionResponse["expires_in"] = tokenPair.ExpiresIn
		completionResponse["token_type"] = "Bearer"
	}

	return OAuthPendingDraft{
		Intent: intent,
		Identity: PendingAuthIdentityKey{
			ProviderType:    "wechat",
			ProviderKey:     WeChatOAuthProviderKey,
			ProviderSubject: providerSubject,
		},
		TargetUserID:           targetUserID,
		ResolvedEmail:          email,
		RedirectTo:             redirectTo,
		BrowserSessionKey:      browserSessionKey,
		UpstreamIdentityClaims: upstreamClaims,
		CompletionResponse:     completionResponse,
	}, nil
}
func PrepareWeChatChoice(
	identity PendingAuthIdentityKey,
	suggestedEmail string,
	resolvedEmail string,
	redirectTo string,
	browserSessionKey string,
	upstreamClaims map[string]any,
	compatEmail string,
	compatEmailUser *User,
	forceEmailOnSignup bool,
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
		"create_account_allowed":    true,
		"force_email_on_signup":     forceEmailOnSignup,
		"choice_reason":             "third_party_signup",
	}
	if strings.TrimSpace(compatEmail) != "" {
		completionResponse["compat_email"] = strings.TrimSpace(compatEmail)
	}
	if compatEmailUser != nil {
		completionResponse["email"] = strings.TrimSpace(compatEmailUser.Email)
		completionResponse["existing_account_email"] = strings.TrimSpace(compatEmailUser.Email)
		completionResponse["existing_account_bindable"] = true
		completionResponse["choice_reason"] = "compat_email_match"
	}
	if forceEmailOnSignup {
		completionResponse["choice_reason"] = "force_email_on_signup"
	}

	resolvedChoiceEmail := suggestionEmail
	if compatEmailUser != nil {
		resolvedChoiceEmail = strings.TrimSpace(compatEmailUser.Email)
	}

	return OAuthPendingDraft{
		Intent:                 OAuthIntentLogin,
		Identity:               identity,
		ResolvedEmail:          resolvedChoiceEmail,
		RedirectTo:             redirectTo,
		BrowserSessionKey:      browserSessionKey,
		UpstreamIdentityClaims: upstreamClaims,
		CompletionResponse:     completionResponse,
	}
}
