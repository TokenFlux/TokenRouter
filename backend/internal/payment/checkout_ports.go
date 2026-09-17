// 下单能力通过固定端口装配；请求只传业务输入，不携带数据库或旧实体。
package payment

import (
	"context"
	"time"
)

type Buyer struct {
	ID                             int64
	Email, Username, Notes, Status string
}
type CheckoutDraft struct {
	Order                   Order
	MaxPending              int
	DailyLimit, LimitAmount float64
	TimeoutMinutes          int
}
type CheckoutStore interface {
	CreateCheckout(context.Context, CheckoutDraft) (*Order, error)
	PersistCheckoutResponse(context.Context, int64, *InstanceSelection, *CreatePaymentResponse) (*Order, error)
	FailCheckout(context.Context, int64) error
	CancelledCount(context.Context, string, time.Time) (int, error)
}
type CheckoutRuntime struct {
	User             func(context.Context, int64) (*Buyer, error)
	WeChatCredential func(context.Context) (string, string, error)
	CreateProvider   func(string, string, map[string]string) (Provider, error)
	RememberLocale   func(context.Context, int64, string, string)
	Audit            func(context.Context, int64, string, string, map[string]any)
	Observe          func(context.Context) func()
	Error            func(string, ...any)
	Now              func() time.Time
}
type Checkout struct {
	store         CheckoutStore
	configService *ConfigService
	loadBalancer  LoadBalancer
	resume        *PaymentResumeService
	runtime       CheckoutRuntime
}

func NewCheckout(store CheckoutStore, cfg *ConfigService, balancer LoadBalancer, resume *PaymentResumeService, runtime CheckoutRuntime) *Checkout {
	if runtime.Error == nil {
		runtime.Error = func(string, ...any) {}
	}
	if runtime.Audit == nil {
		runtime.Audit = func(context.Context, int64, string, string, map[string]any) {}
	}
	if runtime.Observe == nil {
		runtime.Observe = func(context.Context) func() { return func() {} }
	}
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	return &Checkout{store: store, configService: cfg, loadBalancer: balancer, resume: resume, runtime: runtime}
}
