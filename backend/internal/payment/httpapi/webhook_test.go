package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

var errWebhookOrderNotFound = errors.New("webhook order not found")

// webhookOrderStore supplies only the provider fallback and notification-order
// reads reached by these handler tests. Other promoted store operations would
// panic if callback handling unexpectedly continued into fulfillment.
type webhookOrderStore struct {
	payment.BindingStore
	payment.FulfillmentStore
	order *payment.Order
	err   error
}

// webhookConfirmer verifies one fixed success notification and records whether
// handler ordering reached upstream confirmation.
type webhookConfirmer struct {
	payment.Provider
	notification *payment.PaymentNotification
	confirmErr   error
	confirmCalls int
}

func TestEasyPayNotifyChecksLocalOrderBeforeConfirmation(t *testing.T) {
	tests := []struct {
		name         string
		order        *payment.Order
		orderErr     error
		confirmErr   error
		wantCode     int
		wantBody     string
		wantConfirms int
	}{
		{
			name:         "missing order is acknowledged",
			orderErr:     errWebhookOrderNotFound,
			confirmErr:   errors.New("confirmation must not run"),
			wantCode:     http.StatusOK,
			wantBody:     "success",
			wantConfirms: 0,
		},
		{
			name:         "database failure remains retryable",
			orderErr:     errors.New("database unavailable"),
			confirmErr:   errors.New("confirmation must not run"),
			wantCode:     http.StatusInternalServerError,
			wantBody:     "order lookup failed",
			wantConfirms: 0,
		},
		{
			name:         "existing order still requires confirmation",
			order:        &payment.Order{ID: 42, OutTradeNo: "order-42"},
			confirmErr:   errors.New("upstream unavailable"),
			wantCode:     http.StatusBadGateway,
			wantBody:     "confirm failed",
			wantConfirms: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &webhookOrderStore{order: tt.order, err: tt.orderErr}
			provider := &webhookConfirmer{
				notification: &payment.PaymentNotification{
					OrderID: "order-42",
					Status:  payment.NotificationStatusSuccess,
				},
				confirmErr: tt.confirmErr,
			}
			registry := payment.NewRegistry()
			registry.Register(provider)
			bindings := payment.NewProviderBindings(store, registry, nil, payment.BindingRuntime{}, true)
			fulfillment := payment.NewFulfillment(store, bindings, registry, nil, nil, payment.FulfillmentRuntime{})
			runtime := &payment.Runtime{
				OrderLifecycle:   payment.NewOrderLifecycle(fulfillment, nil, nil),
				ProviderBindings: bindings,
			}

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/payment/webhook/easypay", nil)

			NewPaymentWebhookHandler(runtime).EasyPayNotify(ctx)

			require.Equal(t, tt.wantCode, recorder.Code)
			require.Equal(t, tt.wantBody, recorder.Body.String())
			require.Equal(t, tt.wantConfirms, provider.confirmCalls)
		})
	}
}

func (s *webhookOrderStore) OrderByTradeNumber(context.Context, string) (*payment.Order, error) {
	return s.order, s.err
}

func (*webhookOrderStore) CountEnabledInstances(context.Context, string) (int, error) {
	return 0, nil
}

func (*webhookOrderStore) IsNotFound(err error) bool {
	return errors.Is(err, errWebhookOrderNotFound)
}

func (*webhookConfirmer) Name() string { return "webhook confirmer" }

func (*webhookConfirmer) ProviderKey() string { return payment.TypeEasyPay }

func (*webhookConfirmer) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeEasyPay}
}

func (p *webhookConfirmer) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	if p.notification == nil {
		return nil, fmt.Errorf("test notification missing")
	}
	copy := *p.notification
	return &copy, nil
}

func (p *webhookConfirmer) ConfirmNotification(context.Context, *payment.PaymentNotification) error {
	p.confirmCalls++
	return p.confirmErr
}
