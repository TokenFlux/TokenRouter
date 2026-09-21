// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	errors "errors"
	sync "sync"
	time "time"

	logredact "github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
)

// RefreshProviderExecution 提供已选平台的资格和单账号执行端口，不携带旧实体或具体实现。
type RefreshProviderExecution struct {
	Platform     string
	State        *RefreshProviderState
	CanRefresh   func(*Record) bool
	NeedsRefresh func(*Record, time.Duration) bool
	Execute      func(context.Context, *Record, time.Duration, *RefreshProviderState) error
}

// RefreshPageProcessor 拥有按平台分组、并发 worker、取消/隔离跳过及结果统计。
type RefreshPageProcessor struct {
	Concurrency int
	Info, Warn  func(string, ...any)
}

func (s RefreshPageProcessor) ProcessPage(
	ctx context.Context,
	accounts []Record,
	providerStates map[string]*RefreshProviderExecution,
	refreshWindow time.Duration,
) RefreshPageStats {
	stats := RefreshPageStats{Total: len(accounts)}
	groups := make(map[string][]*Record)
	for i := range accounts {
		account := &accounts[i]
		state := providerStates[account.Platform]
		if state == nil || state.CanRefresh == nil || !state.CanRefresh(account) {
			continue
		}
		stats.OAuth++
		if !state.NeedsRefresh(account, refreshWindow) {
			continue
		}
		stats.NeedsRefresh++
		groups[account.Platform] = append(groups[account.Platform], account)
	}

	type providerResult struct {
		refreshed int
		skipped   int
		failed    int
	}
	results := make(chan providerResult, len(groups))
	var wg sync.WaitGroup
	for platform, group := range groups {
		state := providerStates[platform]
		wg.Add(1)
		go func() {
			defer wg.Done()
			refreshed, skipped, failed := s.ProcessProvider(ctx, state, group, refreshWindow)
			results <- providerResult{refreshed: refreshed, skipped: skipped, failed: failed}
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		stats.Refreshed += result.refreshed
		stats.Skipped += result.skipped
		stats.Failed += result.failed
	}
	return stats
}
func (s RefreshPageProcessor) ProcessProvider(
	ctx context.Context,
	state *RefreshProviderExecution,
	accounts []*Record,
	refreshWindow time.Duration,
) (refreshed, skipped, failed int) {
	if state == nil || len(accounts) == 0 {
		return 0, 0, 0
	}
	if s.Info == nil {
		s.Info = func(string, ...any) {}
	}
	if s.Warn == nil {
		s.Warn = func(string, ...any) {}
	}
	type refreshResult struct {
		accountID int64
		err       error
	}
	jobs := make(chan *Record, len(accounts))
	results := make(chan refreshResult, len(accounts))
	workerCount := s.Concurrency
	if workerCount > len(accounts) {
		workerCount = len(accounts)
	}
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for account := range jobs {
				if ctx.Err() != nil || state.State.IsTripped() {
					results <- refreshResult{accountID: account.ID, err: ErrRefreshSkipped}
					continue
				}
				if state.State.IsTripped() {
					results <- refreshResult{accountID: account.ID, err: ErrRefreshSkipped}
					continue
				}
				err := state.Execute(ctx, account, refreshWindow, state.State)
				state.State.RecordResult(err)
				results <- refreshResult{accountID: account.ID, err: err}
			}
		}()
	}
	for _, account := range accounts {
		jobs <- account
	}
	close(jobs)
	wg.Wait()
	close(results)

	for result := range results {
		switch {
		case result.err == nil:
			refreshed++
			s.Info("token_refresh.account_refreshed", "account_id", result.accountID, "platform", state.Platform)
		case errors.Is(result.err, ErrRefreshSkipped):
			skipped++
		default:
			failed++
			s.Warn("token_refresh.account_refresh_failed", "account_id", result.accountID, "platform", state.Platform, "error", logredact.RedactText(result.err.Error()))
		}
	}
	return refreshed, skipped, failed
}
