package service

import (
	"context"
	"errors"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
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

func (r *QoderTokenRefresher) CanRefresh(value *Account) bool {
	return accountcore.CanRefreshQoder(AccountRecordView(value))
}
func (r *QoderTokenRefresher) NeedsRefresh(value *Account, duration time.Duration) bool {
	return accountcore.NeedsRefreshQoder(AccountRecordView(value), duration)
}
func QoderTokenCacheKey(value *Account) string {
	return accountcore.QoderTokenCacheKey(AccountRecordView(value))
}

// Refresh 仅绑定平台交换与账号凭据合并，持久化继续参与已有刷新协调和 CAS。
func (r *QoderTokenRefresher) Refresh(ctx context.Context, value *Account) (map[string]any, error) {
	if !r.CanRefresh(value) {
		return nil, errors.New("not a qoder cosy account")
	}
	input := qoderCredentialInput(value)
	exchange := qoder.RefreshExchange{RefreshSession: qoder.SessionRefresher(r.refreshSession), RefreshCN20: qoder.CN20SessionRefresher(r.refreshCN20), RefreshCNCosy: qoder.CNCosySessionRefresher(r.refreshCNCosy), Doer: newQoderRequestDoer(value, r.httpUpstream, r.tlsFPProfileSvc)}
	result, err := exchange.Refresh(ctx, input)
	if err != nil {
		return nil, err
	}
	patch := qoder.TokenInfoCredentials(result.Identity, input, result.Machine)
	if r.qoderOAuthService != nil {
		token := qoder.BuildAuthorizationTokenInfoForSite(result.Identity, result.Machine, result.Site, result.Mode, time.Time{}, nil, nil)
		patch = r.qoderOAuthService.BuildAccountCredentials((*QoderTokenInfo)(token))
	}
	return accountcore.MergeQoderRefreshCredentials(value.Credentials, patch, string(result.Site), result.Mode, result.ExpiresAt), nil
}
