// 下单编排保持原选择、币种、发票、OAuth 与渠道调用顺序。
package payment

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/shopspring/decimal"
)

func (s *Checkout) CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error) {
	if req.OrderType == "" {
		req.OrderType = OrderTypeBalance
	}
	if normalized := NormalizeVisibleMethod(req.PaymentType); normalized != "" {
		req.PaymentType = normalized
	}
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get payment config: %w", err)
	}
	if !cfg.Enabled {
		return nil, infraerrors.Forbidden("PAYMENT_DISABLED", "payment system is disabled")
	}
	plan, err := s.ValidateOrderInput(ctx, req, cfg)
	if err != nil {
		return nil, err
	}
	if err := s.CheckCancelRateLimit(ctx, req.UserID, cfg); err != nil {
		return nil, err
	}
	user, err := s.runtime.User(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if user.Status != EntityStatusActive {
		return nil, infraerrors.Forbidden("USER_INACTIVE", "user account is disabled")
	}
	if s.runtime.RememberLocale != nil {
		s.runtime.RememberLocale(ctx, req.UserID, user.Email, req.Locale)
	}
	orderAmount := req.Amount
	limitAmount := req.Amount
	if plan != nil {
		orderAmount = plan.Price
		limitAmount = plan.Price
	} else if req.OrderType == OrderTypeBalance {
		orderAmount = CalculateCreditedBalance(req.Amount, cfg.BalanceRechargeMultiplier)
	}
	methodFee := cfg.EffectiveMethodFee(req.PaymentType)
	methodCurrency := DefaultPaymentCurrency
	if s.configService != nil {
		methodCurrency, err = s.configService.ValidateMethodCurrencyConsistency(ctx, req.PaymentType)
		if err != nil {
			return nil, err
		}
	}
	// 订阅订单先换算网关扣款基数，再统一应用 fork 的固定费与比例费模型。
	feeBreakdown, payAmountStr, payAmount, err := CalculateCreateOrderPayAmountForOrderType(limitAmount, methodFee, methodCurrency, req.OrderType, cfg.SubscriptionUSDToCNYRate)
	if err != nil {
		return nil, err
	}
	sel, err := s.SelectCreateOrderInstance(ctx, req, cfg, payAmount)
	if err != nil {
		return nil, err
	}
	if err := s.ValidateSelectedCreateOrderInstance(ctx, req, sel); err != nil {
		return nil, err
	}
	selectedCurrency := DefaultPaymentCurrency
	if sel != nil {
		selectedCurrency = ProviderConfigCurrency(sel.ProviderKey, sel.Config)
	}
	if selectedCurrency != methodCurrency {
		feeBreakdown, payAmountStr, payAmount, err = CalculateCreateOrderPayAmountForOrderType(limitAmount, methodFee, selectedCurrency, req.OrderType, cfg.SubscriptionUSDToCNYRate)
		if err != nil {
			return nil, err
		}
	}
	if err := ValidateSelectedCreateOrderAmountCurrency(payAmountStr, sel); err != nil {
		return nil, err
	}
	if sel.ProviderKey == TypeStripe {
		req.BillingInfo = TrimBillingInfo(req.BillingInfo)
		if err := ValidateBillingInfo(req.BillingInfo, user.Email); err != nil {
			return nil, err
		}
		if req.BillingInfo.Email == "" {
			req.BillingInfo.Email = strings.TrimSpace(user.Email)
		}
	} else {
		req.BillingInfo = nil
	}
	oauthResp, err := s.MaybeBuildWeChatOAuthRequiredResponseForSelection(ctx, req, limitAmount, feeBreakdown, sel)
	if err != nil {
		return nil, err
	}
	if oauthResp != nil {
		return oauthResp, nil
	}
	order, err := s.CreateOrderInTx(ctx, req, user, plan, cfg, orderAmount, limitAmount, feeBreakdown, sel)
	if err != nil {
		return nil, err
	}
	resp, err := s.InvokeProvider(ctx, order, req, cfg, limitAmount, payAmountStr, payAmount, plan, sel)
	if err != nil {
		_ = s.store.FailCheckout(ctx, order.ID)
		return nil, err
	}
	return resp, nil
}
func (s *Checkout) ValidateOrderInput(ctx context.Context, req CreateOrderRequest, cfg *PaymentConfig) (*SubscriptionPlan, error) {
	if req.OrderType == OrderTypeBalance && cfg.BalanceDisabled {
		return nil, infraerrors.Forbidden("BALANCE_PAYMENT_DISABLED", "balance recharge has been disabled")
	}
	if req.OrderType == OrderTypeSubscription {
		return s.ValidateSubOrder(ctx, req)
	}
	if math.IsNaN(req.Amount) || math.IsInf(req.Amount, 0) || req.Amount <= 0 {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "amount must be a positive number")
	}
	if (cfg.MinAmount > 0 && req.Amount < cfg.MinAmount) || (cfg.MaxAmount > 0 && req.Amount > cfg.MaxAmount) {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "amount out of range").
			WithMetadata(map[string]string{"min": fmt.Sprintf("%.2f", cfg.MinAmount), "max": fmt.Sprintf("%.2f", cfg.MaxAmount)})
	}
	return nil, nil
}
func (s *Checkout) ValidateSubOrder(ctx context.Context, req CreateOrderRequest) (*SubscriptionPlan, error) {
	if req.PlanID == 0 {
		return nil, infraerrors.BadRequest("INVALID_INPUT", "subscription order requires a plan")
	}
	plan, err := s.configService.GetByID(ctx, req.PlanID)
	if err != nil || !plan.ForSale {
		return nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
	}
	return plan, nil
}
func (s *Checkout) CreateOrderInTx(ctx context.Context, req CreateOrderRequest, user *Buyer, plan *SubscriptionPlan, cfg *PaymentConfig, orderAmount, limitAmount float64, fee FeeBreakdown, sel *InstanceSelection) (*Order, error) {
	return s.store.CreateCheckout(ctx, BuildCheckoutDraft(req, user, plan, cfg, orderAmount, limitAmount, fee, sel))
}
func BuildPaymentOrderProviderSnapshot(sel *InstanceSelection, req CreateOrderRequest) map[string]any {
	if sel == nil {
		return nil
	}

	snapshot := map[string]any{}
	snapshot["schema_version"] = 2

	instanceID := strings.TrimSpace(sel.InstanceID)
	if instanceID != "" {
		snapshot["provider_instance_id"] = instanceID
	}

	providerKey := strings.TrimSpace(sel.ProviderKey)
	if providerKey != "" {
		snapshot["provider_key"] = providerKey
	}

	paymentMode := strings.TrimSpace(sel.PaymentMode)
	if paymentMode != "" {
		snapshot["payment_mode"] = paymentMode
	}

	if providerKey == TypeWxpay {
		if merchantAppID := PaymentOrderSnapshotWxpayAppID(sel, req); merchantAppID != "" {
			snapshot["merchant_app_id"] = merchantAppID
		}
		if merchantID := strings.TrimSpace(sel.Config["mchId"]); merchantID != "" {
			snapshot["merchant_id"] = merchantID
		}
		snapshot["currency"] = DefaultPaymentCurrency
	}
	if providerKey == TypeAlipay {
		if merchantAppID := strings.TrimSpace(sel.Config["appId"]); merchantAppID != "" {
			snapshot["merchant_app_id"] = merchantAppID
		}
	}
	if providerKey == TypeEasyPay {
		if merchantID := strings.TrimSpace(sel.Config["pid"]); merchantID != "" {
			snapshot["merchant_id"] = merchantID
		}
	}
	if providerKey == TypeStripe {
		snapshot["currency"] = ProviderConfigCurrency(providerKey, sel.Config)
	}
	if providerKey == TypeAirwallex {
		if accountID := strings.TrimSpace(sel.Config["accountId"]); accountID != "" {
			snapshot["merchant_id"] = accountID
		}
		snapshot["currency"] = ProviderConfigCurrency(providerKey, sel.Config)
	}

	if len(snapshot) == 1 {
		return nil
	}
	return snapshot
}
func PaymentOrderSnapshotWxpayAppID(sel *InstanceSelection, req CreateOrderRequest) string {
	if sel == nil || strings.TrimSpace(sel.ProviderKey) != TypeWxpay {
		return ""
	}
	if strings.TrimSpace(req.OpenID) != "" {
		return strings.TrimSpace(ResolveWxpayJSAPIAppID(sel.Config))
	}
	return strings.TrimSpace(sel.Config["appId"])
}
func (s *Checkout) SelectCreateOrderInstance(ctx context.Context, req CreateOrderRequest, cfg *PaymentConfig, payAmount float64) (*InstanceSelection, error) {
	selectCtx, err := s.PrepareCreateOrderSelectionContext(ctx, req)
	if err != nil {
		return nil, err
	}
	sel, err := s.loadBalancer.SelectInstance(selectCtx, "", req.PaymentType, Strategy(cfg.LoadBalanceStrategy), payAmount)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_GATEWAY_ERROR", "method_not_configured").
			WithMetadata(map[string]string{"payment_type": req.PaymentType})
	}
	if sel == nil {
		return nil, infraerrors.TooManyRequests("NO_AVAILABLE_INSTANCE", "no_available_instance")
	}
	return sel, nil
}
func (s *Checkout) PrepareCreateOrderSelectionContext(ctx context.Context, req CreateOrderRequest) (context.Context, error) {
	if !RequestNeedsWeChatJSAPICompatibility(req) {
		return ctx, nil
	}
	if !s.UsesOfficialWxpayVisibleMethod(ctx) {
		return ctx, nil
	}
	expectedAppID, _, err := s.runtime.WeChatCredential(ctx)
	if err != nil {
		return nil, err
	}
	return WithWxpayJSAPIAppID(ctx, expectedAppID), nil
}
func RequestNeedsWeChatJSAPICompatibility(req CreateOrderRequest) bool {
	if GetBasePaymentType(req.PaymentType) != TypeWxpay {
		return false
	}
	return req.IsWeChatBrowser || strings.TrimSpace(req.OpenID) != ""
}
func (s *Checkout) UsesOfficialWxpayVisibleMethod(ctx context.Context) bool {
	if s == nil || s.configService == nil {
		return false
	}
	inst, err := s.configService.ConfigResolveEnabledVisibleMethodInstance(ctx, TypeWxpay)
	if err != nil {
		return false
	}
	if inst == nil {
		return false
	}
	return inst.ProviderKey == TypeWxpay
}
func (s *Checkout) InvokeProvider(ctx context.Context, order *Order, req CreateOrderRequest, cfg *PaymentConfig, limitAmount float64, payAmountStr string, payAmount float64, plan *SubscriptionPlan, sel *InstanceSelection) (*CreateOrderResponse, error) {
	prov, err := s.runtime.CreateProvider(sel.ProviderKey, sel.InstanceID, sel.Config)
	if err != nil {
		s.runtime.Error("[PaymentService] CreateProvider failed", "provider", sel.ProviderKey, "instance", sel.InstanceID, "error", err)
		// If the provider returned a structured ApplicationError (e.g. WXPAY_CONFIG_MISSING_KEY),
		// pass it through with provider context added to metadata. Otherwise wrap as PAYMENT_PROVIDER_MISCONFIGURED.
		if appErr := new(infraerrors.ApplicationError); errors.As(err, &appErr) {
			md := map[string]string{"provider": sel.ProviderKey, "instance_id": sel.InstanceID}
			for k, v := range appErr.Metadata {
				md[k] = v
			}
			return nil, appErr.WithMetadata(md)
		}
		return nil, infraerrors.ServiceUnavailable("PAYMENT_PROVIDER_MISCONFIGURED", "provider_misconfigured").
			WithMetadata(map[string]string{"provider": sel.ProviderKey, "instance_id": sel.InstanceID})
	}
	subject := s.BuildPaymentSubject(plan, limitAmount, cfg, sel)
	outTradeNo := order.OutTradeNo
	canonicalReturnURL, err := CanonicalizeReturnURL(req.ReturnURL, req.SrcHost, req.SrcURL)
	if err != nil {
		return nil, err
	}
	resumeToken := ""
	if resume := s.resume; resume != nil {
		if canonicalReturnURL != "" && resume.IsSigningConfigured() {
			resumeToken, err = resume.CreateToken(ResumeTokenClaims{
				OrderID:            order.ID,
				UserID:             order.UserID,
				ProviderInstanceID: sel.InstanceID,
				ProviderKey:        sel.ProviderKey,
				PaymentType:        req.PaymentType,
				CanonicalReturnURL: canonicalReturnURL,
			})
			if err != nil {
				return nil, fmt.Errorf("create payment resume token: %w", err)
			}
		}
	}
	providerReturnURL, err := ResumeBuildPaymentReturnURL(canonicalReturnURL, order.ID, outTradeNo, resumeToken)
	if err != nil {
		return nil, err
	}
	providerReq := BuildProviderCreatePaymentRequest(CreateOrderRequest{
		PaymentType: req.PaymentType,
		OpenID:      req.OpenID,
		ClientIP:    req.ClientIP,
		IsMobile:    req.IsMobile,
		ReturnURL:   providerReturnURL,
		BillingInfo: req.BillingInfo,
	}, sel, outTradeNo, payAmountStr, subject, order.ExpiresAt)
	providerReq.UserEmail = order.UserEmail
	providerReq.AlipayMobilePrecreate = ShouldUseAlipayMobilePrecreate(req, cfg, sel)
	finishProviderCall := s.runtime.Observe(ctx)
	pr, err := prov.CreatePayment(ctx, providerReq)
	finishProviderCall()
	if err != nil {
		s.runtime.Error("[PaymentService] CreatePayment failed", "provider", sel.ProviderKey, "instance", sel.InstanceID, "error", err)
		if appErr := new(infraerrors.ApplicationError); errors.As(err, &appErr) {
			return nil, appErr
		}
		return nil, ClassifyCreatePaymentError(req, sel.ProviderKey, err)
	}
	SanitizeCreatePaymentResponseDetails(pr)
	order, err = s.PersistCreatePaymentResponse(ctx, order.ID, sel, pr)
	if err != nil {
		return nil, err
	}
	s.runtime.Audit(ctx, order.ID, "ORDER_CREATED", fmt.Sprintf("user:%d", req.UserID), map[string]any{
		"paymentAmount":  req.Amount,
		"creditedAmount": order.Amount,
		"payAmount":      order.PayAmount,
		"paymentType":    req.PaymentType,
		"orderType":      req.OrderType,
		"paymentSource":  NormalizePaymentSource(req.PaymentSource),
	})
	resultType := pr.ResultType
	if resultType == "" {
		resultType = CreatePaymentResultOrderCreated
	}
	resp := BuildCreateOrderResponse(order, req, payAmount, sel, pr, resultType)
	resp.ResumeToken = resumeToken
	resp.AlipayMobilePrecreateDeepLink = providerReq.AlipayMobilePrecreate && strings.TrimSpace(pr.QRCode) != ""
	return resp, nil
}
func (s *Checkout) PersistCreatePaymentResponse(ctx context.Context, id int64, sel *InstanceSelection, response *CreatePaymentResponse) (*Order, error) {
	return s.store.PersistCheckoutResponse(ctx, id, sel, response)
}

// ShouldUseAlipayMobilePrecreate 判断当前订单是否应使用官方支付宝移动端当面付。
func ShouldUseAlipayMobilePrecreate(req CreateOrderRequest, cfg *PaymentConfig, sel *InstanceSelection) bool {
	return cfg != nil &&
		cfg.AlipayMobilePrecreateDeepLink &&
		req.IsMobile &&
		sel != nil &&
		strings.EqualFold(strings.TrimSpace(sel.ProviderKey), TypeAlipay)
}

// SanitizeCreatePaymentResponseDetails 清理所有将写入 PostgreSQL text 字段的支付响应值。
func SanitizeCreatePaymentResponseDetails(pr *CreatePaymentResponse) {
	if pr == nil {
		return
	}
	pr.TradeNo = RemovePostgresTextNUL(pr.TradeNo)
	pr.PayURL = RemovePostgresTextNUL(pr.PayURL)
	pr.QRCode = RemovePostgresTextNUL(pr.QRCode)
	pr.CustomerID = RemovePostgresTextNUL(pr.CustomerID)
	pr.InvoiceID = RemovePostgresTextNUL(pr.InvoiceID)
	pr.InvoiceURL = RemovePostgresTextNUL(pr.InvoiceURL)
	pr.InvoicePDF = RemovePostgresTextNUL(pr.InvoicePDF)
	pr.InvoiceStatus = RemovePostgresTextNUL(pr.InvoiceStatus)
}
func RemovePostgresTextNUL(value string) string {
	if !strings.ContainsRune(value, 0) {
		return value
	}
	return strings.ReplaceAll(value, "\x00", "")
}
func BuildProviderCreatePaymentRequest(req CreateOrderRequest, sel *InstanceSelection, orderID, amount, subject string, expiresAt time.Time) CreatePaymentRequest {
	return CreatePaymentRequest{
		OrderID:            orderID,
		Amount:             amount,
		PaymentType:        req.PaymentType,
		Subject:            subject,
		ReturnURL:          req.ReturnURL,
		OpenID:             strings.TrimSpace(req.OpenID),
		ClientIP:           req.ClientIP,
		IsMobile:           req.IsMobile,
		InstanceSubMethods: SelectedInstanceSupportedTypes(sel),
		ExpiresAt:          expiresAt,
		BillingInfo:        req.BillingInfo,
	}
}
func SelectedInstanceSupportedTypes(sel *InstanceSelection) string {
	if sel == nil {
		return ""
	}
	return sel.SupportedTypes
}
func (s *Checkout) BuildPaymentSubject(plan *SubscriptionPlan, limitAmount float64, cfg *PaymentConfig, sel *InstanceSelection) string {
	if plan != nil {
		productName := plan.ProductName
		if productName == "" {
			productName = "Sub2API Subscription " + plan.Name
		}
		return ApplyPaymentProductNameAffix(productName, cfg)
	}
	currency := DefaultPaymentCurrency
	if sel != nil {
		currency = ProviderConfigCurrency(sel.ProviderKey, sel.Config)
	}
	amountStr := FormatAmountForCurrency(limitAmount, currency)
	if HasPaymentProductNameAffix(cfg) {
		return ApplyPaymentProductNameAffix(amountStr, cfg)
	}
	return "Sub2API " + amountStr + " " + currency
}
func HasPaymentProductNameAffix(cfg *PaymentConfig) bool {
	if cfg == nil {
		return false
	}
	pf := strings.TrimSpace(cfg.ProductNamePrefix)
	sf := strings.TrimSpace(cfg.ProductNameSuffix)
	return pf != "" || sf != ""
}
func ApplyPaymentProductNameAffix(productName string, cfg *PaymentConfig) string {
	if !HasPaymentProductNameAffix(cfg) {
		return productName
	}
	pf := strings.TrimSpace(cfg.ProductNamePrefix)
	sf := strings.TrimSpace(cfg.ProductNameSuffix)
	return strings.TrimSpace(pf + " " + productName + " " + sf)
}
func (s *Checkout) MaybeBuildWeChatOAuthRequiredResponse(ctx context.Context, req CreateOrderRequest, amount float64, feeBreakdown FeeBreakdown) (*CreateOrderResponse, error) {
	return s.MaybeBuildWeChatOAuthRequiredResponseForSelection(ctx, req, amount, feeBreakdown, nil)
}
func (s *Checkout) MaybeBuildWeChatOAuthRequiredResponseForSelection(ctx context.Context, req CreateOrderRequest, amount float64, feeBreakdown FeeBreakdown, sel *InstanceSelection) (*CreateOrderResponse, error) {
	if sel != nil && sel.ProviderKey != "" && sel.ProviderKey != TypeWxpay {
		return nil, nil
	}
	if strings.TrimSpace(req.OpenID) != "" || !req.IsWeChatBrowser || GetBasePaymentType(req.PaymentType) != TypeWxpay {
		return nil, nil
	}
	return s.BuildWeChatOAuthRequiredResponse(ctx, req, amount, feeBreakdown)
}
func (s *Checkout) BuildWeChatOAuthRequiredResponse(ctx context.Context, req CreateOrderRequest, amount float64, feeBreakdown FeeBreakdown) (*CreateOrderResponse, error) {
	appID, _, err := s.runtime.WeChatCredential(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.resume.EnsureSigningKey(); err != nil {
		return nil, err
	}

	authorizeURL, err := BuildWeChatPaymentOAuthStartURL(req, "snsapi_base")
	if err != nil {
		return nil, err
	}

	return &CreateOrderResponse{
		Amount:        amount,
		PayAmount:     feeBreakdown.PayAmount,
		FeeRate:       feeBreakdown.FeeRate,
		FeeFixed:      feeBreakdown.FixedFee,
		FeeRateAmount: feeBreakdown.FeeRateAmount,
		FeeAmount:     feeBreakdown.FeeAmount,
		ResultType:    CreatePaymentResultOAuthRequired,
		PaymentType:   req.PaymentType,
		OAuth: &WechatOAuthInfo{
			AuthorizeURL: authorizeURL,
			AppID:        appID,
			Scope:        "snsapi_base",
			RedirectURL:  "/auth/wechat/payment/callback",
		},
	}, nil
}
func (s *Checkout) ValidateSelectedCreateOrderInstance(ctx context.Context, req CreateOrderRequest, sel *InstanceSelection) error {
	if !RequiresWeChatJSAPICompatibleSelection(req, sel) {
		return nil
	}
	expectedAppID, _, err := s.runtime.WeChatCredential(ctx)
	if err != nil {
		return err
	}
	selectedAppID := ResolveWxpayJSAPIAppID(sel.Config)
	if selectedAppID == "" || selectedAppID != expectedAppID {
		return infraerrors.TooManyRequests("NO_AVAILABLE_INSTANCE", "selected payment instance is not compatible with the current WeChat OAuth app")
	}
	return nil
}
func CalculateCreateOrderPayAmount(limitAmount float64, methodFee FeeConfig, currency string) (FeeBreakdown, string, float64, error) {
	if err := ValidateCreateOrderAmountCurrency(limitAmount, currency); err != nil {
		return FeeBreakdown{}, "", 0, err
	}
	feeBreakdown := CalculatePayAmountWithFeeForCurrency(limitAmount, methodFee, currency)
	payAmountStr := feeBreakdown.PayAmountStringForCurrency(currency)
	if _, err := AmountToMinorUnit(payAmountStr, currency); err != nil {
		return FeeBreakdown{}, "", 0, infraerrors.BadRequest("INVALID_AMOUNT", err.Error()).
			WithMetadata(map[string]string{"currency": currency})
	}
	payAmount, err := strconv.ParseFloat(payAmountStr, 64)
	if err != nil {
		return FeeBreakdown{}, "", 0, infraerrors.BadRequest("INVALID_AMOUNT", "invalid payment amount").
			WithMetadata(map[string]string{"currency": currency})
	}
	return feeBreakdown, payAmountStr, payAmount, nil
}
func CalculateCreateOrderPayAmountForOrderType(limitAmount float64, methodFee FeeConfig, currency, orderType string, usdToCnyRate float64) (FeeBreakdown, string, float64, error) {
	paymentAmount := limitAmount
	if orderType == OrderTypeSubscription {
		paymentAmount = CalculateSubscriptionGatewayBaseAmount(limitAmount, usdToCnyRate, currency)
	}
	return CalculateCreateOrderPayAmount(paymentAmount, methodFee, currency)
}

// CalculateSubscriptionGatewayBaseAmount 计算订阅订单的网关扣款基数。
// 换算是显式 opt-in：仅当管理员配置了订阅汇率（rate > 0，1 USD = rate CNY）
// 且网关币种为 CNY 时，按 price × rate 换算；未配置时保持 price 直付的存量行为。
func CalculateSubscriptionGatewayBaseAmount(amount, usdToCnyRate float64, currency string) float64 {
	rate := NormalizeSubscriptionUSDToCNYRate(usdToCnyRate)
	if rate <= 0 || currency != DefaultPaymentCurrency {
		return amount
	}
	return decimal.NewFromFloat(amount).
		Mul(decimal.NewFromFloat(rate)).
		Round(int32(CurrencyMaxFractionDigits(currency))).
		InexactFloat64()
}
func ValidateCreateOrderAmountCurrency(amount float64, currency string) error {
	amountStr := strconv.FormatFloat(amount, 'f', -1, 64)
	if _, err := AmountToMinorUnit(amountStr, currency); err != nil {
		return infraerrors.BadRequest("INVALID_AMOUNT", err.Error()).
			WithMetadata(map[string]string{"currency": currency})
	}
	return nil
}
func ValidateSelectedCreateOrderAmountCurrency(payAmount string, sel *InstanceSelection) error {
	if sel == nil {
		return nil
	}
	currency := ProviderConfigCurrency(sel.ProviderKey, sel.Config)
	if _, err := AmountToMinorUnit(payAmount, currency); err != nil {
		return infraerrors.BadRequest("INVALID_AMOUNT", err.Error()).
			WithMetadata(map[string]string{"currency": currency})
	}
	return nil
}
func RequiresWeChatJSAPICompatibleSelection(req CreateOrderRequest, sel *InstanceSelection) bool {
	if sel == nil || sel.ProviderKey != TypeWxpay || GetBasePaymentType(req.PaymentType) != TypeWxpay {
		return false
	}
	return req.IsWeChatBrowser || strings.TrimSpace(req.OpenID) != ""
}
func ClassifyCreatePaymentError(req CreateOrderRequest, providerKey string, err error) error {
	if err == nil {
		return nil
	}
	if providerKey == TypeWxpay &&
		GetBasePaymentType(req.PaymentType) == TypeWxpay &&
		strings.Contains(err.Error(), "wxpay h5 payments are not authorized for this merchant") {
		return infraerrors.ServiceUnavailable(
			"WECHAT_H5_NOT_AUTHORIZED",
			"wechat h5 payment is not available for this merchant",
		).WithMetadata(map[string]string{
			"action": "open_in_wechat_or_scan_qr",
		})
	}
	return infraerrors.ServiceUnavailable("PAYMENT_GATEWAY_ERROR", fmt.Sprintf("payment gateway error: %s", err.Error()))
}
func BuildCreateOrderResponse(order *Order, req CreateOrderRequest, payAmount float64, sel *InstanceSelection, pr *CreatePaymentResponse, resultType CreatePaymentResultType) *CreateOrderResponse {
	return &CreateOrderResponse{
		OrderID:       order.ID,
		Amount:        order.Amount,
		PayAmount:     payAmount,
		FeeRate:       order.FeeRate,
		FeeFixed:      order.FeeFixed,
		FeeRateAmount: order.FeeRateAmount,
		FeeAmount:     order.FeeAmount,
		Status:        OrderStatusPending,
		ResultType:    resultType,
		PaymentType:   req.PaymentType,
		OutTradeNo:    order.OutTradeNo,
		PayURL:        pr.PayURL,
		QRCode:        pr.QRCode,
		ClientSecret:  pr.ClientSecret,
		CustomerID:    pr.CustomerID,
		InvoiceID:     pr.InvoiceID,
		InvoiceURL:    pr.InvoiceURL,
		InvoicePDF:    pr.InvoicePDF,
		InvoiceStatus: pr.InvoiceStatus,
		IntentID:      pr.IntentID,
		Currency:      pr.Currency,
		CountryCode:   pr.CountryCode,
		PaymentEnv:    pr.PaymentEnv,
		OAuth:         pr.OAuth,
		JSAPI:         pr.JSAPI,
		JSAPIPayload:  pr.JSAPI,
		ExpiresAt:     order.ExpiresAt,
		PaymentMode:   sel.PaymentMode,
	}
}
func BuildWeChatPaymentOAuthStartURL(req CreateOrderRequest, scope string) (string, error) {
	u, err := url.Parse("/api/v1/auth/oauth/wechat/payment/start")
	if err != nil {
		return "", fmt.Errorf("build wechat payment oauth start url: %w", err)
	}
	q := u.Query()
	q.Set("payment_type", strings.TrimSpace(req.PaymentType))
	if req.Amount > 0 {
		q.Set("amount", strconv.FormatFloat(req.Amount, 'f', -1, 64))
	}
	if orderType := strings.TrimSpace(req.OrderType); orderType != "" {
		q.Set("order_type", orderType)
	}
	if req.PlanID > 0 {
		q.Set("plan_id", strconv.FormatInt(req.PlanID, 10))
	}
	if scope = strings.TrimSpace(scope); scope != "" {
		q.Set("scope", scope)
	}
	if redirectTo := PaymentRedirectPathFromURL(req.SrcURL); redirectTo != "" {
		q.Set("redirect", redirectTo)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func PaymentRedirectPathFromURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "/purchase"
	}
	if strings.HasPrefix(rawURL, "/") && !strings.HasPrefix(rawURL, "//") {
		return NormalizePaymentRedirectPath(rawURL)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "/purchase"
	}
	path := strings.TrimSpace(u.EscapedPath())
	if path == "" {
		path = strings.TrimSpace(u.Path)
	}
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "/purchase"
	}
	if strings.TrimSpace(u.RawQuery) != "" {
		path += "?" + u.RawQuery
	}
	return NormalizePaymentRedirectPath(path)
}
func NormalizePaymentRedirectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/purchase"
	}
	if path == "/payment" {
		return "/purchase"
	}
	if strings.HasPrefix(path, "/payment?") {
		return "/purchase" + strings.TrimPrefix(path, "/payment")
	}
	return path
}
