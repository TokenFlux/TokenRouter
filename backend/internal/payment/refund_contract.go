// 退款恢复事实与权益参与契约；渠道请求始终在本地事务之外。
package payment

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type RefundPlan struct {
	OrderID         int64
	Order           *Order
	RefundAmount    float64
	GatewayAmount   float64
	Reason          string
	Force           bool
	DeductBalance   bool
	DeductionType   string
	BalanceToDeduct float64
	SubDaysToDeduct int
	SubscriptionID  int64
	// OperationID 只关联本地审计，不改变渠道幂等编码。
	OperationID     string
	ChannelRefundID string
}
type RefundResult struct {
	Success         bool    `json:"success"`
	Warning         string  `json:"warning,omitempty"`
	RequireForce    bool    `json:"require_force,omitempty"`
	BalanceDeducted float64 `json:"balance_deducted,omitempty"`
	SubDaysDeducted int     `json:"subscription_days_deducted,omitempty"`
}
type RefundPendingDetail struct {
	RefundID            string  `json:"refundID"`
	DeductBalance       bool    `json:"deductBalance"`
	DeductionType       string  `json:"deductionType"`
	BalanceDeducted     float64 `json:"balanceDeducted"`
	SubDaysDeducted     int     `json:"subDaysDeducted"`
	SubscriptionID      int64   `json:"subscriptionID"`
	DeductionRollbackOK bool    `json:"deductionRollbackOK"`
	OperationID         string  `json:"operationID,omitempty"`
}

// RefundReceipt 记录准备事务实际扣减与订单操作版本；不保存渠道密钥。
type RefundReceipt struct {
	Version          int       `json:"version"`
	OperationID      string    `json:"operationID"`
	OrderID          int64     `json:"orderID"`
	OperationVersion time.Time `json:"operationVersion"`
	PreviousStatus   string    `json:"previousStatus"`
	RefundAmount     float64   `json:"refundAmount"`
	GatewayAmount    float64   `json:"gatewayAmount"`
	Reason           string    `json:"reason"`
	Force            bool      `json:"force"`
	RefundPendingDetail
}

// RefundRights 由存储参与工厂绑定到当前事务，不取得或结束事务。
type RefundRights interface {
	DeductBalance(context.Context, int64, float64) (float64, error)
	CompensateBalance(context.Context, int64, float64) error
	AdjustSubscription(context.Context, int64, int) error
	RevokeSubscription(context.Context, int64) error
}
type RefundMutation func(context.Context, RefundRights, *RefundPlan) error

// RefundStore 的命名操作明确限定原子范围，不向核心暴露事务客户端。
type RefundStore interface {
	RequestRefund(context.Context, int64, int64, float64, string, time.Time, string) (int, error)
	Order(context.Context, int64) (*Order, error)
	PrepareRefund(context.Context, *RefundPlan, RefundMutation) (*RefundReceipt, error)
	CompleteRefund(context.Context, *RefundPlan, *RefundReceipt, RefundMutation) (*RefundResult, error)
	CompensateRefund(context.Context, *RefundPlan, *RefundReceipt, string, string, RefundMutation) error
	FailPendingRefund(context.Context, *Order, RefundPendingDetail, string) (*Order, error)
	RefundRecovery(context.Context, *Order) (*RefundReceipt, error)
	PendingDetail(context.Context, int64) (RefundPendingDetail, error)
}

// RefundRuntime 由装配提供渠道身份解析、观测及必要的只读权益投影。
type RefundUser struct{ Balance float64 }
type RefundSubscription struct {
	ID     int64
	Status string
}
type RefundRuntime struct {
	User          func(context.Context, int64) (*RefundUser, error)
	Subscriptions func(context.Context, int64) ([]RefundSubscription, error)
	Instance      func(context.Context, *Order) (*ProviderInstance, error)
	Warn          func(string, ...any)
	Now           func() time.Time
	Provider      func(context.Context, *Order) (Provider, error)
	Observe       func(context.Context) func()
	Audit         func(context.Context, int64, string, string, map[string]any)
}
type RefundWorkflow struct {
	store   RefundStore
	runtime RefundRuntime
}

func NewRefundWorkflow(store RefundStore, runtime RefundRuntime) *RefundWorkflow {
	if runtime.Warn == nil {
		runtime.Warn = func(string, ...any) {}
	}
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	if runtime.Observe == nil {
		runtime.Observe = func(context.Context) func() { return func() {} }
	}
	if runtime.Audit == nil {
		runtime.Audit = func(context.Context, int64, string, string, map[string]any) {}
	}
	return &RefundWorkflow{store: store, runtime: runtime}
}
func RefundRecoveryRequired(reason string) error {
	return apperror.Conflict("REFUND_RECOVERY_REQUIRED", "refund recovery requires manual verification").WithMetadata(map[string]string{"reason": reason})
}
