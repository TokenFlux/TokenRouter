// 旧入口投影账号与依赖，不保留模型快照或任务状态。
package service

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func (s *GrokQuotaService) scheduleGrokObservedModelsSync(acc *Account) {
	if s == nil {
		return
	}
	account.ScheduleGrokObservedModels(s.grokModelsOptions(), AccountRecordView(acc))
}

func (s *GrokQuotaService) grokModelsOptions() account.GrokModelsOptions {
	o := account.GrokModelsOptions{Runtime: &s.probeRuntime, Available: func() bool { return s.accountRepo != nil }, Debug: slog.Debug, UpdateExtra: func(ctx context.Context, id int64, extra map[string]any) error {
		return s.accountRepo.UpdateExtra(ctx, id, extra)
	}, Fetch: func(ctx context.Context, record *account.Record, token string) ([]string, error) {
		return s.fetchGrokObservedModels(ctx, AccountFromRecord(record), token)
	}}
	if s.tokenProvider != nil {
		o.Token = func(ctx context.Context, record *account.Record) (string, error) {
			return s.tokenProvider.GetAccessToken(ctx, AccountFromRecord(record))
		}
	}
	return o
}
func (s *GrokQuotaService) fetchGrokObservedModels(ctx context.Context, account *Account, token string) ([]string, error) {
	baseURL := strings.TrimSpace(account.GetGrokBaseURL())
	if s.settingService != nil {
		baseURL = strings.TrimSpace(s.settingService.ResolveGrokBaseURL(ctx, account))
	}
	if baseURL == "" {
		baseURL = xai.DefaultCLIBaseURL
	}
	validator, err := grokBaseURLValidator(account, s.cfg)
	if err != nil {
		return nil, err
	}
	validatedBaseURL, err := validator(baseURL)
	if err != nil {
		return nil, err
	}

	return xai.FetchObservedModels(ctx, xai.ModelsRequest{URL: buildOpenAIModelsURL(validatedBaseURL), Token: token, UserID: account.GetCredential("sub"), Email: account.GetCredential("email"), OAuth: account.IsGrokOAuth(), ApplyOverrides: account.ApplyHeaderOverrides, Do: func(req *http.Request) (*http.Response, error) {
		proxyURL := ""
		if s.proxyRepo != nil && account.ProxyID != nil {
			if p, err := s.proxyRepo.GetByID(ctx, *account.ProxyID); err == nil && p != nil {
				proxyURL = p.URL()
			}
		}
		if s.httpUpstream == nil {
			return nil, nil
		}
		return s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	}})
}
