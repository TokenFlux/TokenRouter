package qoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	// AuthStatusPath 是 QoderCN20 OpenAPI 身份换取最终 COSY 身份的逻辑路径。
	AuthStatusPath = "/api/v3/user/status"
	// CosyRefreshTokenPath 是国内手动 COSY token 的旧式刷新逻辑路径。
	CosyRefreshTokenPath = "/api/v3/user/refresh_token"
	// QuotaUsagePath 是两站额度查询使用的逻辑路径。
	QuotaUsagePath = "/api/v2/quota/usage"
)

// ExchangeQoderCN20PATContext 完成国内现代 PAT 的 exchange、userinfo 和 status 链路。
func ExchangeQoderCN20PATContext(
	ctx context.Context,
	pat string,
	machine *MachineIdentity,
	profile Profile,
	doer RequestDoer,
) (*AuthIdentity, time.Time, error) {
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		return nil, time.Time{}, err
	}
	if normalized.Site != SiteCN {
		return nil, time.Time{}, fmt.Errorf("qoder: QoderCN20 PAT requires cn site")
	}
	if machine == nil {
		machine = NewMachineForSite(normalized.Site)
	}
	openAPI := NewOAuthClientForProfile(normalized, nil)
	openAPI.Doer = doer
	token, err := openAPI.ExchangeQoderCN20PAT(ctx, pat)
	if err != nil {
		return nil, time.Time{}, err
	}
	user, err := openAPI.GetUserInfo(ctx, token.AccessTokenValue())
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("qoder: load PAT userinfo: %w", err)
	}
	return CompleteQoderCN20IdentityContext(ctx, normalized, token, user, machine, doer)
}

// RefreshQoderCN20SessionContext 完成国内标准 OAuth 的 refresh、userinfo 和 status 链路。
func RefreshQoderCN20SessionContext(
	ctx context.Context,
	refreshToken string,
	machine *MachineIdentity,
	profile Profile,
	doer RequestDoer,
) (*AuthIdentity, time.Time, error) {
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		return nil, time.Time{}, err
	}
	if normalized.Site != SiteCN {
		return nil, time.Time{}, fmt.Errorf("qoder: QoderCN20 refresh requires cn site")
	}
	if machine == nil {
		machine = NewMachineForSite(normalized.Site)
	}
	openAPI := NewOAuthClientForProfile(normalized, nil)
	openAPI.Doer = doer
	token, err := openAPI.RefreshQoderCN20Token(ctx, refreshToken)
	if err != nil {
		return nil, time.Time{}, err
	}
	user, err := openAPI.GetUserInfo(ctx, token.AccessTokenValue())
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("qoder: load refreshed userinfo: %w", err)
	}
	return CompleteQoderCN20IdentityContext(ctx, normalized, token, user, machine, doer)
}

// AuthStatusAuthInfo 是 Gateway 身份请求内嵌的可选账号信息。
type AuthStatusAuthInfo struct {
	UserName       string `json:"userName,omitempty"`
	OrganizationID string `json:"orgId,omitempty"`
}

// AuthStatusParams 完整对应官方 Gateway 身份请求结构；末尾三个零值字段也必须发送。
type AuthStatusParams struct {
	AccessKey          string             `json:"accessKey,omitempty"`
	SecretKey          string             `json:"secretKey,omitempty"`
	SecurityToken      string             `json:"securityToken,omitempty"`
	UserID             string             `json:"userId,omitempty"`
	OrganizationID     string             `json:"orgId,omitempty"`
	Token              string             `json:"token,omitempty"`
	PersonalToken      string             `json:"personalToken"`
	SecurityOauthToken string             `json:"securityOauthToken"`
	RefreshToken       string             `json:"refreshToken"`
	NeedRefresh        bool               `json:"needRefresh"`
	AuthInfo           AuthStatusAuthInfo `json:"authInfo"`
}

// gatewayHTTPPayload 对应官方 remoting.HttpPayload，是 Gateway 身份接口的外层载荷。
type gatewayHTTPPayload struct {
	Payload       string `json:"payload"`
	EncodeVersion string `json:"encodeVersion"`
	RequestID     string `json:"requestId,omitempty"`
}

// AuthStatusResult 兼容国内 Gateway status/refresh 返回的完整身份字段。
type AuthStatusResult struct {
	Name                      string         `json:"name"`
	ID                        string         `json:"id"`
	AccountID                 string         `json:"accountId"`
	StaffID                   string         `json:"staffId"`
	Token                     string         `json:"token"`
	Quota                     any            `json:"quota"`
	WhitelistStatus           any            `json:"whitelistStatus"`
	OrganizationID            string         `json:"orgId"`
	OrganizationName          string         `json:"orgName"`
	YxUID                     string         `json:"yxUid"`
	AvatarURL                 string         `json:"avatarUrl"`
	SecurityOauthToken        string         `json:"securityOauthToken"`
	RefreshToken              string         `json:"refreshToken"`
	ExpireTime                FlexibleInt64  `json:"expireTime"`
	IsSubAccount              bool           `json:"isSubAccount"`
	Email                     string         `json:"email"`
	UserType                  string         `json:"userType"`
	IsPrivacyPolicyModifiable bool           `json:"isPrivacyPolicyModifiable"`
	IsQuotaExceeded           bool           `json:"isQuotaExceeded"`
	FeatureSwitches           map[string]any `json:"featureSwitches"`
	TeamSwitches              map[string]any `json:"teamSwitches"`
}

// CompleteQoderCN20IdentityContext 将国内 OpenAPI token 换成可用于推理的最终 COSY 身份。
func CompleteQoderCN20IdentityContext(
	ctx context.Context,
	profile Profile,
	token *DeviceTokenResponse,
	user *UserInfo,
	machine *MachineIdentity,
	doer RequestDoer,
) (*AuthIdentity, time.Time, error) {
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		return nil, time.Time{}, err
	}
	if normalized.Site != SiteCN {
		return nil, time.Time{}, fmt.Errorf("qoder: QoderCN20 completion requires cn site")
	}
	if machine == nil {
		machine = NewMachineForSite(normalized.Site)
	}
	expiresAt, err := token.ValidateQoderCN20(time.Now())
	if err != nil {
		return nil, time.Time{}, err
	}
	// status 会校验用户与 token 的关联关系，必须优先使用设备 token 响应中的 user_id。
	userID := firstNonEmpty(token.UserID, userIDFromInfo(user))
	if userID == "" {
		return nil, time.Time{}, fmt.Errorf("qoder: QoderCN20 completion requires user id")
	}
	provisional := &AuthIdentity{
		Name:               firstNonEmpty(userNameFromInfo(user), token.UserName),
		AID:                userID,
		UID:                userID,
		UserType:           "personal_standard",
		SecurityOauthToken: token.AccessTokenValue(),
		RefreshToken:       strings.TrimSpace(token.RefreshToken),
	}
	// status 使用登录前 Appcode 签名模式，不构造或发送 COSY Authorization。
	session := &SessionContext{
		Machine:       machine,
		Site:          normalized.Site,
		ClientVersion: normalized.ClientVersion,
	}
	params := AuthStatusParams{
		UserID:             userID,
		SecurityOauthToken: provisional.SecurityOauthToken,
		RefreshToken:       provisional.RefreshToken,
	}
	body, err := marshalAuthStatusRequest(params)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("qoder: encode auth status request: %w", err)
	}
	client := NewClientForProfile(normalized)
	var status AuthStatusResult
	if err := client.SignatureJSONRequestContextWithDoer(ctx, http.MethodPost, session, AuthStatusPath, body, nil, doer, &status); err != nil {
		return nil, time.Time{}, fmt.Errorf("qoder: complete QoderCN20 identity: %w", err)
	}
	identity, err := identityFromAuthStatus(&status, provisional)
	if err != nil {
		return nil, time.Time{}, err
	}
	// 国内标准登录保留本次 OpenAPI 的 effective access/refresh token，status 只补充账号状态。
	identity.SecurityOauthToken = provisional.SecurityOauthToken
	identity.RefreshToken = strings.TrimSpace(token.RefreshToken)
	return identity, expiresAt, nil
}

// RefreshCosySessionForProfileContext 使用国内 Gateway 旧式协议刷新手动 COSY token。
func RefreshCosySessionForProfileContext(
	ctx context.Context,
	profile Profile,
	refreshToken string,
	securityOauthToken string,
	userID string,
	organizationID string,
	machine *MachineIdentity,
	doer RequestDoer,
) (*AuthIdentity, error) {
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		return nil, err
	}
	if normalized.Site != SiteCN {
		return nil, fmt.Errorf("qoder: Gateway COSY refresh requires cn site")
	}
	if machine == nil {
		machine = NewMachineForSite(normalized.Site)
	}
	provisional := &AuthIdentity{
		AID:                strings.TrimSpace(userID),
		UID:                strings.TrimSpace(userID),
		OrganizationID:     strings.TrimSpace(organizationID),
		UserType:           "personal_standard",
		SecurityOauthToken: strings.TrimSpace(securityOauthToken),
		RefreshToken:       strings.TrimSpace(refreshToken),
	}
	if provisional.UID == "" || provisional.SecurityOauthToken == "" || provisional.RefreshToken == "" {
		return nil, fmt.Errorf("qoder: COSY refresh requires user id, security token and refresh token")
	}
	session, err := NewSessionForProfileWithKey(provisional, machine, normalized, nil)
	if err != nil {
		return nil, err
	}
	body, err := marshalAuthStatusRequest(AuthStatusParams{
		UserID:             provisional.UID,
		OrganizationID:     provisional.OrganizationID,
		SecurityOauthToken: provisional.SecurityOauthToken,
		RefreshToken:       provisional.RefreshToken,
	})
	if err != nil {
		return nil, fmt.Errorf("qoder: encode COSY refresh request: %w", err)
	}
	client := NewClientForProfile(normalized)
	var status AuthStatusResult
	if err := client.JSONRequestContextWithDoer(ctx, http.MethodPost, session, CosyRefreshTokenPath, body, nil, doer, &status); err != nil {
		return nil, fmt.Errorf("qoder: refresh COSY identity: %w", err)
	}
	identity, err := identityFromAuthStatus(&status, provisional)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(identity.RefreshToken) == "" {
		identity.RefreshToken = provisional.RefreshToken
	}
	return identity, nil
}

// marshalAuthStatusRequest 先把身份参数序列化成字符串，再按官方协议包入 EncodeVersion=1 的外层对象。
func marshalAuthStatusRequest(params AuthStatusParams) ([]byte, error) {
	inner, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode inner payload: %w", err)
	}
	body, err := json.Marshal(gatewayHTTPPayload{
		Payload:       string(inner),
		EncodeVersion: "1",
	})
	if err != nil {
		return nil, fmt.Errorf("encode request envelope: %w", err)
	}
	return body, nil
}

func identityFromAuthStatus(status *AuthStatusResult, fallback *AuthIdentity) (*AuthIdentity, error) {
	if status == nil {
		return nil, fmt.Errorf("qoder: auth status response is empty")
	}
	identity := &AuthIdentity{
		Name:             firstNonEmpty(status.Name, fallbackIdentityValue(fallback, func(v *AuthIdentity) string { return v.Name })),
		AID:              firstNonEmpty(status.AccountID, status.ID, fallbackIdentityValue(fallback, func(v *AuthIdentity) string { return v.AID })),
		UID:              firstNonEmpty(status.ID, status.AccountID, fallbackIdentityValue(fallback, func(v *AuthIdentity) string { return v.UID })),
		YxUID:            status.YxUID,
		OrganizationID:   firstNonEmpty(status.OrganizationID, fallbackIdentityValue(fallback, func(v *AuthIdentity) string { return v.OrganizationID })),
		OrganizationName: firstNonEmpty(status.OrganizationName, fallbackIdentityValue(fallback, func(v *AuthIdentity) string { return v.OrganizationName })),
		UserType:         firstNonEmpty(status.UserType, fallbackIdentityValue(fallback, func(v *AuthIdentity) string { return v.UserType }), "personal_standard"),
		// QoderCN20 status 只负责校验并返回账号状态，成功响应可能不重复回传请求中的 token。
		SecurityOauthToken: firstNonEmpty(status.SecurityOauthToken, fallbackIdentityValue(fallback, func(v *AuthIdentity) string { return v.SecurityOauthToken })),
		RefreshToken:       firstNonEmpty(status.RefreshToken, fallbackIdentityValue(fallback, func(v *AuthIdentity) string { return v.RefreshToken })),
	}
	if identity.SecurityOauthToken == "" {
		return nil, fmt.Errorf("qoder: auth status response missing securityOauthToken")
	}
	return identity, nil
}

func fallbackIdentityValue(identity *AuthIdentity, getter func(*AuthIdentity) string) string {
	if identity == nil {
		return ""
	}
	return getter(identity)
}

func userIDFromInfo(user *UserInfo) string {
	if user == nil {
		return ""
	}
	return firstNonEmpty(user.UserID, user.ID)
}

func userNameFromInfo(user *UserInfo) string {
	if user == nil {
		return ""
	}
	return firstNonEmpty(user.Name, user.UserName)
}
