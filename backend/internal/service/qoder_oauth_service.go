// 旧入口仅转接账号授权用例；平台协议与授权状态均只有一份实现。
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

const qoderOAuthPollInterval = account.QoderOAuthPollInterval

type qoderOAuthClient = qoder.OAuthClientPort
type qoderOAuthClientFactory = qoder.OAuthClientFactory
type QoderAuthURLResult = account.QoderAuthURLResult
type QoderExchangeCodeInput = account.QoderExchangeCodeInput
type QoderTokenInfo = account.QoderTokenInfo
type QoderPollResult = account.QoderPollResult
type QoderOAuthService struct {
	Core          *accountprovider.QoderAuthorization
	sessionStore  *account.QoderAuthorizationStore[*qoder.AuthorizationFlow]
	proxyRepo     ProxyRepository
	clientFactory qoderOAuthClientFactory
}

func NewQoderOAuthService(proxyRepo ProxyRepository) *QoderOAuthService {
	s := &QoderOAuthService{proxyRepo: proxyRepo, clientFactory: qoder.DefaultOAuthClientFactory}
	s.Core = accountprovider.NewQoderAuthorization(s.resolveProxyURL, func(profile qoder.Profile, url string) (qoder.OAuthClientPort, error) {
		return s.clientFactory(profile, url)
	})
	s.sessionStore = s.Core.Core.Store
	return s
}
func (s *QoderOAuthService) GenerateAuthURL(ctx context.Context, id *int64) (*QoderAuthURLResult, error) {
	return s.GenerateAuthURLForSite(ctx, qoder.SiteGlobal, id)
}
func (s *QoderOAuthService) GenerateAuthURLForSite(ctx context.Context, site qoder.Site, id *int64) (*QoderAuthURLResult, error) {
	return s.Core.Core.GenerateAuthURLForSite(ctx, string(site), id)
}
func (s *QoderOAuthService) ExchangeCode(ctx context.Context, input *QoderExchangeCodeInput) (*QoderTokenInfo, error) {
	return s.Core.Core.ExchangeCode(ctx, input)
}
func (s *QoderOAuthService) Poll(ctx context.Context, id, state string, proxy *int64) (*QoderPollResult, error) {
	return s.Core.Core.Poll(ctx, id, state, proxy)
}
func (s *QoderOAuthService) BuildAccountCredentials(token *QoderTokenInfo) map[string]any {
	return s.Core.Core.BuildAccountCredentials(token)
}
func (s *QoderOAuthService) Start() { s.Core.Core.Start() }
func (s *QoderOAuthService) Stop()  { _ = s.StopContext(context.Background()) }
func (s *QoderOAuthService) StopContext(ctx context.Context) error {
	if s == nil || s.Core == nil {
		return nil
	}
	return s.Core.Core.StopContext(ctx)
}
func parseQoderCallback(raw string) (string, string) { return account.ParseQoderCallback(raw) }
func buildQoderTokenInfo(identity *qoder.AuthIdentity, machine *qoder.MachineIdentity, userErr, orgErr error) *QoderTokenInfo {
	return (*QoderTokenInfo)(qoder.BuildAuthorizationTokenInfo(identity, machine, userErr, orgErr))
}

func (s *QoderOAuthService) resolveProxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil {
		return "", nil
	}
	if s.proxyRepo == nil {
		return "", errors.New("proxy repository is not configured")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
	if err != nil {
		return "", fmt.Errorf("get proxy: %w", err)
	}
	if proxy == nil {
		return "", errors.New("proxy not found")
	}
	return proxy.URL(), nil
}
