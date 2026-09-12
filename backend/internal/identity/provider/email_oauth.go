// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	context "context"
	json "encoding/json"
	errors "errors"
	fmt "fmt"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	logredact "github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	req "github.com/imroc/req/v3"
	gjson "github.com/tidwall/gjson"
	strings "strings"
)

// EmailOAuthProviderConfig 保存 GitHub/Google 这类邮箱 OAuth 登录的配置。
type EmailOAuthOptions = identity.EmailOAuthOptions
type EmailOAuthTokenResponse = identity.EmailOAuthTokenResponse
type EmailOAuthProfile = identity.EmailOAuthProfile

func ExchangeEmailOAuthCode(ctx context.Context, cfg EmailOAuthOptions, code string) (*EmailOAuthTokenResponse, error) {
	resp, err := req.C().
		R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetFormData(map[string]string{
			"grant_type":    "authorization_code",
			"client_id":     cfg.ClientID,
			"client_secret": cfg.ClientSecret,
			"code":          code,
			"redirect_uri":  cfg.RedirectURL,
		}).
		Post(cfg.TokenURL)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token endpoint status %d: %s", resp.StatusCode, logredact.TruncateUTF8Value(resp.String(), 1024))
	}
	var tokenResp EmailOAuthTokenResponse
	if err := json.Unmarshal(resp.Bytes(), &tokenResp); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, errors.New("missing access_token")
	}
	return &tokenResp, nil
}

func FetchEmailOAuthProfile(ctx context.Context, provider string, cfg EmailOAuthOptions, token *EmailOAuthTokenResponse) (*EmailOAuthProfile, error) {
	resp, err := req.C().
		R().
		SetContext(ctx).
		SetBearerAuthToken(token.AccessToken).
		SetHeader("Accept", "application/json").
		Get(cfg.UserInfoURL)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("userinfo endpoint status %d: %s", resp.StatusCode, logredact.TruncateUTF8Value(resp.String(), 1024))
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github":
		return ParseGitHubOAuthProfile(ctx, cfg, token, resp.String())
	case "google":
		return ParseGoogleOAuthProfile(resp.String())
	default:
		return nil, errors.New("unsupported oauth provider")
	}
}

func ParseGitHubOAuthProfile(ctx context.Context, cfg EmailOAuthOptions, token *EmailOAuthTokenResponse, body string) (*EmailOAuthProfile, error) {
	subject := strings.TrimSpace(gjson.Get(body, "id").String())
	if subject == "" {
		return nil, errors.New("github user id is missing")
	}
	emailsURL := strings.TrimSpace(cfg.EmailsURL)
	if emailsURL == "" {
		return nil, errors.New("github verified email is missing")
	}
	email, err := FetchGitHubPrimaryVerifiedEmail(ctx, emailsURL, token.AccessToken)
	if err != nil {
		return nil, err
	}
	if email == "" {
		return nil, errors.New("github verified email is missing")
	}
	login := strings.TrimSpace(gjson.Get(body, "login").String())
	name := strings.TrimSpace(gjson.Get(body, "name").String())
	return &EmailOAuthProfile{
		Subject:       subject,
		Email:         email,
		EmailVerified: true,
		Username:      identity.OAuthFirstNonEmpty(login, name, "github_"+subject),
		DisplayName:   identity.OAuthFirstNonEmpty(name, login),
		AvatarURL:     strings.TrimSpace(gjson.Get(body, "avatar_url").String()),
		Metadata: map[string]any{
			"login": login,
		},
	}, nil
}

func FetchGitHubPrimaryVerifiedEmail(ctx context.Context, emailsURL string, accessToken string) (string, error) {
	resp, err := req.C().
		R().
		SetContext(ctx).
		SetBearerAuthToken(accessToken).
		SetHeader("Accept", "application/json").
		Get(emailsURL)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github emails endpoint status %d: %s", resp.StatusCode, logredact.TruncateUTF8Value(resp.String(), 1024))
	}
	items := gjson.Parse(resp.String()).Array()
	for _, item := range items {
		if item.Get("primary").Bool() && item.Get("verified").Bool() {
			if email := strings.TrimSpace(item.Get("email").String()); email != "" {
				return email, nil
			}
		}
	}
	for _, item := range items {
		if item.Get("verified").Bool() {
			if email := strings.TrimSpace(item.Get("email").String()); email != "" {
				return email, nil
			}
		}
	}
	return "", errors.New("github verified email is missing")
}

func ParseGoogleOAuthProfile(body string) (*EmailOAuthProfile, error) {
	subject := strings.TrimSpace(gjson.Get(body, "sub").String())
	email := strings.TrimSpace(gjson.Get(body, "email").String())
	verified := gjson.Get(body, "email_verified").Bool()
	if subject == "" {
		return nil, errors.New("google subject is missing")
	}
	if email == "" || !verified {
		return nil, errors.New("google verified email is missing")
	}
	name := strings.TrimSpace(gjson.Get(body, "name").String())
	return &EmailOAuthProfile{
		Subject:       subject,
		Email:         email,
		EmailVerified: true,
		Username:      identity.OAuthFirstNonEmpty(strings.TrimSpace(gjson.Get(body, "given_name").String()), name, email),
		DisplayName:   name,
		AvatarURL:     strings.TrimSpace(gjson.Get(body, "picture").String()),
		Metadata: map[string]any{
			"email_verified": true,
		},
	}, nil
}

// EmailOAuthClientAdapter 复用现有客户端，身份 HTTP 不依赖具体网络实现。
type EmailOAuthClientAdapter struct{}

func (EmailOAuthClientAdapter) ExchangeCode(ctx context.Context, c EmailOAuthOptions, code string) (*EmailOAuthTokenResponse, error) {
	return ExchangeEmailOAuthCode(ctx, c, code)
}
func (EmailOAuthClientAdapter) FetchProfile(ctx context.Context, provider string, c EmailOAuthOptions, t *EmailOAuthTokenResponse) (*EmailOAuthProfile, error) {
	return FetchEmailOAuthProfile(ctx, provider, c, t)
}
