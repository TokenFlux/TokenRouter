package account

import "context"

// RetryCooldownStore 读取最新池模式，只写明确的临时停调字段。
type RetryCooldownStore interface {
	TemporaryFailureStore
	GetByID(context.Context, int64) (*Record, error)
}

type RetryCooldownInput struct {
	AccountID              int64
	Status                 int
	Retryable              bool
	RequestScopedTransient bool
}

type RetryCooldownOptions struct {
	Logf        func(string, ...any)
	LookupError func(int64, error)
}

// RetryCooldown 处理重试耗尽后的旧兼容冷却，不持有计数或缓存。
type RetryCooldown struct {
	store   RetryCooldownStore
	options RetryCooldownOptions
}

func NewRetryCooldown(store RetryCooldownStore, options RetryCooldownOptions) *RetryCooldown {
	if options.Logf == nil {
		options.Logf = func(string, ...any) {}
	}
	if options.LookupError == nil {
		options.LookupError = func(int64, error) {}
	}
	return &RetryCooldown{store: store, options: options}
}

func (r *RetryCooldown) Apply(ctx context.Context, input RetryCooldownInput) {
	if r == nil || r.store == nil || !input.Retryable || input.RequestScopedTransient {
		return
	}
	account, err := r.store.GetByID(ctx, input.AccountID)
	if err != nil {
		r.options.LookupError(input.AccountID, err)
		return
	}
	if account != nil && account.IsPoolMode() {
		return
	}
	switch input.Status {
	case 400:
		TempUnscheduleGoogleConfigError(ctx, r.store, input.AccountID, "[handler]", r.options.Logf)
	case 502:
		TempUnscheduleEmptyResponse(ctx, r.store, input.AccountID, "[handler]", r.options.Logf)
	}
}
