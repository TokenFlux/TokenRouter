// 旧 OpenAI HTTP 构造器只投影旧消费者；生产直接由 app 绑定账号 Adapter。
package admin

import (
	"context"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type OpenAIOAuthHandler = accounthttp.OpenAIOAuthHandler

func NewOpenAIOAuthHandler(auth *service.OpenAIOAuthService, admin service.AdminService, quota *service.OpenAIQuotaService, recovery *service.RateLimitService) *OpenAIOAuthHandler {
	var authCore *accountcore.OpenAIAuthorization
	if auth != nil {
		authCore = auth.Core()
	}
	var quotaSource accounthttp.OpenAIQuotaService
	if quota != nil {
		quotaSource = quota.Core()
	}
	var recoverySource accounthttp.OpenAIAccountStateRecoverer
	if recovery != nil {
		recoverySource = recovery
	}
	return accounthttp.NewOpenAIOAuthHandler(authCore, legacyOpenAIOAuthAdmin{source: admin}, quotaSource, recoverySource, accounthttp.OpenAIHTTPOptions{
		ClientID: openai.OAuthClientConfigByPlatform,
		ProxyURL: func(ctx context.Context, id int64) (string, bool, error) {
			proxy, err := admin.GetProxy(ctx, id)
			if err != nil || proxy == nil {
				return "", false, err
			}
			return proxy.URL(), true, nil
		},
	})
}

type legacyOpenAIOAuthAdmin struct{ source service.AdminService }

func (s legacyOpenAIOAuthAdmin) GetAccount(ctx context.Context, id int64) (*accountcore.Record, error) {
	value, err := s.source.GetAccount(ctx, id)
	return service.AccountRecordView(value), err
}
func (s legacyOpenAIOAuthAdmin) CreateAccount(ctx context.Context, input *accountcore.CreateAccountInput) (*accountcore.Record, error) {
	value, err := s.source.CreateAccount(ctx, input)
	return service.AccountRecordView(value), err
}
func (s legacyOpenAIOAuthAdmin) UpdateAccount(ctx context.Context, id int64, input *accountcore.UpdateAccountInput) (*accountcore.Record, error) {
	value, err := s.source.UpdateAccount(ctx, id, input)
	return service.AccountRecordView(value), err
}
func (s legacyOpenAIOAuthAdmin) CreateShadow(ctx context.Context, id int64, input accountcore.ShadowOptions) (*accountcore.Record, error) {
	value, err := s.source.CreateShadow(ctx, id, input)
	return service.AccountRecordView(value), err
}

type OpenAIGenerateAuthURLRequest = accounthttp.OpenAIGenerateAuthURLRequest
type OpenAIExchangeCodeRequest = accounthttp.OpenAIExchangeCodeRequest
type OpenAIRefreshTokenRequest = accounthttp.OpenAIRefreshTokenRequest
type OpenAICodexPATCreateRequest = accounthttp.OpenAICodexPATCreateRequest

type CreateShadowRequest = accounthttp.CreateShadowRequest
