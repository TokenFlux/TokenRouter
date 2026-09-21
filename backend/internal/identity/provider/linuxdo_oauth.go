// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	context "context"
	errors "errors"
	fmt "fmt"
	url "net/url"
	strconv "strconv"
	strings "strings"
	time "time"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	req "github.com/imroc/req/v3"
	gjson "github.com/tidwall/gjson"
)

type LinuxDoTokenResponse = identity.LinuxDoTokenResponse
type LinuxDoTokenExchangeError = identity.LinuxDoTokenExchangeError

// LinuxDoClient 接入身份端口，复用原 HTTP 与解析实现。
type LinuxDoClient struct{}

func (LinuxDoClient) ExchangeCode(ctx context.Context, o LinuxDoOptions, code, redirect, verifier string) (*LinuxDoTokenResponse, error) {
	return LinuxDoExchangeCode(ctx, o, code, redirect, verifier)
}
func (LinuxDoClient) FetchUserInfo(ctx context.Context, o LinuxDoOptions, t *LinuxDoTokenResponse) (string, string, string, string, string, error) {
	return LinuxDoFetchUserInfo(ctx, o, t)
}
func LinuxDoExchangeCode(
	ctx context.Context,
	cfg LinuxDoOptions,
	code string,
	redirectURI string,
	codeVerifier string,
) (*LinuxDoTokenResponse, error) {
	client := req.C().SetTimeout(30 * time.Second)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", cfg.ClientID)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	if strings.TrimSpace(codeVerifier) != "" {
		form.Set("code_verifier", codeVerifier)
	}

	r := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json")

	switch strings.ToLower(strings.TrimSpace(cfg.TokenAuthMethod)) {
	case "", "client_secret_post":
		form.Set("client_secret", cfg.ClientSecret)
	case "client_secret_basic":
		r.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	case "none":
	default:
		return nil, fmt.Errorf("unsupported token_auth_method: %s", cfg.TokenAuthMethod)
	}

	resp, err := r.SetFormDataFromValues(form).Post(cfg.TokenURL)
	if err != nil {
		return nil, fmt.Errorf("request token: %w", err)
	}
	body := strings.TrimSpace(resp.String())
	if !resp.IsSuccessState() {
		providerErr, providerDesc := ParseOAuthProviderError(body)
		return nil, &LinuxDoTokenExchangeError{
			StatusCode:          resp.StatusCode,
			ProviderError:       providerErr,
			ProviderDescription: providerDesc,
			Body:                body,
		}
	}

	tokenResp, ok := ParseLinuxDoTokenResponse(body)
	if !ok || strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, &LinuxDoTokenExchangeError{
			StatusCode: resp.StatusCode,
			Body:       body,
		}
	}
	if strings.TrimSpace(tokenResp.TokenType) == "" {
		tokenResp.TokenType = "Bearer"
	}
	return tokenResp, nil
}

func LinuxDoFetchUserInfo(
	ctx context.Context,
	cfg LinuxDoOptions,
	token *LinuxDoTokenResponse,
) (email string, username string, subject string, displayName string, avatarURL string, err error) {
	client := req.C().SetTimeout(30 * time.Second)
	authorization, err := BuildBearerAuthorization(token.TokenType, token.AccessToken)
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("invalid token for userinfo request: %w", err)
	}

	resp, err := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetHeader("Authorization", authorization).
		Get(cfg.UserInfoURL)
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("request userinfo: %w", err)
	}
	if !resp.IsSuccessState() {
		return "", "", "", "", "", fmt.Errorf("userinfo status=%d", resp.StatusCode)
	}

	return LinuxDoParseUserInfo(resp.String(), cfg)
}

func LinuxDoParseUserInfo(body string, cfg LinuxDoOptions) (email string, username string, subject string, displayName string, avatarURL string, err error) {
	email = identity.OAuthFirstNonEmpty(
		GetGJSON(body, cfg.UserInfoEmailPath),
		GetGJSON(body, "email"),
		GetGJSON(body, "user.email"),
		GetGJSON(body, "data.email"),
		GetGJSON(body, "attributes.email"),
	)
	username = identity.OAuthFirstNonEmpty(
		GetGJSON(body, cfg.UserInfoUsernamePath),
		GetGJSON(body, "username"),
		GetGJSON(body, "preferred_username"),
		GetGJSON(body, "name"),
		GetGJSON(body, "user.username"),
		GetGJSON(body, "user.name"),
	)
	subject = identity.OAuthFirstNonEmpty(
		GetGJSON(body, cfg.UserInfoIDPath),
		GetGJSON(body, "sub"),
		GetGJSON(body, "id"),
		GetGJSON(body, "user_id"),
		GetGJSON(body, "uid"),
		GetGJSON(body, "user.id"),
	)

	displayName = identity.OAuthFirstNonEmpty(
		GetGJSON(body, "name"),
		GetGJSON(body, "nickname"),
		GetGJSON(body, "display_name"),
		GetGJSON(body, "user.name"),
		GetGJSON(body, "user.username"),
		username,
	)
	avatarURL = identity.OAuthFirstNonEmpty(
		GetGJSON(body, "avatar_url"),
		GetGJSON(body, "avatar"),
		GetGJSON(body, "picture"),
		GetGJSON(body, "profile_image_url"),
		GetGJSON(body, "user.avatar"),
		GetGJSON(body, "user.avatar_url"),
	)

	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", "", "", "", "", errors.New("userinfo missing id field")
	}
	if !identity.OAuthIsSafeLinuxDoSubject(subject) {
		return "", "", "", "", "", errors.New("userinfo returned invalid id field")
	}

	email = strings.TrimSpace(email)
	if email == "" {
		// LinuxDo Connect 的 userinfo 可能不提供 email。为兼容现有用户模型（email 必填且唯一），使用稳定的合成邮箱。
		email = identity.OAuthLinuxDoSyntheticEmail(subject)
	}

	username = strings.TrimSpace(username)
	if username == "" {
		username = "linuxdo_" + subject
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = username
	}
	avatarURL = strings.TrimSpace(avatarURL)

	return email, username, subject, displayName, avatarURL, nil
}

func ParseOAuthProviderError(body string) (providerErr string, providerDesc string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", ""
	}

	providerErr = identity.OAuthFirstNonEmpty(
		GetGJSON(body, "error"),
		GetGJSON(body, "code"),
		GetGJSON(body, "error.code"),
	)
	providerDesc = identity.OAuthFirstNonEmpty(
		GetGJSON(body, "error_description"),
		GetGJSON(body, "error.message"),
		GetGJSON(body, "message"),
		GetGJSON(body, "detail"),
	)

	if providerErr != "" || providerDesc != "" {
		return providerErr, providerDesc
	}

	values, err := url.ParseQuery(body)
	if err != nil {
		return "", ""
	}
	providerErr = identity.OAuthFirstNonEmpty(values.Get("error"), values.Get("code"))
	providerDesc = identity.OAuthFirstNonEmpty(values.Get("error_description"), values.Get("error_message"), values.Get("message"))
	return providerErr, providerDesc
}

func ParseLinuxDoTokenResponse(body string) (*LinuxDoTokenResponse, bool) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, false
	}

	accessToken := strings.TrimSpace(GetGJSON(body, "access_token"))
	if accessToken != "" {
		tokenType := strings.TrimSpace(GetGJSON(body, "token_type"))
		refreshToken := strings.TrimSpace(GetGJSON(body, "refresh_token"))
		scope := strings.TrimSpace(GetGJSON(body, "scope"))
		expiresIn := gjson.Get(body, "expires_in").Int()
		return &LinuxDoTokenResponse{
			AccessToken:  accessToken,
			TokenType:    tokenType,
			ExpiresIn:    expiresIn,
			RefreshToken: refreshToken,
			Scope:        scope,
		}, true
	}

	values, err := url.ParseQuery(body)
	if err != nil {
		return nil, false
	}
	accessToken = strings.TrimSpace(values.Get("access_token"))
	if accessToken == "" {
		return nil, false
	}
	expiresIn := int64(0)
	if raw := strings.TrimSpace(values.Get("expires_in")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			expiresIn = v
		}
	}
	return &LinuxDoTokenResponse{
		AccessToken:  accessToken,
		TokenType:    strings.TrimSpace(values.Get("token_type")),
		ExpiresIn:    expiresIn,
		RefreshToken: strings.TrimSpace(values.Get("refresh_token")),
		Scope:        strings.TrimSpace(values.Get("scope")),
	}, true
}

func GetGJSON(body string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	res := gjson.Get(body, path)
	if !res.Exists() {
		return ""
	}
	return res.String()
}

func BuildBearerAuthorization(tokenType, accessToken string) (string, error) {
	tokenType = strings.TrimSpace(tokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	if !strings.EqualFold(tokenType, "Bearer") {
		return "", fmt.Errorf("unsupported token_type: %s", tokenType)
	}

	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return "", errors.New("missing access_token")
	}
	if strings.ContainsAny(accessToken, " \t\r\n") {
		return "", errors.New("access_token contains whitespace")
	}
	return "Bearer " + accessToken, nil
}
