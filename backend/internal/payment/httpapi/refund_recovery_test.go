package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 缺失恢复资料必须在真实 HTTP envelope 中给出人工核实错误，不能被普通错误脱敏成泛化 500。
type missingRefundRecovery struct{ payment.RefundStore }

func (missingRefundRecovery) Order(context.Context, int64) (*payment.Order, error) {
	return &payment.Order{ID: 1, Status: payment.OrderStatusRefunding}, nil
}
func (missingRefundRecovery) RefundRecovery(context.Context, *payment.Order) (*payment.RefundReceipt, error) {
	return nil, payment.RefundRecoveryRequired("preparation record unavailable")
}
func TestRefundRecoveryErrorIsVisibleToAdministrator(t *testing.T) {
	runtime := &payment.Runtime{RefundWorkflow: payment.NewRefundWorkflow(missingRefundRecovery{}, payment.RefundRuntime{})}
	handler := NewAdminHandler(runtime, nil, nil, timezone.NewCalendar(time.Local))
	router := gin.New()
	router.POST("/orders/:id/refund/query", handler.QueryAndFinalizeRefund)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("POST", "/orders/1/refund/query", nil))
	require.Equal(t, 409, recorder.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "REFUND_RECOVERY_REQUIRED", body["reason"])
	require.Contains(t, body["message"], "manual verification")
}
