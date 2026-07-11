package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
)

type qoderSessionRefresher func(ctx context.Context, refreshToken, securityOauthToken string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error)

// QoderTokenRefresher 使用 Qoder refresh_token 换取新的 COSY session。
type QoderTokenRefresher struct {
	qoderOAuthService *QoderOAuthService
	refreshSession    qoderSessionRefresher
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
	refreshToken := strings.TrimSpace(account.GetCredential("refresh_token"))
	if refreshToken == "" {
		return nil, errors.New("no refresh token available")
	}
	machineID := strings.TrimSpace(account.GetCredential("machine_id"))
	if machineID == "" {
		return nil, errors.New("qoder refresh requires machine_id")
	}
	refreshSession := r.refreshSession
	if refreshSession == nil {
		refreshSession = func(ctx context.Context, refreshToken, securityOauthToken string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
			return qoder.RefreshSessionContext(ctx, refreshToken, securityOauthToken, machine, "", newQoderRequestDoer(account, r.httpUpstream, r.tlsFPProfileSvc))
		}
	}
	machine := &qoder.MachineIdentity{
		MachineID:    machineID,
		MachineToken: firstNonEmptyQoder(account.GetCredential("machine_token"), machineID),
		MachineType:  firstNonEmptyQoder(account.GetCredential("machine_type"), "5"),
	}
	identity, err := refreshSession(ctx, refreshToken, account.GetCredential("security_oauth_token"), machine)
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
		})
	}
	newCredentials = MergeCredentials(account.Credentials, newCredentials)
	if strings.TrimSpace(stringFromCredentialValue(newCredentials["refresh_token"])) == "" {
		newCredentials["refresh_token"] = refreshToken
	}
	// 目前观测到的 Qoder refresh 响应没有可靠的新过期时间。
	// 不保留导入时的旧 expires_at，否则 NeedsRefresh 会立刻把刚刷新的账号
	// 判定为即将过期，并可能形成刷新循环。
	delete(newCredentials, "expires_at")
	return newCredentials, nil
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
