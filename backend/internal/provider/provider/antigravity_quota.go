package provider

import (
	"context"
	"errors"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// antigravity_quota.go supplies upstream clients, paid daily-first quota lookup,
// and forbidden-response classifiers to provider quota queries. Antigravity
// quota responses can represent verification requirements or policy violations
// as well as remaining model capacity. The options connect the provider quota
// reader to bounded upstream queries and typed forbidden errors, with platform
// parsers supplying the corresponding display category and URL.

// AntigravityQuotaOptions supplies upstream quota clients and
// forbidden-response parsing. The returned ReadLimit callback captures limit
// exactly, FetchPaidModels queries daily before prod for Pro and Ultra plans,
// and ResolveProxy retains the supplied resolver.
func AntigravityQuotaOptions(limit int64, resolveProxy func(context.Context, int64) (string, bool)) provider.AntigravityQuotaOptions {
	return provider.AntigravityQuotaOptions{
		NewClient: func(proxy string) (provider.AntigravityQuotaClient, error) { return antigravity.NewClient(proxy) },
		FetchPaidModels: func(ctx context.Context, client provider.AntigravityQuotaClient, accessToken, projectID string, bodyLimit int64) (*google.AntigravityFetchAvailableModelsResponse, map[string]any, error) {
			if agClient, ok := client.(*antigravity.Client); ok {
				return agClient.FetchAvailableModelsWithOptions(ctx, accessToken, projectID, antigravity.FetchAvailableModelsOptions{
					BodyLimit:   bodyLimit,
					PreferDaily: true,
				})
			}
			return client.FetchAvailableModels(ctx, accessToken, projectID, bodyLimit)
		},
		ReadLimit:    func() int64 { return limit },
		ResolveProxy: resolveProxy,
		ForbiddenBody: func(err error) (string, bool) {
			if forbidden, ok := errors.AsType[*antigravity.ForbiddenError](err); ok {
				return forbidden.Body, true
			}
			return "", false
		},
		ClassifyForbidden: antigravity.ClassifyForbiddenType,
		ValidationURL:     antigravity.ExtractValidationURL,
		Warn:              slog.Warn,
	}
}
