package qoder

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// OpenAPIBaseURL 是 qodercli 浏览器登录使用的 Qoder OpenAPI 服务地址。
	OpenAPIBaseURL = "https://openapi.qoder.sh"

	// OAuthClientID 是 qodercli 设备授权使用的公开 client ID。
	OAuthClientID = "e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb"

	// DeviceAuthorizationURL 是 qodercli 登录使用的浏览器授权地址。
	DeviceAuthorizationURL = "https://qoder.com/device/selectAccounts"

	// DevicePollPath 是浏览器授权后轮询 device token 的端点。
	DevicePollPath = "/api/v1/deviceToken/poll"

	// UserInfoPath 返回 access token 对应 Qoder 用户身份的端点。
	UserInfoPath = "/api/v1/userinfo"

	// OrganizationTagsPathPrefix 返回 Qoder 用户组织元数据的端点前缀。
	OrganizationTagsPathPrefix = "/api/v1/organizations/"
)

type DeviceAuthRequest struct {
	Nonce         string
	CodeVerifier  string
	CodeChallenge string
	MachineID     string
	ClientID      string
}

type DeviceTokenResponse struct {
	Token        string `json:"token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	UserID       string `json:"user_id"`
}

type UserInfo struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	UserType         string `json:"userType"`
	Email            string `json:"email"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
}

type OrganizationTags struct {
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
}

func (u *UserInfo) UnmarshalJSON(data []byte) error {
	type userInfoAlias UserInfo
	var raw struct {
		userInfoAlias
		UserTypeCamel         string `json:"user_type"`
		OrganizationIDCamel   string `json:"organizationId"`
		OrganizationNameCamel string `json:"organizationName"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*u = UserInfo(raw.userInfoAlias)
	if strings.TrimSpace(u.UserType) == "" {
		u.UserType = raw.UserTypeCamel
	}
	if strings.TrimSpace(u.OrganizationID) == "" {
		u.OrganizationID = raw.OrganizationIDCamel
	}
	if strings.TrimSpace(u.OrganizationName) == "" {
		u.OrganizationName = raw.OrganizationNameCamel
	}
	return nil
}

func (o *OrganizationTags) UnmarshalJSON(data []byte) error {
	type organizationTagsAlias OrganizationTags
	var raw struct {
		organizationTagsAlias
		ID                    string `json:"id"`
		Name                  string `json:"name"`
		OrganizationIDCamel   string `json:"organizationId"`
		OrganizationNameCamel string `json:"organizationName"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*o = OrganizationTags(raw.organizationTagsAlias)
	if strings.TrimSpace(o.OrganizationID) == "" {
		o.OrganizationID = firstNonEmpty(raw.OrganizationIDCamel, raw.ID)
	}
	if strings.TrimSpace(o.OrganizationName) == "" {
		o.OrganizationName = firstNonEmpty(raw.OrganizationNameCamel, raw.Name)
	}
	return nil
}

type OAuthClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewOAuthClient(baseURL string, httpClient *http.Client) *OAuthClient {
	if baseURL == "" {
		baseURL = OpenAPIBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &OAuthClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: httpClient,
	}
}

func NewDeviceAuthRequest() (*DeviceAuthRequest, error) {
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		return nil, err
	}
	return &DeviceAuthRequest{
		Nonce:         RandomUUIDLike(),
		CodeVerifier:  verifier,
		CodeChallenge: GenerateCodeChallenge(verifier),
		MachineID:     RandomToken(50),
		ClientID:      OAuthClientID,
	}, nil
}

func (r *DeviceAuthRequest) AuthorizationURL() string {
	clientID := strings.TrimSpace(r.ClientID)
	if clientID == "" {
		clientID = OAuthClientID
	}
	params := url.Values{}
	params.Set("nonce", r.Nonce)
	params.Set("challenge", r.CodeChallenge)
	params.Set("challenge_method", "S256")
	params.Set("client_id", clientID)
	params.Set("machine_id", r.MachineID)
	return DeviceAuthorizationURL + "?" + params.Encode()
}

func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func GenerateCodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func RandomUUIDLike() string {
	hex := RandomHex(32)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex[:8], hex[8:12], hex[12:16], hex[16:20], hex[20:])
}

func (c *OAuthClient) PollDeviceToken(ctx context.Context, nonce, verifier string) (*DeviceTokenResponse, bool, error) {
	if c == nil {
		c = NewOAuthClient("", nil)
	}
	values := url.Values{}
	values.Set("nonce", strings.TrimSpace(nonce))
	values.Set("verifier", strings.TrimSpace(verifier))
	values.Set("challenge_method", "S256")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+DevicePollPath+"?"+values.Encode(), nil)
	if err != nil {
		return nil, false, fmt.Errorf("qoder: create device token poll request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Go-http-client/2.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("qoder: device token poll request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, false, fmt.Errorf("qoder: device token poll failed with status %d: %s", resp.StatusCode, RedactSensitiveText(string(body)))
	}

	var token DeviceTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, false, fmt.Errorf("qoder: parse device token poll response: %w", err)
	}
	if strings.TrimSpace(token.Token) == "" && strings.TrimSpace(token.AccessToken) == "" {
		return nil, false, nil
	}
	return &token, true, nil
}

func (c *OAuthClient) GetUserInfo(ctx context.Context, token string) (*UserInfo, error) {
	if c == nil {
		c = NewOAuthClient("", nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+UserInfoPath, nil)
	if err != nil {
		return nil, fmt.Errorf("qoder: create userinfo request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("User-Agent", "Go-http-client/2.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: userinfo request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("qoder: userinfo failed with status %d: %s", resp.StatusCode, RedactSensitiveText(string(body)))
	}

	var info UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("qoder: parse userinfo response: %w", err)
	}
	return &info, nil
}

func (c *OAuthClient) GetOrganizationTags(ctx context.Context, token, uid string) (*OrganizationTags, error) {
	if c == nil {
		c = NewOAuthClient("", nil)
	}
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil, fmt.Errorf("qoder: organization tags require uid")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+OrganizationTagsPathPrefix+url.PathEscape(uid)+"/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("qoder: create organization tags request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("User-Agent", "Go-http-client/2.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: organization tags request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("qoder: organization tags failed with status %d: %s", resp.StatusCode, RedactSensitiveText(string(body)))
	}

	var tags OrganizationTags
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("qoder: parse organization tags response: %w", err)
	}
	return &tags, nil
}

func (r *DeviceTokenResponse) AccessTokenValue() string {
	if r == nil {
		return ""
	}
	if strings.TrimSpace(r.Token) != "" {
		return strings.TrimSpace(r.Token)
	}
	return strings.TrimSpace(r.AccessToken)
}

func BuildIdentityFromDeviceToken(user *UserInfo, token *DeviceTokenResponse) *AuthIdentity {
	accessToken := token.AccessTokenValue()
	userID := strings.TrimSpace(token.UserID)
	name := ""
	userType := "personal_standard"
	organizationID := ""
	organizationName := ""
	if user != nil {
		if strings.TrimSpace(user.ID) != "" {
			userID = strings.TrimSpace(user.ID)
		}
		name = strings.TrimSpace(user.Name)
		if strings.TrimSpace(user.UserType) != "" {
			userType = strings.TrimSpace(user.UserType)
		}
		organizationID = strings.TrimSpace(user.OrganizationID)
		organizationName = strings.TrimSpace(user.OrganizationName)
	}
	return &AuthIdentity{
		Name:               name,
		AID:                userID,
		UID:                userID,
		OrganizationID:     organizationID,
		OrganizationName:   organizationName,
		UserType:           userType,
		SecurityOauthToken: accessToken,
		RefreshToken:       strings.TrimSpace(token.RefreshToken),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
