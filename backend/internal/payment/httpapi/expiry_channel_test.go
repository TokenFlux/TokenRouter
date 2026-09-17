//go:build unit

package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/stretchr/testify/require"
)

// 测试替身只提供读取数据；真实运行时、渠道绑定和 HTTP 取消链保持生产调用。
type expiryChannelOrders struct {
	payment.FulfillmentStore
	nextPhase atomic.Int32
}

func (s *expiryChannelOrders) PendingReconciliation(context.Context, time.Time, int) ([]*payment.Order, error) {
	return []*payment.Order{{ID: 1, PaymentType: payment.TypeAlipay, OutTradeNo: "s12-channel-cancel", Status: payment.OrderStatusPending}}, nil
}
func (s *expiryChannelOrders) ProcessingIDs(context.Context) ([]int64, error) {
	s.nextPhase.Add(1)
	return nil, nil
}
func (s *expiryChannelOrders) RecoverableFulfillmentIDs(context.Context, time.Time, time.Duration, time.Duration) ([]int64, error) {
	s.nextPhase.Add(1)
	return nil, nil
}
func (s *expiryChannelOrders) ExpiredPending(context.Context, time.Time) ([]*payment.Order, error) {
	s.nextPhase.Add(1)
	return nil, nil
}

type expiryChannelInstances struct{ payment.BindingStore }

func (expiryChannelInstances) ListInstances(context.Context, payment.InstanceFilter) ([]*payment.ProviderInstance, error) {
	return nil, nil
}
func (expiryChannelInstances) CountEnabledInstances(context.Context, string) (int, error) {
	return 0, nil
}

type expiryHTTPProvider struct {
	payment.Provider
	client *http.Client
	url    string
}

func (expiryHTTPProvider) ProviderKey() string { return payment.TypeAlipay }
func (expiryHTTPProvider) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeAlipay}
}
func (p expiryHTTPProvider) QueryOrder(ctx context.Context, _ string) (*payment.QueryOrderResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return nil, err
	}
	response, err := p.client.Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	return nil, err
}
func TestPaymentExpiryStopCancelsChannelAndPreventsNextPhase(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(cancelled) }))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 2 * time.Second
	registry := payment.NewRegistry()
	registry.Register(expiryHTTPProvider{client: client, url: server.URL})
	bindings := payment.NewProviderBindings(expiryChannelInstances{}, registry, nil, payment.BindingRuntime{}, true)
	store := &expiryChannelOrders{}
	fulfillment := payment.NewFulfillment(store, bindings, registry, nil, nil, payment.FulfillmentRuntime{})
	orders := payment.NewOrderLifecycle(fulfillment, nil, nil)
	worker := payment.NewOrderExpiry(orders, time.Hour, payment.ExpiryRuntime{})
	runCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	worker.Start(runCtx)
	select {
	case <-entered:
	case <-runCtx.Done():
		t.Fatal("后台未进入本地渠道")
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	require.NoError(t, worker.StopContext(stopCtx))
	select {
	case <-cancelled:
	case <-stopCtx.Done():
		t.Fatal("停止没有取消渠道 HTTP 请求")
	}
	require.Zero(t, store.nextPhase.Load())
}
