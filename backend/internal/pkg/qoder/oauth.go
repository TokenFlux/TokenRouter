package qoder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/oauthpkce"

	"github.com/google/uuid"
)

const (
	// OpenAPIBaseURL 是国际站 OpenAPI 地址，保留名称以兼容旧调用。
	OpenAPIBaseURL = GlobalOpenAPIBaseURL

	// OAuthClientID 是国际站公开 client ID，保留名称以兼容旧调用。
	OAuthClientID = GlobalOAuthClientID

	// DeviceAuthorizationURL 是国际站授权地址，保留名称以兼容旧调用。
	DeviceAuthorizationURL = GlobalDeviceAuthorizationURL

	// DevicePollPath 是浏览器授权后轮询 device token 的端点。
	DevicePollPath = "/api/v1/deviceToken/poll"

	// UserInfoPath 返回 access token 对应 Qoder 用户身份的端点。
	UserInfoPath = "/api/v1/userinfo"

	// OrganizationTagsPathPrefix 返回 Qoder 用户组织元数据的端点前缀。
	OrganizationTagsPathPrefix = "/api/v1/organizations/"

	// JobTokenExchangePath 是国内站现代 QODER_PAT 的交换端点。
	JobTokenExchangePath = "/api/v1/jobToken/exchange"

	// DeviceTokenRefreshPath 是国内站 QoderCN20 token 刷新端点。
	DeviceTokenRefreshPath = "/api/v1/deviceToken/refresh"
)

type DeviceAuthRequest struct {
	Nonce         string
	CodeVerifier  string
	CodeChallenge string
	MachineID     string
	ClientID      string
	Profile       Profile
}

// FlexibleInt64 兼容 Qoder API 中以 JSON 数字或字符串返回的整数。
type FlexibleInt64 int64

// UnmarshalJSON 容错解析 JSON 数字、整数样式浮点数和数字字符串。
func (v *FlexibleInt64) UnmarshalJSON(data []byte) error {
	parsed, err := parseFlexibleInt64(data)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// parseFlexibleInt64 解析 Qoder API 常见的宽松整数表示。
func parseFlexibleInt64(data []byte) (FlexibleInt64, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || trimmed == `""` {
		return 0, nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return 0, err
		}
		trimmed = strings.TrimSpace(text)
	}
	parsed, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		floatValue, floatErr := strconv.ParseFloat(trimmed, 64)
		if floatErr != nil {
			return 0, fmt.Errorf("qoder: invalid integer %q", trimmed)
		}
		parsed = int64(floatValue)
	}
	return FlexibleInt64(parsed), nil
}

// DeviceTokenResponse 兼容国际设备授权和国内 QoderCN20 token 响应。
type DeviceTokenResponse struct {
	ID           string        `json:"id"`
	Token        string        `json:"token"`
	AccessToken  string        `json:"access_token"`
	RefreshToken string        `json:"refresh_token"`
	UserID       string        `json:"user_id"`
	UserName     string        `json:"user_name"`
	Email        string        `json:"email"`
	AvatarURL    string        `json:"avatar_url"`
	ExpiresAt    FlexibleInt64 `json:"expires_at"`
	ExpiresIn    FlexibleInt64 `json:"expires_in"`
	Scope        string        `json:"scope"`
	TokenType    string        `json:"token_type"`
	Nonce        string        `json:"nonce"`
}

// UnmarshalJSON 单独兼容 expires_at 的日期格式，并允许由有效 expires_in 回退。
func (r *DeviceTokenResponse) UnmarshalJSON(data []byte) error {
	type responseAlias DeviceTokenResponse

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	rawExpiresAt, hasExpiresAt := fields["expires_at"]
	delete(fields, "expires_at")

	remaining, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var decoded responseAlias
	if err := json.Unmarshal(remaining, &decoded); err != nil {
		return err
	}
	*r = DeviceTokenResponse(decoded)
	if !hasExpiresAt {
		return nil
	}

	expiresAt, err := parseFlexibleExpiresAt(rawExpiresAt)
	if err != nil {
		if int64(r.ExpiresIn) > 0 {
			return nil
		}
		return fmt.Errorf("qoder: invalid expires_at: %w", err)
	}
	r.ExpiresAt = expiresAt
	return nil
}

// parseFlexibleExpiresAt 将数字或常见 ISO 日期统一转换为 Unix 秒。
func parseFlexibleExpiresAt(data []byte) (FlexibleInt64, error) {
	parsed, err := parseFlexibleInt64(data)
	if err == nil {
		return parsed, nil
	}

	var text string
	if unmarshalErr := json.Unmarshal(data, &text); unmarshalErr != nil {
		return 0, err
	}
	text = strings.TrimSpace(text)
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		time.DateOnly,
	} {
		var parsedTime time.Time
		if strings.Contains(layout, "Z07:00") {
			parsedTime, err = time.Parse(layout, text)
		} else {
			parsedTime, err = time.ParseInLocation(layout, text, time.UTC)
		}
		if err == nil {
			return FlexibleInt64(parsedTime.Unix()), nil
		}
	}
	return 0, fmt.Errorf("qoder: invalid expiry %q", text)
}

// UserInfo 兼容两站 userinfo 的 snake_case 与 camelCase 字段。
type UserInfo struct {
	ID               string `json:"id"`
	UserID           string `json:"user_id"`
	Name             string `json:"name"`
	UserName         string `json:"user_name"`
	UserType         string `json:"userType"`
	Email            string `json:"email"`
	AvatarURL        string `json:"avatar_url"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
}

type OrganizationTags struct {
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
}

func (u *UserInfo) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID                    string `json:"id"`
		UserID                string `json:"user_id"`
		Name                  string `json:"name"`
		UserName              string `json:"user_name"`
		Email                 string `json:"email"`
		AvatarURL             string `json:"avatar_url"`
		AvatarURLCamel        string `json:"avatarUrl"`
		UserType              string `json:"userType"`
		UserTypeSnake         string `json:"user_type"`
		OrganizationID        string `json:"organization_id"`
		OrganizationName      string `json:"organization_name"`
		OrganizationIDCamel   string `json:"organizationId"`
		OrganizationNameCamel string `json:"organizationName"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*u = UserInfo{
		ID:               raw.ID,
		UserID:           raw.UserID,
		Name:             raw.Name,
		UserName:         raw.UserName,
		UserType:         firstNonEmpty(raw.UserType, raw.UserTypeSnake),
		Email:            raw.Email,
		AvatarURL:        firstNonEmpty(raw.AvatarURL, raw.AvatarURLCamel),
		OrganizationID:   firstNonEmpty(raw.OrganizationID, raw.OrganizationIDCamel),
		OrganizationName: firstNonEmpty(raw.OrganizationName, raw.OrganizationNameCamel),
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
	Profile    Profile
	Doer       RequestDoer
}

func NewOAuthClient(baseURL string, httpClient *http.Client) *OAuthClient {
	profile := MustProfileForSite(SiteGlobal)
	if strings.TrimSpace(baseURL) != "" {
		profile.OpenAPIBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
	return NewOAuthClientForProfile(profile, httpClient)
}

// NewOAuthClientForProfile 使用站点 profile 创建 OpenAPI 客户端。
func NewOAuthClientForProfile(profile Profile, httpClient *http.Client) *OAuthClient {
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		normalized = MustProfileForSite(SiteGlobal)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &OAuthClient{
		BaseURL:    strings.TrimRight(normalized.OpenAPIBaseURL, "/"),
		HTTPClient: httpClient,
		Profile:    normalized,
	}
}

func NewDeviceAuthRequest() (*DeviceAuthRequest, error) {
	return NewDeviceAuthRequestForProfile(MustProfileForSite(SiteGlobal))
}

// NewDeviceAuthRequestForSite 为指定站点生成设备授权参数。
func NewDeviceAuthRequestForSite(site Site) (*DeviceAuthRequest, error) {
	profile, err := ProfileForSite(site)
	if err != nil {
		return nil, err
	}
	return NewDeviceAuthRequestForProfile(profile)
}

// NewDeviceAuthRequestForProfile 使用冻结的站点 profile 生成设备授权参数。
func NewDeviceAuthRequestForProfile(profile Profile) (*DeviceAuthRequest, error) {
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		return nil, err
	}
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		return nil, err
	}
	nonce := RandomUUIDLike()
	machineID := RandomToken(50)
	if normalized.Site == SiteCN {
		nonce = RandomHex(32)
		machineID = RandomUUIDLike()
	}
	return &DeviceAuthRequest{
		Nonce:         nonce,
		CodeVerifier:  verifier,
		CodeChallenge: GenerateCodeChallenge(verifier),
		MachineID:     machineID,
		ClientID:      normalized.OAuthClientID,
		Profile:       normalized,
	}, nil
}

func (r *DeviceAuthRequest) AuthorizationURL() string {
	profile := r.Profile
	if strings.TrimSpace(profile.DeviceAuthorizationURL) == "" {
		profile = MustProfileForSite(SiteGlobal)
	}
	clientID := strings.TrimSpace(r.ClientID)
	if clientID == "" {
		clientID = profile.OAuthClientID
	}
	params := url.Values{}
	params.Set("nonce", r.Nonce)
	params.Set("challenge", r.CodeChallenge)
	params.Set("challenge_method", "S256")
	params.Set("client_id", clientID)
	params.Set("machine_id", r.MachineID)
	return profile.DeviceAuthorizationURL + "?" + params.Encode()
}

func GenerateCodeVerifier() (string, error) {
	return oauthpkce.Verifier()
}

func GenerateCodeChallenge(verifier string) string {
	return oauthpkce.Challenge(verifier)
}

// RandomUUIDLike 生成官方客户端使用的 UUID v4 字符串。
func RandomUUIDLike() string {
	return uuid.NewString()
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
	req.Header.Set("User-Agent", c.userAgent())

	resp, err := c.do(req)
	if err != nil {
		return nil, false, fmt.Errorf("qoder: device token poll request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

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
	req.Header.Set("User-Agent", c.userAgent())

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: userinfo request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

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
	req.Header.Set("User-Agent", c.userAgent())

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: organization tags request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

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

// ExchangeQoderCN20PAT 使用国内站现代 QODER_PAT 换取 OpenAPI token。
func (c *OAuthClient) ExchangeQoderCN20PAT(ctx context.Context, pat string) (*DeviceTokenResponse, error) {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return nil, fmt.Errorf("qoder: PAT is required")
	}
	body, err := json.Marshal(map[string]string{"personal_token": pat})
	if err != nil {
		return nil, fmt.Errorf("qoder: encode PAT exchange request: %w", err)
	}
	return c.postTokenRequest(ctx, JobTokenExchangePath, body, "PAT exchange")
}

// RefreshQoderCN20Token 使用国内站 refresh token 换取新的 OpenAPI token。
func (c *OAuthClient) RefreshQoderCN20Token(ctx context.Context, refreshToken string) (*DeviceTokenResponse, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("qoder: refresh token is required")
	}
	body, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return nil, fmt.Errorf("qoder: encode token refresh request: %w", err)
	}
	return c.postTokenRequest(ctx, DeviceTokenRefreshPath, body, "token refresh")
}

// CompleteQoderCN20Identity 使用本客户端冻结的 profile 与 transport 完成国内 COSY 身份交换。
func (c *OAuthClient) CompleteQoderCN20Identity(
	ctx context.Context,
	token *DeviceTokenResponse,
	user *UserInfo,
	machine *MachineIdentity,
) (*AuthIdentity, time.Time, error) {
	return CompleteQoderCN20IdentityContext(ctx, c.profile(), token, user, machine, c.do)
}

func (c *OAuthClient) postTokenRequest(ctx context.Context, path string, body []byte, operation string) (*DeviceTokenResponse, error) {
	if c == nil {
		c = NewOAuthClientForProfile(MustProfileForSite(SiteCN), nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("qoder: create %s request: %w", operation, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("Cosy-Version", c.profile().ClientVersion)
	req.Header.Set("Cosy-ClientType", "5")
	req.Header.Set("Cosy-MachineOS", MachineOS())
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: %s request: %w", operation, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &OpenAPIError{
			Operation:  operation,
			StatusCode: resp.StatusCode,
			Message:    RedactSensitiveText(string(responseBody)),
		}
	}
	var token DeviceTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, fmt.Errorf("qoder: parse %s response: %w", operation, err)
	}
	if _, err := token.ValidateQoderCN20(time.Now()); err != nil {
		return nil, fmt.Errorf("qoder: invalid %s response: %w", operation, err)
	}
	return &token, nil
}

// OpenAPIError 表示 OpenAPI 返回的脱敏 HTTP 错误。
type OpenAPIError struct {
	Operation  string
	StatusCode int
	Message    string
}

// Error 返回不包含原始凭据的错误文本。
func (e *OpenAPIError) Error() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		return fmt.Sprintf("qoder: %s failed with status %d", e.Operation, e.StatusCode)
	}
	return fmt.Sprintf("qoder: %s failed with status %d: %s", e.Operation, e.StatusCode, message)
}

// InvalidCredentials 判断 OpenAPI 错误是否明确表示 PAT 或 refresh token 已失效。
func (e *OpenAPIError) InvalidCredentials() bool {
	if e == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(e.Operation)) {
	case "pat exchange":
		return e.StatusCode == http.StatusBadRequest ||
			e.StatusCode == http.StatusUnauthorized ||
			e.StatusCode == http.StatusForbidden
	case "token refresh":
		return e.StatusCode == http.StatusBadRequest || e.StatusCode == http.StatusUnauthorized
	default:
		return false
	}
}

// UpstreamFailure 判断 OpenAPI 错误是否为可重试的服务端故障。
func (e *OpenAPIError) UpstreamFailure() bool {
	return e != nil && e.StatusCode >= http.StatusInternalServerError
}

func (c *OAuthClient) profile() Profile {
	if c == nil {
		return MustProfileForSite(SiteGlobal)
	}
	profile, err := NormalizeProfile(c.Profile)
	if err != nil {
		profile = MustProfileForSite(SiteGlobal)
	}
	if strings.TrimSpace(c.BaseURL) != "" {
		profile.OpenAPIBaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	}
	return profile
}

func (c *OAuthClient) userAgent() string {
	return c.profile().OpenAPIUserAgent()
}

func (c *OAuthClient) do(req *http.Request) (*http.Response, error) {
	if c != nil && c.Doer != nil {
		return c.Doer(req)
	}
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient.Do(req)
	}
	return http.DefaultClient.Do(req)
}

func (r *DeviceTokenResponse) AccessTokenValue() string {
	if r == nil {
		return ""
	}
	// 官方 Qoder CN 客户端优先使用 access_token，token 仅用于旧响应兼容回退。
	if strings.TrimSpace(r.AccessToken) != "" {
		return strings.TrimSpace(r.AccessToken)
	}
	return strings.TrimSpace(r.Token)
}

// ExpiryTime 将 token 响应中的绝对或相对有效期规范化为 UTC 时间。
func (r *DeviceTokenResponse) ExpiryTime(now time.Time) time.Time {
	if r == nil {
		return time.Time{}
	}
	expiresAt := int64(r.ExpiresAt)
	if expiresAt > 0 {
		switch {
		case expiresAt >= 1_000_000_000_000:
			return time.UnixMilli(expiresAt).UTC()
		case expiresAt >= 1_000_000_000:
			return time.Unix(expiresAt, 0).UTC()
		default:
			return now.Add(time.Duration(expiresAt) * time.Second).UTC()
		}
	}
	expiresIn := int64(r.ExpiresIn)
	if expiresIn <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(expiresIn) * time.Second).UTC()
}

// ValidateQoderCN20 校验国内标准 OAuth/PAT 返回并规范化过期时间。
func (r *DeviceTokenResponse) ValidateQoderCN20(now time.Time) (time.Time, error) {
	if r == nil {
		return time.Time{}, fmt.Errorf("token response is empty")
	}
	if r.AccessTokenValue() == "" {
		return time.Time{}, fmt.Errorf("access token is missing")
	}
	if strings.TrimSpace(r.RefreshToken) == "" {
		return time.Time{}, fmt.Errorf("refresh token is missing")
	}
	expiresAt := r.ExpiryTime(now)
	if expiresAt.IsZero() {
		return time.Time{}, fmt.Errorf("positive expiry is missing")
	}
	return expiresAt, nil
}

func BuildIdentityFromDeviceToken(user *UserInfo, token *DeviceTokenResponse) *AuthIdentity {
	accessToken := token.AccessTokenValue()
	userID := strings.TrimSpace(token.UserID)
	name := ""
	userType := "personal_standard"
	organizationID := ""
	organizationName := ""
	if user != nil {
		if resolvedUserID := firstNonEmpty(user.UserID, user.ID); resolvedUserID != "" {
			userID = resolvedUserID
		}
		name = firstNonEmpty(user.Name, user.UserName, token.UserName)
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
