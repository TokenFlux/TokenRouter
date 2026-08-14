package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/qoder"
)

type qoderSessionRefresher func(ctx context.Context, refreshToken, securityOauthToken string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error)
type qoderCN20SessionRefresher func(ctx context.Context, refreshToken string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error)
type qoderCNCosySessionRefresher func(ctx context.Context, refreshToken, securityOauthToken, userID, organizationID string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error)

// QoderTokenRefresher 使用 Qoder refresh_token 换取新的 COSY session。
type QoderTokenRefresher struct {
	qoderOAuthService *QoderOAuthService
	refreshSession    qoderSessionRefresher
	refreshCN20       qoderCN20SessionRefresher
	refreshCNCosy     qoderCNCosySessionRefresher
	httpUpstream      HTTPUpstream
	tlsFPProfileSvc   *TLSFingerprintProfileService
}

func NewQoderTokenRefresher(qoderOAuthService *QoderOAuthService) *QoderTokenRefresher {
	return &QoderTokenRefresher{
		qoderOAuthService: qoderOAuthService,
	}
}

func NewQoderTokenRefresherWithHTTPUpstream(qoderOAuthService *QoderOAuthService, httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService) *QoderTokenRefresher {
	refresher := NewQoderTokenRefresher(qoderOAuthService)
	refresher.httpUpstream = httpUpstream
	refresher.tlsFPProfileSvc = tlsFPProfileService
	return refresher
}

type qoderAdminRefreshTransportProvider interface {
	qoderRefreshHTTPUpstream() HTTPUpstream
	qoderRefreshTLSFingerprintService() *TLSFingerprintProfileService
}

func NewQoderTokenRefresherForAdmin(adminService AdminService, qoderOAuthService *QoderOAuthService) *QoderTokenRefresher {
	if provider, ok := adminService.(qoderAdminRefreshTransportProvider); ok {
		return NewQoderTokenRefresherWithHTTPUpstream(
			qoderOAuthService,
			provider.qoderRefreshHTTPUpstream(),
			provider.qoderRefreshTLSFingerprintService(),
		)
	}
	return NewQoderTokenRefresher(qoderOAuthService)
}

func (r *QoderTokenRefresher) CacheKey(account *Account) string {
	return QoderTokenCacheKey(account)
}

func (r *QoderTokenRefresher) CanRefresh(account *Account) bool {
	return account != nil && account.Platform == PlatformQoder && account.Type == AccountTypeCosy
}

func (r *QoderTokenRefresher) NeedsRefresh(account *Account, _ time.Duration) bool {
	if !r.CanRefresh(account) {
		return false
	}
	if strings.TrimSpace(account.GetCredential("pat")) != "" {
		return false
	}
	if strings.TrimSpace(account.GetCredential("refresh_token")) == "" {
		return false
	}
	if expiresAt := account.GetCredentialAsTime("expires_at"); expiresAt != nil {
		return time.Until(*expiresAt) < 15*time.Minute
	}
	// Qoder 的 RateLimitResetAt 也承载 upstream 月度 quota / agent-limit 调度信号，
	// 不能像 OpenAI 一样把通用限流状态当成后台 token refresh 触发器。
	return false
}

func (r *QoderTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	if !r.CanRefresh(account) {
		return nil, errors.New("not a qoder cosy account")
	}
	site, err := qoderSiteForAccount(account)
	if err != nil {
		return nil, err
	}
	refreshMode, err := qoderRefreshModeForAccount(account)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(account.GetCredential("pat")) != "" {
		refreshMode = qoder.RefreshModeCosy
	}
	machineID := strings.TrimSpace(account.GetCredential("machine_id"))
	if machineID == "" && strings.TrimSpace(account.GetCredential("pat")) == "" {
		return nil, errors.New("qoder refresh requires machine_id")
	}
	machine := qoderMachineForAccount(account)
	doer := newQoderRequestDoer(account, r.httpUpstream, r.tlsFPProfileSvc)
	identity, expiresAt, err := r.refreshIdentity(ctx, account, site, refreshMode, machine, doer)
	if err != nil {
		return nil, fmt.Errorf("qoder refresh token: %w", err)
	}
	if identity == nil {
		return nil, errors.New("qoder refresh returned empty identity")
	}
	if strings.TrimSpace(identity.SecurityOauthToken) == "" {
		return nil, errors.New("qoder refresh returned empty security_oauth_token")
	}
	applyQoderAccountIdentityMetadata(identity, account)
	newCredentials := qoderTokenInfoCredentials(identity, account, machine)
	if r.qoderOAuthService != nil {
		newCredentials = r.qoderOAuthService.BuildAccountCredentials(&QoderTokenInfo{
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
			UserType:           firstNonEmptyQoder(identity.UserType, "personal_standard"),
			Site:               string(site),
			RefreshMode:        refreshMode,
		})
	}
	newCredentials = MergeCredentials(account.Credentials, newCredentials)
	newCredentials["site"] = string(site)
	newCredentials["refresh_mode"] = refreshMode
	if site == qoder.SiteCN {
		// 合并旧凭据后再次清理，避免刷新把历史随机机器字段带回国内账号。
		delete(newCredentials, "machine_token")
		delete(newCredentials, "machine_type")
	}
	if !expiresAt.IsZero() {
		newCredentials["expires_at"] = expiresAt.UTC().Format(time.RFC3339)
	}
	refreshToken := strings.TrimSpace(account.GetCredential("refresh_token"))
	if strings.TrimSpace(stringFromCredentialValue(newCredentials["refresh_token"])) == "" {
		if refreshToken != "" {
			newCredentials["refresh_token"] = refreshToken
		}
	}
	// 目前观测到的 Qoder refresh 响应没有可靠的新过期时间。
	// 不保留导入时的旧 expires_at，否则 NeedsRefresh 会立刻把刚刷新的账号
	// 判定为即将过期，并可能形成刷新循环。
	if expiresAt.IsZero() {
		delete(newCredentials, "expires_at")
	}
	return newCredentials, nil
}

func (r *QoderTokenRefresher) refreshIdentity(
	ctx context.Context,
	account *Account,
	site qoder.Site,
	refreshMode string,
	machine *qoder.MachineIdentity,
	doer qoder.RequestDoer,
) (*qoder.AuthIdentity, time.Time, error) {
	pat := strings.TrimSpace(account.GetCredential("pat"))
	if pat != "" {
		if site == qoder.SiteCN {
			profile := qoder.MustProfileForSite(qoder.SiteCN)
			identity, expiresAt, err := qoder.ExchangeQoderCN20PATContext(ctx, pat, machine, profile, doer)
			return identity, expiresAt, err
		}
		identity, err := qoder.ExchangePATContext(ctx, pat, machine, "", doer)
		return identity, time.Time{}, err
	}

	refreshToken := strings.TrimSpace(account.GetCredential("refresh_token"))
	if refreshToken == "" {
		return nil, time.Time{}, errors.New("no refresh token available")
	}
	securityToken := strings.TrimSpace(account.GetCredential("security_oauth_token"))
	if refreshMode == qoder.RefreshModeQoderCN20 {
		if site != qoder.SiteCN {
			return nil, time.Time{}, errors.New("qoder qodercn20 refresh requires cn site")
		}
		if r.refreshCN20 != nil {
			return r.refreshCN20(ctx, refreshToken, machine)
		}
		return qoder.RefreshQoderCN20SessionContext(ctx, refreshToken, machine, qoder.MustProfileForSite(site), doer)
	}
	if site == qoder.SiteCN {
		userID := firstNonEmptyQoder(account.GetCredential("uid"), account.GetCredential("aid"))
		organizationID := account.GetCredential("organization_id")
		if r.refreshCNCosy != nil {
			identity, err := r.refreshCNCosy(ctx, refreshToken, securityToken, userID, organizationID, machine)
			return identity, time.Time{}, err
		}
		identity, err := qoder.RefreshCosySessionForProfileContext(ctx, qoder.MustProfileForSite(site), refreshToken, securityToken, userID, organizationID, machine, doer)
		return identity, time.Time{}, err
	}
	refreshSession := r.refreshSession
	if refreshSession == nil {
		refreshSession = func(ctx context.Context, refreshToken, securityOauthToken string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
			return qoder.RefreshSessionContext(ctx, refreshToken, securityOauthToken, machine, "", doer)
		}
	}
	identity, err := refreshSession(ctx, refreshToken, securityToken, machine)
	return identity, time.Time{}, err
}

func QoderTokenCacheKey(account *Account) string {
	if account == nil {
		return "qoder:account:0"
	}
	return "qoder:account:" + strconv.FormatInt(account.ID, 10)
}

func qoderTokenInfoCredentials(identity *qoder.AuthIdentity, account *Account, machine *qoder.MachineIdentity) map[string]any {
	credentials := map[string]any{}
	if identity != nil {
		if token := strings.TrimSpace(identity.SecurityOauthToken); token != "" {
			credentials["security_oauth_token"] = token
		}
		if refreshToken := strings.TrimSpace(identity.RefreshToken); refreshToken != "" {
			credentials["refresh_token"] = refreshToken
		}
		if uid := strings.TrimSpace(identity.UID); uid != "" {
			credentials["uid"] = uid
		}
		if aid := strings.TrimSpace(identity.AID); aid != "" {
			credentials["aid"] = aid
		}
		if orgID := strings.TrimSpace(identity.OrganizationID); orgID != "" {
			credentials["organization_id"] = orgID
		}
		if orgName := strings.TrimSpace(identity.OrganizationName); orgName != "" {
			credentials["organization_name"] = orgName
		}
		if name := strings.TrimSpace(identity.Name); name != "" {
			credentials["name"] = name
		}
		if userType := strings.TrimSpace(identity.UserType); userType != "" {
			credentials["user_type"] = userType
		}
	}
	if machine != nil {
		if machineID := strings.TrimSpace(machine.MachineID); machineID != "" {
			credentials["machine_id"] = machineID
		}
		if machineToken := strings.TrimSpace(machine.MachineToken); machineToken != "" {
			credentials["machine_token"] = machineToken
		}
		if machineType := strings.TrimSpace(machine.MachineType); machineType != "" {
			credentials["machine_type"] = machineType
		}
	}
	if account != nil {
		if refreshToken := account.GetCredential("refresh_token"); refreshToken != "" {
			if _, ok := credentials["refresh_token"]; !ok {
				credentials["refresh_token"] = refreshToken
			}
		}
	}
	return credentials
}

func stringFromCredentialValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}
