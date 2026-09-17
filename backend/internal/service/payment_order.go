package service

import (
	"context"
	"strings"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
)

// --- Order Creation ---

func (s *PaymentService) CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error) {
	return s.paymentCheckout().CreateOrder(ctx, req)
}

func (s *PaymentService) usesOfficialWxpayVisibleMethod(ctx context.Context) bool {
	return s.paymentCheckout().UsesOfficialWxpayVisibleMethod(ctx)
}

func shouldUseAlipayMobilePrecreate(req CreateOrderRequest, cfg *PaymentConfig, sel *payment.InstanceSelection) bool {
	return payment.ShouldUseAlipayMobilePrecreate(req, cfg, sel)
}

func sanitizeCreatePaymentResponseDetails(pr *payment.CreatePaymentResponse) {
	payment.SanitizeCreatePaymentResponseDetails(pr)
}

func buildProviderCreatePaymentRequest(req CreateOrderRequest, sel *payment.InstanceSelection, orderID, amount, subject string, expiresAt time.Time) payment.CreatePaymentRequest {
	return payment.BuildProviderCreatePaymentRequest(req, sel, orderID, amount, subject, expiresAt)
}

func (s *PaymentService) buildPaymentSubject(plan *SubscriptionPlan, limitAmount float64, cfg *PaymentConfig, sel *payment.InstanceSelection) string {
	return s.paymentCheckout().BuildPaymentSubject(plan, limitAmount, cfg, sel)
}

func (s *PaymentService) maybeBuildWeChatOAuthRequiredResponse(ctx context.Context, req CreateOrderRequest, amount float64, feeBreakdown payment.FeeBreakdown) (*CreateOrderResponse, error) {
	return s.paymentCheckout().MaybeBuildWeChatOAuthRequiredResponse(ctx, req, amount, feeBreakdown)
}

func (s *PaymentService) maybeBuildWeChatOAuthRequiredResponseForSelection(ctx context.Context, req CreateOrderRequest, amount float64, feeBreakdown payment.FeeBreakdown, sel *payment.InstanceSelection) (*CreateOrderResponse, error) {
	return s.paymentCheckout().MaybeBuildWeChatOAuthRequiredResponseForSelection(ctx, req, amount, feeBreakdown, sel)
}

func calculateCreateOrderPayAmount(limitAmount float64, methodFee payment.FeeConfig, currency string) (payment.FeeBreakdown, string, float64, error) {
	return payment.CalculateCreateOrderPayAmount(limitAmount, methodFee, currency)
}

func calculateCreateOrderPayAmountForOrderType(limitAmount float64, methodFee payment.FeeConfig, currency, orderType string, usdToCnyRate float64) (payment.FeeBreakdown, string, float64, error) {
	return payment.CalculateCreateOrderPayAmountForOrderType(limitAmount, methodFee, currency, orderType, usdToCnyRate)
}

func validateSelectedCreateOrderAmountCurrency(payAmount string, sel *payment.InstanceSelection) error {
	return payment.ValidateSelectedCreateOrderAmountCurrency(payAmount, sel)
}

func (s *PaymentService) getWeChatPaymentOAuthCredential(ctx context.Context) (string, string, error) {
	if s == nil || s.configService == nil || s.configService.settingRepo == nil {
		return "", "", infraerrors.ServiceUnavailable(
			"WECHAT_PAYMENT_MP_NOT_CONFIGURED",
			"wechat in-app payment requires a complete WeChat MP OAuth credential",
		)
	}
	cfg, err := (&SettingService{settingRepo: s.configService.settingRepo}).GetWeChatConnectOAuthConfig(ctx)
	appID := strings.TrimSpace(cfg.AppIDForMode("mp"))
	appSecret := strings.TrimSpace(cfg.AppSecretForMode("mp"))
	if err != nil || !cfg.SupportsMode("mp") || appID == "" || appSecret == "" {
		return "", "", infraerrors.ServiceUnavailable(
			"WECHAT_PAYMENT_MP_NOT_CONFIGURED",
			"wechat in-app payment requires a complete WeChat MP OAuth credential",
		)
	}
	return appID, appSecret, nil
}

func buildCreateOrderResponse(order *dbent.PaymentOrder, req CreateOrderRequest, payAmount float64, sel *payment.InstanceSelection, pr *payment.CreatePaymentResponse, resultType payment.CreatePaymentResultType) *CreateOrderResponse {
	return payment.BuildCreateOrderResponse(paymentOrderValue(order), req, payAmount, sel, pr, resultType)
}

// --- Order Queries ---

func (s *PaymentService) GetOrder(ctx context.Context, orderID, userID int64) (*dbent.PaymentOrder, error) {
	v, e := s.paymentQueries().GetOrder(ctx, orderID, userID)
	return paymentOrderEntity(v), e
}

func (s *PaymentService) GetOrderByID(ctx context.Context, orderID int64) (*dbent.PaymentOrder, error) {
	v, e := s.paymentQueries().GetOrderByID(ctx, orderID)
	return paymentOrderEntity(v), e
}

func (s *PaymentService) GetUserOrders(ctx context.Context, userID int64, p OrderListParams) ([]*dbent.PaymentOrder, int, error) {
	v, n, e := s.paymentQueries().GetUserOrders(ctx, userID, p)
	return paymentOrderEntities(v), n, e
}

func (s *PaymentService) AdminListOrders(ctx context.Context, userID int64, p OrderListParams) ([]*dbent.PaymentOrder, int, error) {
	v, n, e := s.paymentQueries().AdminListOrders(ctx, userID, p)
	return paymentOrderEntities(v), n, e
}
