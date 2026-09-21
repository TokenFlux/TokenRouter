package account

import (
	"context"
	"strings"
	"time"

	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// OpenAI429ResetTime 从观测到的窗口选择原账号恢复时间。
// 返回 nil 表示无法从响应头中确定重置时间
func OpenAI429ResetTime(snapshot *openaiprotocol.OpenAICodexUsageSnapshot, clock func() time.Time, info func(string, ...any)) *time.Time {
	if snapshot == nil {
		return nil
	}

	normalized := snapshot.Normalize()
	if normalized == nil {
		return nil
	}

	now := clock()

	// 判断哪个限制被触发（used_percent >= 100）
	is7dExhausted := normalized.Used7dPercent != nil && *normalized.Used7dPercent >= 100
	is5hExhausted := normalized.Used5hPercent != nil && *normalized.Used5hPercent >= 100

	// 优先使用被触发限制的重置时间
	if is7dExhausted && normalized.Reset7dSeconds != nil {
		resetAt := now.Add(time.Duration(*normalized.Reset7dSeconds) * time.Second)
		info("openai_429_7d_limit_exhausted", "reset_after_seconds", *normalized.Reset7dSeconds, "reset_at", resetAt)
		return &resetAt
	}
	if is5hExhausted && normalized.Reset5hSeconds != nil {
		resetAt := now.Add(time.Duration(*normalized.Reset5hSeconds) * time.Second)
		info("openai_429_5h_limit_exhausted", "reset_after_seconds", *normalized.Reset5hSeconds, "reset_at", resetAt)
		return &resetAt
	}

	// 都未达到100%但收到429，使用较长的重置时间
	var maxResetSecs int
	if normalized.Reset7dSeconds != nil && *normalized.Reset7dSeconds > maxResetSecs {
		maxResetSecs = *normalized.Reset7dSeconds
	}
	if normalized.Reset5hSeconds != nil && *normalized.Reset5hSeconds > maxResetSecs {
		maxResetSecs = *normalized.Reset5hSeconds
	}
	if maxResetSecs > 0 {
		resetAt := now.Add(time.Duration(maxResetSecs) * time.Second)
		info("openai_429_using_max_reset", "max_reset_seconds", maxResetSecs, "reset_at", resetAt)
		return &resetAt
	}

	return nil
}

// PersistOpenAIObservedPlan 将 429 错误中观测到的计划类型同步到账户凭据。
func PersistOpenAIObservedPlan(ctx context.Context, repo OpenAIPlanWriter, account *Record, planType string, info, warn func(string, ...any)) {
	if repo == nil || account == nil || account.Platform != capability.PlatformOpenAI {
		return
	}
	// spark 影子账号恒不持凭据:即便收到带 plan_type 的 429,也不能把 plan_type 写进影子 credentials
	// ——该路径走 repo.BulkUpdate 直写、不经 persistAccountCredentials 守卫(外审第7轮 P1)。
	// plan_type 由母账号在自己的请求上维护,影子跳过。
	if account.IsCredentialShadow() {
		return
	}

	if planType == "" {
		return
	}

	current := strings.TrimSpace(account.GetCredential("plan_type"))
	if strings.EqualFold(current, planType) {
		return
	}

	if _, err := repo.BulkUpdate(ctx, []int64{account.ID}, AccountBulkUpdate{
		Credentials: map[string]any{"plan_type": planType},
	}); err != nil {
		warn("openai_429_plan_type_sync_failed", "account_id", account.ID, "plan_type", planType, "error", err)
		return
	}

	if account.Credentials == nil {
		account.Credentials = make(map[string]any, 1)
	}
	account.Credentials["plan_type"] = planType
	info("openai_429_plan_type_synced", "account_id", account.ID, "previous_plan_type", current, "plan_type", planType)
}

// OpenAIPlanWriter 只允许按字段补丁写入本次观测到的套餐，不覆盖消费或其他凭据。
type OpenAIPlanWriter interface {
	BulkUpdate(context.Context, []int64, AccountBulkUpdate) (int64, error)
}
