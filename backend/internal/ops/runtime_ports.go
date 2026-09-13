// 运行端口提供技术动作；锁名称、降级和通知顺序仍由各用例决定。
package ops

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
)

type RuntimeCache interface {
	Claim(context.Context, string, string, time.Duration) (bool, error)
	Release(context.Context, string, string) error
	Get(context.Context, string) (string, error)
	Put(context.Context, string, string, time.Duration) error
}
type AdvisoryLocker interface {
	Acquire(context.Context, string) (func(), bool)
}
type PreAggregationRuntimeReader interface {
	OpsEnabled(context.Context) bool
	RegisterListener(func(preaggregation.PreAggregationSettings, preaggregation.PreAggregationSettings))
}
type ProxyStatsReader interface {
	CountExpired(context.Context) (int64, error)
	CountExpiringSoon(context.Context, time.Time) (int64, error)
}
type AdminReader interface {
	GetFirstAdmin(context.Context) (*UserObservation, error)
}
type EmailDelivery struct {
	TemplatesEnabled       bool
	SendEmail              func(context.Context, string, string, string) error
	SendTemplate           func(context.Context, Notification) error
	ResolveRecipientLocale func(context.Context, int64, string) string
	RecipientName          func(string) string
	ShouldFallback         func(error) bool
}
type Notification struct {
	Event, Locale, RecipientEmail, RecipientName, SourceType, SourceID, ReminderKey string
	Variables, RawHTMLVariables                                                     map[string]string
}

func reportOptions(o *Options, format string, args ...any) {
	if o != nil && o.Logf != nil {
		o.Logf(format, args...)
	}
}
func acquireAdvisory(ctx context.Context, p AdvisoryLocker, key string) (func(), bool) {
	if p == nil {
		return nil, false
	}
	return p.Acquire(ctx, key)
}
