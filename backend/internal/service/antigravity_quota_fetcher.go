// 旧额度入口只投影配置、代理和账号；query singleflight 仍使用原账号实例。
package service

import (
	"context"
	"errors"
	"log/slog"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

type AntigravityQuotaFetcher struct{ core acctcore.AntigravityQuota }

func NewAntigravityQuotaFetcher(proxyRepo ProxyRepository, cfg *config.Config) *AntigravityQuotaFetcher {
	return &AntigravityQuotaFetcher{core: acctcore.AntigravityQuota{Options: acctcore.AntigravityQuotaOptions{
		NewClient: func(proxy string) (acctcore.AntigravityQuotaClient, error) { return antigravity.NewClient(proxy) }, ReadLimit: func() int64 { return resolveModelsListReadLimit(cfg) }, ResolveProxy: func(ctx context.Context, id int64) (string, bool) {
			if proxyRepo == nil {
				return "", false
			}
			proxy, err := proxyRepo.GetByID(ctx, id)
			if err != nil || proxy == nil {
				return "", false
			}
			return proxy.URL(), true
		}, ForbiddenBody: func(err error) (string, bool) {
			var value *antigravity.ForbiddenError
			if errors.As(err, &value) {
				return value.Body, true
			}
			return "", false
		}, ClassifyForbidden: antigravity.ClassifyForbiddenType, ValidationURL: antigravity.ExtractValidationURL, Warn: slog.Warn,
	}}}
}
func (f *AntigravityQuotaFetcher) CanFetch(a *Account) bool {
	return f.core.CanFetch(AccountRecordView(a))
}
func (f *AntigravityQuotaFetcher) FetchQuota(ctx context.Context, a *Account, proxy string) (*QuotaResult, error) {
	return f.core.FetchQuota(ctx, AccountRecordView(a), proxy)
}
func (f *AntigravityQuotaFetcher) GetProxyURL(ctx context.Context, a *Account) string {
	return f.core.GetProxyURL(ctx, AccountRecordView(a))
}

func classifyForbiddenType(body string) string { return antigravity.ClassifyForbiddenType(body) }
func extractValidationURL(body string) string  { return antigravity.ExtractValidationURL(body) }

const forbiddenTypeValidation = acctcore.ForbiddenTypeValidation

const forbiddenTypeViolation = acctcore.ForbiddenTypeViolation

const errorCodeForbidden = acctcore.ErrorCodeForbidden

const errorCodeUnauthenticated = acctcore.ErrorCodeUnauthenticated

const errorCodeRateLimited = acctcore.ErrorCodeRateLimited

const errorCodeNetworkError = acctcore.ErrorCodeNetworkError
