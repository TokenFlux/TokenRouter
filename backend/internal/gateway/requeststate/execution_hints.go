package requeststate

import "context"

// Hint 保留未提供与显式零值的区别，值本身不持有调用方可变指针。
type Hint[T bool | int | int64] struct {
	Value T
	Set   bool
}

// ExecutionHints 是本次请求的执行参数快照；派生 attempt 不修改父请求。
type ExecutionHints struct {
	ClaudeCode                  bool
	ClaudeCodeVersion           string
	OpenAIImageGenerationIntent bool
	OpenAIImagesEndpoint        bool
	IsMaxTokensOneHaikuRequest  Hint[bool]
	ThinkingEnabled             Hint[bool]
	PrefetchedStickyAccountID   Hint[int64]
	PrefetchedStickyGroupID     Hint[int64]
	SingleAccountRetry          Hint[bool]
	AccountSwitchCount          Hint[int]
}

// 客户端和能力标记只保存入口已经作出的判断，不重新解析报文或读取设置。
func IsClaudeCodeClient(ctx context.Context) bool {
	return ExecutionHintsFromContext(ctx).ClaudeCode
}

func SetClaudeCodeClient(ctx context.Context, value bool) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.ClaudeCode = value })
}

func SetClaudeCodeVersion(ctx context.Context, value string) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.ClaudeCodeVersion = value })
}

func GetClaudeCodeVersion(ctx context.Context) string {
	return ExecutionHintsFromContext(ctx).ClaudeCodeVersion
}

func WithOpenAIImageGenerationIntent(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return updateHints(ctx, func(h *ExecutionHints) { h.OpenAIImageGenerationIntent = true })
}

func OpenAIImageGenerationIntentFromContext(ctx context.Context) bool {
	return ExecutionHintsFromContext(ctx).OpenAIImageGenerationIntent
}

func WithOpenAIImagesEndpoint(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return updateHints(ctx, func(h *ExecutionHints) { h.OpenAIImagesEndpoint = true })
}

func OpenAIImagesEndpointFromContext(ctx context.Context) bool {
	return ExecutionHintsFromContext(ctx).OpenAIImagesEndpoint
}

type executionHintsKey struct{}

// ExecutionHintsFromContext 仅用于调用边界携带快照，原生执行入口显式接收同一值。
func ExecutionHintsFromContext(ctx context.Context) ExecutionHints {
	if ctx == nil {
		return ExecutionHints{}
	}
	hints, _ := ctx.Value(executionHintsKey{}).(ExecutionHints)
	return hints
}

func WithExecutionHints(ctx context.Context, hints ExecutionHints) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, executionHintsKey{}, hints)
}

func updateHints(ctx context.Context, update func(*ExecutionHints)) context.Context {
	hints := ExecutionHintsFromContext(ctx)
	update(&hints)
	return WithExecutionHints(ctx, hints)
}

func WithIsMaxTokensOneHaikuRequest(ctx context.Context, value bool) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.IsMaxTokensOneHaikuRequest = Hint[bool]{value, true} })
}

func WithThinkingEnabled(ctx context.Context, value bool) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.ThinkingEnabled = Hint[bool]{value, true} })
}

func WithPrefetchedStickySession(ctx context.Context, accountID, groupID int64) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) {
		h.PrefetchedStickyAccountID = Hint[int64]{accountID, true}
		h.PrefetchedStickyGroupID = Hint[int64]{groupID, true}
	})
}

func WithSingleAccountRetry(ctx context.Context, value bool) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.SingleAccountRetry = Hint[bool]{value, true} })
}

func WithAccountSwitchCount(ctx context.Context, value int) context.Context {
	return updateHints(ctx, func(h *ExecutionHints) { h.AccountSwitchCount = Hint[int]{value, true} })
}

func IsMaxTokensOneHaikuRequestFromContext(ctx context.Context) (bool, bool) {
	h := ExecutionHintsFromContext(ctx).IsMaxTokensOneHaikuRequest
	return h.Value, h.Set
}

func ThinkingEnabledFromContext(ctx context.Context) (bool, bool) {
	h := ExecutionHintsFromContext(ctx).ThinkingEnabled
	return h.Value, h.Set
}

func PrefetchedStickyAccountIDFromContext(ctx context.Context) (int64, bool) {
	h := ExecutionHintsFromContext(ctx).PrefetchedStickyAccountID
	return h.Value, h.Set
}

func PrefetchedStickyGroupIDFromContext(ctx context.Context) (int64, bool) {
	h := ExecutionHintsFromContext(ctx).PrefetchedStickyGroupID
	return h.Value, h.Set
}

func SingleAccountRetryFromContext(ctx context.Context) (bool, bool) {
	h := ExecutionHintsFromContext(ctx).SingleAccountRetry
	return h.Value, h.Set
}

func AccountSwitchCountFromContext(ctx context.Context) (int, bool) {
	h := ExecutionHintsFromContext(ctx).AccountSwitchCount
	return h.Value, h.Set
}
