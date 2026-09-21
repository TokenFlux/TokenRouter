// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	context "context"
	json "encoding/json"
	fmt "fmt"
	io "io"
	http "net/http"
	url "net/url"
	strings "strings"
	time "time"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

// WeChatOptions 只包含客户端所需凭据与请求目标。
type WeChatOptions struct{ AppID, AppSecret, TokenURL, UserInfoURL string }

const DefaultWeChatTokenURL = "https://api.weixin.qq.com/sns/oauth2/access_token"
const DefaultWeChatUserInfoURL = "https://api.weixin.qq.com/sns/userinfo"

type WechatOAuthTokenResponse = identity.WeChatOAuthTokenResponse
type WechatOAuthUserInfoResponse = identity.WeChatOAuthUserInfoResponse

func FetchWeChatOAuthIdentity(ctx context.Context, cfg WeChatOptions, code string) (*WechatOAuthTokenResponse, *WechatOAuthUserInfoResponse, error) {
	tokenResp, err := ExchangeWeChatOAuthCode(ctx, cfg, code)
	if err != nil {
		return nil, nil, err
	}
	userInfo, err := FetchWeChatUserInfo(ctx, cfg.UserInfoURL, tokenResp)
	if err != nil {
		return nil, nil, err
	}
	return tokenResp, userInfo, nil
}

func ExchangeWeChatOAuthCode(ctx context.Context, cfg WeChatOptions, code string) (*WechatOAuthTokenResponse, error) {
	endpoint, err := url.Parse(cfg.TokenURL)
	if err != nil {
		return nil, fmt.Errorf("parse wechat access token url: %w", err)
	}

	query := endpoint.Query()
	query.Set("appid", cfg.AppID)
	query.Set("secret", cfg.AppSecret)
	query.Set("code", strings.TrimSpace(code))
	query.Set("grant_type", "authorization_code")
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build wechat access token request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request wechat access token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read wechat access token response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("wechat access token status=%d", resp.StatusCode)
	}

	var tokenResp WechatOAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("decode wechat access token response: %w", err)
	}
	if tokenResp.ErrCode != 0 {
		return nil, fmt.Errorf("wechat access token error=%d %s", tokenResp.ErrCode, strings.TrimSpace(tokenResp.ErrMsg))
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, fmt.Errorf("wechat access token missing access_token")
	}
	return &tokenResp, nil
}

func FetchWeChatUserInfo(ctx context.Context, userInfoURL string, tokenResp *WechatOAuthTokenResponse) (*WechatOAuthUserInfoResponse, error) {
	if tokenResp == nil {
		return nil, fmt.Errorf("wechat token response is nil")
	}

	endpoint, err := url.Parse(userInfoURL)
	if err != nil {
		return nil, fmt.Errorf("parse wechat userinfo url: %w", err)
	}
	query := endpoint.Query()
	query.Set("access_token", strings.TrimSpace(tokenResp.AccessToken))
	query.Set("openid", strings.TrimSpace(tokenResp.OpenID))
	query.Set("lang", "zh_CN")
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build wechat userinfo request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request wechat userinfo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read wechat userinfo response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("wechat userinfo status=%d", resp.StatusCode)
	}

	var userInfo WechatOAuthUserInfoResponse
	if err := json.Unmarshal(body, &userInfo); err != nil {
		return nil, fmt.Errorf("decode wechat userinfo response: %w", err)
	}
	if userInfo.ErrCode != 0 {
		return nil, fmt.Errorf("wechat userinfo error=%d %s", userInfo.ErrCode, strings.TrimSpace(userInfo.ErrMsg))
	}
	return &userInfo, nil
}

// WeChatClient 复用原授权/资料 HTTP，实现身份端口；地址可由本地测试显式注入。
type WeChatClient struct{ TokenURL, UserInfoURL string }

func (c WeChatClient) FetchIdentity(ctx context.Context, o identity.WeChatOAuthOptions, code string) (*WechatOAuthTokenResponse, *WechatOAuthUserInfoResponse, error) {
	return FetchWeChatOAuthIdentity(ctx, WeChatOptions{AppID: o.AppID, AppSecret: o.AppSecret, TokenURL: c.TokenURL, UserInfoURL: c.UserInfoURL}, code)
}
