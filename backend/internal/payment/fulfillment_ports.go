// 履约通过独立资金与存储端口协作，保留充值码和来源订单的原去重边界。
package payment

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

type FulfillmentLease struct{ Version time.Time }
type OrderTransition struct {
	ID                                               int64
	From                                             []string
	Version                                          *time.Time
	Status                                           string
	TradeNo                                          string
	PayAmount                                        *float64
	PaidAt, CompletedAt, FailedAt                    *time.Time
	FailedReason                                     *string
	ClearFailure                                     bool
	InvoiceID, InvoiceURL, InvoicePDF, InvoiceStatus string
}
type FulfillmentStore interface {
	LifeStore
	Order(context.Context, int64) (*Order, error)
	OrderByTradeNumber(context.Context, string) (*Order, error)
	IsNotFound(error) bool
	TransitionOrder(context.Context, OrderTransition) (int, error)
	ClaimFulfillment(context.Context, int64, time.Time, time.Time) (int, error)
	HasAudit(context.Context, int64, string) bool
	ApplyOrderRebate(context.Context, *Order, float64) error
}
type FulfillmentRedeemer interface {
	GetByCode(context.Context, string) (*billing.RedeemCode, error)
	CreateCode(context.Context, *billing.RedeemCode) error
	Redeem(context.Context, int64, string) (*billing.RedeemCode, error)
}
type FulfillmentSubscriptions interface {
	AssignOrExtendSubscription(context.Context, *billing.AssignSubscriptionInput) (*billing.UserSubscription, bool, error)
	GetActiveSubscription(context.Context, int64, int64) (*billing.UserSubscription, error)
}
type PaymentNotice struct {
	Event, RecipientEmail, RecipientName, SourceType, SourceID string
	UserID                                                     int64
	Variables                                                  map[string]string
}
type FulfillmentRuntime struct {
	Now           func() time.Time
	Audit         func(context.Context, int64, string, string, map[string]any)
	Log           func(string, string, ...any)
	Background    func(string, func())
	Notify        func(context.Context, PaymentNotice) error
	User          func(context.Context, int64) (*RefundUser, error)
	RebateEnabled func(context.Context) bool
}
type Fulfillment struct {
	store           FulfillmentStore
	bindings        *ProviderBindings
	registry        *Registry
	redeemService   FulfillmentRedeemer
	subscriptionSvc FulfillmentSubscriptions
	runtime         FulfillmentRuntime
}

func NewFulfillment(store FulfillmentStore, bindings *ProviderBindings, registry *Registry, redeem FulfillmentRedeemer, subscriptions FulfillmentSubscriptions, runtime FulfillmentRuntime) *Fulfillment {
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	if runtime.Log == nil {
		runtime.Log = func(string, string, ...any) {}
	}
	if runtime.Audit == nil {
		runtime.Audit = func(context.Context, int64, string, string, map[string]any) {}
	}
	return &Fulfillment{
		store:           store,
		bindings:        bindings,
		registry:        registry,
		redeemService:   redeem,
		subscriptionSvc: subscriptions,
		runtime:         runtime,
	}
}
