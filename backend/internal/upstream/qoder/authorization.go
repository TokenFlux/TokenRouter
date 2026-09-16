// 本文件拥有 Qoder 授权协议交换；授权会话的认领、缓存与清理由 account 负责。
package qoder

import (
	"context"
	"errors"
	"strings"
	"time"

	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

type OAuthClientPort interface {
	PollDeviceToken(ctx context.Context, nonce, verifier string) (*DeviceTokenResponse, bool, error)
	GetUserInfo(ctx context.Context, token string) (*UserInfo, error)
	GetOrganizationTags(ctx context.Context, token, uid string) (*OrganizationTags, error)
}
type CN20OAuthCompleter interface {
	CompleteQoderCN20Identity(
		ctx context.Context,
		token *DeviceTokenResponse,
		user *UserInfo,
		machine *MachineIdentity,
	) (*AuthIdentity, time.Time, error)
}
type OAuthClientFactory func(profile Profile, proxyURL string) (OAuthClientPort, error)
type AuthorizationTokenInfo struct {
	SecurityOauthToken string         `json:"security_oauth_token"`
	RefreshToken       string         `json:"refresh_token,omitempty"`
	MachineID          string         `json:"machine_id"`
	MachineToken       string         `json:"machine_token,omitempty"`
	MachineType        string         `json:"machine_type,omitempty"`
	UID                string         `json:"uid,omitempty"`
	AID                string         `json:"aid,omitempty"`
	OrganizationID     string         `json:"organization_id,omitempty"`
	OrganizationName   string         `json:"organization_name,omitempty"`
	Name               string         `json:"name,omitempty"`
	UserType           string         `json:"user_type,omitempty"`
	Site               string         `json:"site"`
	RefreshMode        string         `json:"refresh_mode"`
	ExpiresAt          string         `json:"expires_at,omitempty"`
	Extra              map[string]any `json:"extra,omitempty"`
}
type AuthorizationFlow struct {
	Nonce, CodeVerifier, AuthURL, ProxyURL string
	Machine                                *MachineIdentity
	Site                                   Site
	Profile                                Profile
}

func CompleteAuthorization(ctx context.Context, session *AuthorizationFlow, factory OAuthClientFactory) (*AuthorizationTokenInfo, bool, error) {
	client, err := factory(session.Profile, session.ProxyURL)
	if err != nil {
		return nil, false, err
	}

	tokenResp, ready, err := client.PollDeviceToken(ctx, session.Nonce, session.CodeVerifier)
	if err != nil {
		return nil, false, err
	}
	if !ready {
		return nil, true, nil
	}

	accessToken := tokenResp.AccessTokenValue()
	if session.Site == SiteCN {
		expiresAt, validateErr := tokenResp.ValidateQoderCN20(time.Now())
		if validateErr != nil {
			return nil, false, validateErr
		}
		userInfo, userErr := client.GetUserInfo(ctx, accessToken)
		if userErr != nil {
			return nil, false, userErr
		}
		completer, ok := client.(CN20OAuthCompleter)
		if !ok {
			return nil, false, errors.New("qoder CN OAuth client does not support status completion")
		}
		identity, completedExpiry, completeErr := completer.CompleteQoderCN20Identity(ctx, tokenResp, userInfo, session.Machine)
		if completeErr != nil {
			return nil, false, completeErr
		}
		if !completedExpiry.IsZero() {
			expiresAt = completedExpiry
		}
		// status 是国内身份主数据；仅在缺少组织字段时使用 OpenAPI tags 补充。
		organizationLookupUID := FirstNonEmptyQoder(tokenResp.UserID, tokenResp.ID)
		if userInfo != nil {
			organizationLookupUID = FirstNonEmptyQoder(userInfo.UserID, userInfo.ID, organizationLookupUID)
		}
		orgErr := populateQoderOrganizationForUID(ctx, client, accessToken, organizationLookupUID, identity)
		return BuildAuthorizationTokenInfoForSite(identity, session.Machine, SiteCN, RefreshModeQoderCN20, expiresAt, nil, orgErr), false, nil
	}
	userInfo, userErr := client.GetUserInfo(ctx, accessToken)
	if userErr != nil {
		userInfo = &UserInfo{ID: tokenResp.UserID}
	}
	identity := BuildIdentityFromDeviceToken(userInfo, tokenResp)
	orgErr := populateQoderOrganization(ctx, client, accessToken, identity)
	expiresAt := tokenResp.ExpiryTime(time.Now())
	return BuildAuthorizationTokenInfoForSite(identity, session.Machine, SiteGlobal, RefreshModeCosy, expiresAt, userErr, orgErr), false, nil
}
func populateQoderOrganization(ctx context.Context, client OAuthClientPort, token string, identity *AuthIdentity) error {
	return populateQoderOrganizationForUID(ctx, client, token, "", identity)
}
func populateQoderOrganizationForUID(ctx context.Context, client OAuthClientPort, token, lookupUID string, identity *AuthIdentity) error {
	if client == nil || identity == nil {
		return nil
	}
	if strings.TrimSpace(identity.OrganizationID) != "" {
		return nil
	}
	uid := strings.TrimSpace(lookupUID)
	if uid == "" {
		uid = strings.TrimSpace(identity.UID)
	}
	if uid == "" {
		uid = strings.TrimSpace(identity.AID)
	}
	if uid == "" {
		return nil
	}
	tags, err := client.GetOrganizationTags(ctx, token, uid)
	if err != nil {
		return err
	}
	if tags == nil {
		return nil
	}
	identity.OrganizationID = strings.TrimSpace(tags.OrganizationID)
	identity.OrganizationName = strings.TrimSpace(tags.OrganizationName)
	return nil
}
func BuildAuthorizationTokenInfo(identity *AuthIdentity, machine *MachineIdentity, userErr error, orgErr error) *AuthorizationTokenInfo {
	return BuildAuthorizationTokenInfoForSite(identity, machine, SiteGlobal, RefreshModeCosy, time.Time{}, userErr, orgErr)
}
func BuildAuthorizationTokenInfoForSite(
	identity *AuthIdentity,
	machine *MachineIdentity,
	site Site,
	refreshMode string,
	expiresAt time.Time,
	userErr error,
	orgErr error,
) *AuthorizationTokenInfo {
	if identity == nil {
		identity = &AuthIdentity{UserType: "personal_standard"}
	}
	if machine == nil {
		machine = NewMachineForSite(site)
	}
	extra := map[string]any{}
	if userErr != nil {
		extra["userinfo_warning"] = sanitizedQoderOAuthWarning("userinfo_unavailable", "Qoder user info could not be loaded")
	}
	if orgErr != nil {
		extra["organization_warning"] = sanitizedQoderOAuthWarning("organization_unavailable", "Qoder organization info could not be loaded")
	}
	if len(extra) == 0 {
		extra = nil
	}
	tokenInfo := &AuthorizationTokenInfo{
		SecurityOauthToken: strings.TrimSpace(identity.SecurityOauthToken),
		RefreshToken:       strings.TrimSpace(identity.RefreshToken),
		MachineID:          strings.TrimSpace(machine.MachineID),
		MachineToken:       strings.TrimSpace(machine.MachineToken),
		MachineType:        strings.TrimSpace(machine.MachineType),
		UID:                strings.TrimSpace(identity.UID),
		AID:                strings.TrimSpace(identity.AID),
		OrganizationID:     strings.TrimSpace(identity.OrganizationID),
		OrganizationName:   strings.TrimSpace(identity.OrganizationName),
		Name:               strings.TrimSpace(identity.Name),
		UserType:           FirstNonEmptyQoder(identity.UserType, "personal_standard"),
		Site:               string(site),
		RefreshMode:        refreshMode,
		Extra:              extra,
	}
	if !expiresAt.IsZero() {
		tokenInfo.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	}
	return tokenInfo
}
func sanitizedQoderOAuthWarning(code, message string) map[string]string {
	return map[string]string{
		"code":    code,
		"message": message,
	}
}
func DefaultOAuthClientFactory(profile Profile, proxyURL string) (OAuthClientPort, error) {
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL: strings.TrimSpace(proxyURL),
		Timeout:  20 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return NewOAuthClientForProfile(profile, client), nil
}
