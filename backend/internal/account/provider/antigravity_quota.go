package provider

import (
	"context"
	"errors"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// AntigravityQuotaOptions 只绑定供应商交换与错误解析，配置和代理读取由组合根投影。
func AntigravityQuotaOptions(limit int64, resolveProxy func(context.Context, int64) (string, bool)) account.AntigravityQuotaOptions {
	return account.AntigravityQuotaOptions{
		NewClient:    func(proxy string) (account.AntigravityQuotaClient, error) { return antigravity.NewClient(proxy) },
		ReadLimit:    func() int64 { return limit },
		ResolveProxy: resolveProxy,
		ForbiddenBody: func(err error) (string, bool) {
			var forbidden *antigravity.ForbiddenError
			if errors.As(err, &forbidden) {
				return forbidden.Body, true
			}
			return "", false
		},
		ClassifyForbidden: antigravity.ClassifyForbiddenType,
		ValidationURL:     antigravity.ExtractValidationURL,
		Warn:              slog.Warn,
	}
}
