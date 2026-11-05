package payment

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
)

// checkout.go validates purchases, selects provider instances, and coordinates
// local order creation with external payment initiation.
// Checkout separates credited entitlement, purchase principal and fee-bearing
// gateway money before selecting a merchant instance. Balance credit can use a
// multiplier, subscriptions use the sale plan price and optional CNY
// conversion, and channel fees follow the chosen payment currency. Official
// WeChat payer context can require a matching OAuth app before any local order
// is created.
//
// A usable selection produces an order-owned provider snapshot and a committed
// local checkout before external initiation. Return URLs are canonicalized and
// server order/session values are added for provider redirects. The later
// provider response write and best-effort failure update are separate
// persistence steps, so external progress can survive a local error. Display
// response status is initially pending, while later callback/query recovery
// uses persisted order state and historical merchant identity.

// CreateOrder validates buyer/purchase input, selects a provider instance and
// returns either an OAuth requirement or initiated payment details. It
// negotiates locale, defaults blank order type to balance and normalizes the
// visible method before reading current configuration.
//
// Credited entitlement and limit principal are calculated separately. Personal
// recharge terms override global settings only for new balance orders.
// Subscription currency conversion precedes fees, and a selected currency
// difference recalculates gateway charge after instance selection. Stripe
// retains validated billing information; other providers discard it.
// OAuth-required output returns before local creation. Otherwise the store
// commits the draft before InvokeProvider initiates payment and persists its
// response. Any invocation or response-persistence error triggers best-effort
// FailCheckout, whose PostgreSQL update is unconditional on prior status; the
// original error remains visible.
func (s *Checkout) CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResponse, error) {
	req.Locale = locale.Negotiate(req.Locale, locale.FromContext(ctx))
	ctx = locale.WithLanguage(ctx, req.Locale)
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
		rates, err := ResolveBalanceRechargeRates(cfg.BalanceRechargeMultiplier, user.BalanceRechargeMultiplier)
		if err != nil {
			return nil, err
		}
		orderAmount = CalculateCreditedBalance(req.Amount, rates.Effective)
		// Validate the rounded entitlement before creating an external payment;
		// zero or oversized credits cannot be delivered by the redemption wallet.
		if !IsValidCreditedBalance(orderAmount) {
			return nil, infraerrors.BadRequest("INVALID_CREDITED_AMOUNT", "credited balance must be at least 0.01 and less than 1000000000000")
		}
	}
	methodFee := cfg.EffectiveMethodFee(req.PaymentType)
	methodCurrency := DefaultPaymentCurrency
	if s.configService != nil {
		methodCurrency, err = s.configService.ValidateMethodCurrencyConsistency(ctx, req.PaymentType)
		if err != nil {
			return nil, err
		}
	}
	// Compute subscription gateway currency before fees while retaining
	// plan/credited Amount separately. The selector sees fee-bearing gateway
	// money, and draft daily limits still receive the original purchase
	// principal.
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
	// Selection can supply a currency differing from the earlier method
	// observation. Recalculate converted base and fees for that winner before
	// amount validation and persistence, retaining the selected account.
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
	// Commit the validated local draft before external initiation so callbacks
	// can find its merchant number. OAuth-required output has already returned
	// and leaves no order created on that path.
	order, err := s.CreateOrderInTx(ctx, req, user, plan, cfg, orderAmount, limitAmount, feeBreakdown, sel)
	if err != nil {
		return nil, err
	}
	resp, err := s.InvokeProvider(ctx, order, req, cfg, limitAmount, payAmountStr, payAmount, plan, sel)
	if err != nil {
		// Provider initiation or response persistence failed after local creation.
		// Attempt FAILED marking and discard its error, retaining the original
		// failure even when upstream payment has already progressed.
		_ = s.store.FailCheckout(ctx, order.ID)
		return nil, err
	}
	return resp, nil
}

// GetBalanceRechargeRates reads the authenticated buyer's current recharge
// terms using the caller's global configuration observation. Buyer lookup and
// invalid override errors abort the preview instead of displaying inherited terms.
func (s *Checkout) GetBalanceRechargeRates(ctx context.Context, userID int64, global float64) (BalanceRechargeRates, error) {
	user, err := s.runtime.User(ctx, userID)
	if err != nil {
		return BalanceRechargeRates{}, fmt.Errorf("get user: %w", err)
	}
	if user.Status != EntityStatusActive {
		return BalanceRechargeRates{}, infraerrors.Forbidden("USER_INACTIVE", "user account is disabled")
	}
	return ResolveBalanceRechargeRates(global, user.BalanceRechargeMultiplier)
}

// ValidateOrderInput rejects unsupported order types before selecting balance
// or subscription rules. CreateOrder normalizes omitted types to balance;
// balance purchases require finite positive principal within configured limits,
// while subscriptions delegate sale-plan lookup using nonnil configuration.
func (s *Checkout) ValidateOrderInput(ctx context.Context, req CreateOrderRequest, cfg *PaymentConfig) (*SubscriptionPlan, error) {
	if req.OrderType != OrderTypeBalance && req.OrderType != OrderTypeSubscription {
		return nil, infraerrors.BadRequest("INVALID_CHECKOUT_ORDER_TYPE", "order_type must be balance or subscription")
	}
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

// ValidateSubOrder reads a nonzero requested plan ID and returns a for-sale
// plan. A lookup error is converted to PLAN_NOT_AVAILABLE; the configuration
// dependency must return a nonnil plan on nil error, and a negative nonzero ID
// is delegated to lookup.
func (s *Checkout) ValidateSubOrder(ctx context.Context, req CreateOrderRequest) (*SubscriptionPlan, error) {
	if req.PlanID == 0 {
		return nil, infraerrors.BadRequest("INVALID_INPUT", "subscription order requires a plan")
	}
	plan, err := s.configService.GetByID(ctx, req.PlanID)
	// Plan lookup failure is exposed as unavailable sale data rather than a
	// storage-specific error. Successful dependency reads supply a nonnil plan
	// before its ForSale flag is inspected.
	if err != nil || !plan.ForSale {
		return nil, infraerrors.NotFound("PLAN_NOT_AVAILABLE", "plan not found or not for sale")
	}
	return plan, nil
}

// CreateOrderInTx builds a checkout draft and delegates its owned creation
// transaction to the store. The caller supplies validated buyer,
// plan/configuration, selected merchant and separate entitlement/principal/fee
// values before later provider initiation.
func (s *Checkout) CreateOrderInTx(ctx context.Context, req CreateOrderRequest, user *Buyer, plan *SubscriptionPlan, cfg *PaymentConfig, orderAmount, limitAmount float64, fee FeeBreakdown, sel *InstanceSelection) (*Order, error) {
	return s.store.CreateCheckout(ctx, BuildCheckoutDraft(req, user, plan, cfg, orderAmount, limitAmount, fee, sel))
}

// BuildPaymentOrderProviderSnapshot records version-two selected instance and
// merchant identity for later query/callback/refund association. Nil selection
// returns nil; nonblank trimmed instance/key/mode and recognized
// merchant/currency values are added to a newly allocated map.
//
// Official WeChat app identity follows the payer/OpenID mode, Alipay stores
// appId, EasyPay stores pid, and Airwallex stores accountId. Stripe/Airwallex
// currency comes from selected configuration, while WeChat records CNY. Secret
// credentials remain outside this snapshot and are read from current instance
// configuration during provider construction. A map containing only
// schema_version is discarded as no usable binding data.
func BuildPaymentOrderProviderSnapshot(sel *InstanceSelection, req CreateOrderRequest) map[string]any {
	if sel == nil {
		return nil
	}

	snapshot := map[string]any{}
	// Version two distinguishes this merchant identity snapshot schema.
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

// PaymentOrderSnapshotWxpayAppID returns the selected official WeChat app
// identity for snapshot validation. Nonblank payer OpenID selects the JSAPI
// mpAppId/appId precedence; otherwise merchant appId is used. Nil or
// non-WeChat selection returns empty.
func PaymentOrderSnapshotWxpayAppID(sel *InstanceSelection, req CreateOrderRequest) string {
	if sel == nil || strings.TrimSpace(sel.ProviderKey) != TypeWxpay {
		return ""
	}
	if strings.TrimSpace(req.OpenID) != "" {
		return strings.TrimSpace(ResolveWxpayJSAPIAppID(sel.Config))
	}
	return strings.TrimSpace(sel.Config["appId"])
}

// SelectCreateOrderInstance attaches applicable JSAPI context and requests the
// configured strategy for the selected method/amount with an empty
// provider-key filter. Selector errors become PAYMENT_GATEWAY_ERROR, while a
// nil result becomes NO_AVAILABLE_INSTANCE; usable selection returns before
// checkout persistence.
func (s *Checkout) SelectCreateOrderInstance(ctx context.Context, req CreateOrderRequest, cfg *PaymentConfig, payAmount float64) (*InstanceSelection, error) {
	selectCtx, err := s.PrepareCreateOrderSelectionContext(ctx, req)
	if err != nil {
		return nil, err
	}
	sel, err := s.loadBalancer.SelectInstance(selectCtx, "", req.PaymentType, Strategy(cfg.LoadBalanceStrategy), payAmount)
	// Selector failures become method-not-configured gateway errors for checkout
	// clients. Keep method metadata while the original selection diagnostic
	// remains with the selector's observer.
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_GATEWAY_ERROR", "method_not_configured").
			WithMetadata(map[string]string{"payment_type": req.PaymentType})
	}
	if sel == nil {
		return nil, infraerrors.TooManyRequests("NO_AVAILABLE_INSTANCE", "no_available_instance")
	}
	return sel, nil
}

// PrepareCreateOrderSelectionContext adds the expected official JSAPI app
// identity only for WeChat-browser or OpenID-bearing WeChat requests whose
// current visible source resolves official. Other cases return the original
// context.
//
// Credential lookup errors remain visible before selection, while
// source-resolution errors are suppressed by UsesOfficialWxpayVisibleMethod
// into a false decision. The added context makes DefaultLoadBalancer compare
// official candidate configuration against the expected app;
// ValidateSelectedCreateOrderInstance later checks the actual selected
// merchant again. These are separate reads, so selection preserves the app
// association through both stages rather than treating source availability as
// a saved order binding.
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

// RequestNeedsWeChatJSAPICompatibility reports WeChat-base requests made in
// its browser or with a nonblank payer OpenID. Other base methods return false
// before the browser/payer condition, so caller context preparation can skip
// official app matching.
func RequestNeedsWeChatJSAPICompatibility(req CreateOrderRequest) bool {
	if GetBasePaymentType(req.PaymentType) != TypeWxpay {
		return false
	}
	return req.IsWeChatBrowser || strings.TrimSpace(req.OpenID) != ""
}

// UsesOfficialWxpayVisibleMethod reports whether current visible WeChat
// resolution returns the exact official provider key. Nil dependencies,
// absent/cross-provider marker and resolution errors return false; context
// preparation uses that boolean before optional official app lookup.
func (s *Checkout) UsesOfficialWxpayVisibleMethod(ctx context.Context) bool {
	if s == nil || s.configService == nil {
		return false
	}
	inst, err := s.configService.ConfigResolveEnabledVisibleMethodInstance(ctx, TypeWxpay)
	// A current source-resolution failure leaves official-app context preparation
	// inactive. The actual selected official provider is checked separately later
	// before order creation.
	if err != nil {
		return false
	}
	if inst == nil {
		return false
	}
	return inst.ProviderKey == TypeWxpay
}

// InvokeProvider constructs the selected merchant adapter, initiates external
// payment and then persists accepted response details on an already committed
// local order. Supply nonnil order/configuration/selection and usable runtime
// callbacks; the creation store transaction has finished before this
// operation.
//
// Saved display subject wins over recomputation. Canonical return URL and
// optional signed resume token are built with server order identity before the
// provider call. Structured construction errors retain their metadata, which
// can overwrite added provider/instance keys; other errors are classified.
// Accepted response text is NUL-cleaned before the independent store write and
// ordinary creation audit. Persistence failure can follow external progress,
// and CreateOrder performs separate best-effort failure marking. Output keeps
// the supplied result type or its default and records shared JSAPI aliases.
func (s *Checkout) InvokeProvider(ctx context.Context, order *Order, req CreateOrderRequest, cfg *PaymentConfig, limitAmount float64, payAmountStr string, payAmount float64, plan *SubscriptionPlan, sel *InstanceSelection) (*CreateOrderResponse, error) {
	prov, err := s.runtime.CreateProvider(sel.ProviderKey, sel.InstanceID, sel.Config)
	if err != nil {
		s.runtime.Error("[PaymentService] CreateProvider failed", "provider", sel.ProviderKey, "instance", sel.InstanceID, "error", err)
		// Retain structured factory errors and merge their metadata after adding
		// provider/instance context. Existing error keys therefore win; ordinary
		// factory failures become provider-misconfigured service errors.
		if appErr := new(infraerrors.ApplicationError); errors.As(err, &appErr) {
			md := map[string]string{"provider": sel.ProviderKey, "instance_id": sel.InstanceID}
			maps.Copy(md, appErr.Metadata)
			return nil, appErr.WithMetadata(md)
		}
		return nil, infraerrors.ServiceUnavailable("PAYMENT_PROVIDER_MISCONFIGURED", "provider_misconfigured").
			WithMetadata(map[string]string{"provider": sel.ProviderKey, "instance_id": sel.InstanceID})
	}
	subject, _ := order.ProviderSnapshot["display_subject"].(string)
	// A stored nonblank display subject keeps initiation tied to the checkout
	// display observation. Missing snapshot text is recomputed from supplied
	// plan/principal/configuration in the current locale.
	if subject == "" {
		subject = PaymentSubject(plan, limitAmount, cfg, sel, locale.FromContext(ctx))
	}
	outTradeNo := order.OutTradeNo
	canonicalReturnURL, err := CanonicalizeReturnURL(req.ReturnURL, req.SrcHost, req.SrcURL)
	if err != nil {
		return nil, err
	}
	resumeToken := ""
	// A signed public session is added only when a canonical return destination
	// and signing service are available. Otherwise provider redirection keeps its
	// server order parameters without that session credential.
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
	// Accepted external initiation precedes the independent local detail write.
	// Remove PostgreSQL-forbidden NULs before persistence; a later write failure
	// still leaves the upstream result available to recovery.
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

// PersistCreatePaymentResponse delegates provider detail persistence after
// external initiation has returned. The store owns the update and its
// validation; callers retain the error because upstream progress may already
// exist before that independent local write.
func (s *Checkout) PersistCreatePaymentResponse(ctx context.Context, id int64, sel *InstanceSelection, response *CreatePaymentResponse) (*Order, error) {
	return s.store.PersistCheckoutResponse(ctx, id, sel, response)
}

// ShouldUseAlipayMobilePrecreate reports whether enabled mobile deep-link
// preference and a mobile request select official Alipay. Nil
// configuration/selection returns false, while provider-key matching trims and
// compares case-insensitively before the adapter preference is passed.
func ShouldUseAlipayMobilePrecreate(req CreateOrderRequest, cfg *PaymentConfig, sel *InstanceSelection) bool {
	return cfg != nil &&
		cfg.AlipayMobilePrecreateDeepLink &&
		req.IsMobile &&
		sel != nil &&
		strings.EqualFold(strings.TrimSpace(sel.ProviderKey), TypeAlipay)
}

// SanitizeCreatePaymentResponseDetails mutates eight text fields on a nonnil
// provider response before PostgreSQL persistence, removing NUL bytes from
// trade, URL, QR, customer and invoice values. Nil input is accepted;
// ClientSecret and other transient response fields are not included in this
// cleanup.
func SanitizeCreatePaymentResponseDetails(pr *CreatePaymentResponse) {
	if pr == nil {
		return
	}
	// Apply sanitization before empty strings become omitted SetNillable updates.
	// A field consisting solely of NUL becomes empty, so persistence can retain
	// its prior stored value instead of writing the forbidden bytes.
	pr.TradeNo = RemovePostgresTextNUL(pr.TradeNo)
	pr.PayURL = RemovePostgresTextNUL(pr.PayURL)
	pr.QRCode = RemovePostgresTextNUL(pr.QRCode)
	pr.CustomerID = RemovePostgresTextNUL(pr.CustomerID)
	pr.InvoiceID = RemovePostgresTextNUL(pr.InvoiceID)
	pr.InvoiceURL = RemovePostgresTextNUL(pr.InvoiceURL)
	pr.InvoicePDF = RemovePostgresTextNUL(pr.InvoicePDF)
	pr.InvoiceStatus = RemovePostgresTextNUL(pr.InvoiceStatus)
}

// RemovePostgresTextNUL returns text with NUL bytes removed for PostgreSQL
// text columns. A value without NUL is returned directly; other bytes remain
// unchanged, so the helper does not validate or redact URL/identifier
// contents.
func RemovePostgresTextNUL(value string) string {
	if !strings.ContainsRune(value, 0) {
		return value
	}
	// PostgreSQL text rejects NUL bytes. Remove that byte alone so URLs and
	// merchant identifiers retain every other received byte for the subsequent
	// stored or response representation.
	return strings.ReplaceAll(value, "\x00", "")
}

// BuildProviderCreatePaymentRequest assembles adapter input from the caller's
// merchant number, formatted amount, subject and expiration. It trims payer
// OpenID and carries selected subtype text; BillingInfo remains the supplied
// pointer and must already have been validated for its provider.
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

// SelectedInstanceSupportedTypes returns the selected instance's subtype text
// for adapter input. Nil selection returns empty text, leaving
// provider-specific default subtype handling to CreatePayment rather than
// inventing supported methods here.
func SelectedInstanceSupportedTypes(sel *InstanceSelection) string {
	if sel == nil {
		return ""
	}
	return sel.SupportedTypes
}

// BuildPaymentSubject returns the checkout product title using the default
// display locale.
func (s *Checkout) BuildPaymentSubject(plan *SubscriptionPlan, limitAmount float64, cfg *PaymentConfig, sel *InstanceSelection) string {
	return PaymentSubject(plan, limitAmount, cfg, sel, locale.Default())
}

// PaymentSubject builds the recorded product title from the supplied plan or
// balance principal, selected currency and configured affixes. A plan product
// name wins; an absent name uses a language-specific subscription label plus
// the supplied plan name.
//
// Balance presentation formats limitAmount in the selected provider currency
// or default CNY. Configured nonblank prefix/suffix replaces the generic
// brand/currency wrapping around that amount, while subscription names also
// receive those affixes. This helper consumes plan display text rather than
// localizing it, and CreateOrder records its returned subject for later
// initiation to reuse.
func PaymentSubject(plan *SubscriptionPlan, limitAmount float64, cfg *PaymentConfig, sel *InstanceSelection, language string) string {
	if plan != nil {
		productName := plan.ProductName
		// An absent explicit product name needs a title for the payment gateway.
		// Combine the supplied plan name with the selected display-language label
		// before configured affixes are applied.
		if productName == "" {
			productName = "TokenRouter Subscription " + plan.Name
			if locale.Normalize(language) == "zh-Hans" {
				productName = "TokenRouter 订阅 " + plan.Name
			}
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
	return "TokenRouter " + amountStr + " " + currency
}

// HasPaymentProductNameAffix reports whether nonnil configuration has a
// nonblank trimmed product prefix or suffix. Subject builders use that
// decision before selecting their generic brand/currency presentation; nil
// configuration returns false.
func HasPaymentProductNameAffix(cfg *PaymentConfig) bool {
	if cfg == nil {
		return false
	}
	pf := strings.TrimSpace(cfg.ProductNamePrefix)
	sf := strings.TrimSpace(cfg.ProductNameSuffix)
	return pf != "" || sf != ""
}

// ApplyPaymentProductNameAffix wraps the supplied product text with trimmed
// configured prefix/suffix when either is nonblank. Absent configuration or
// affixes returns the product unchanged, and the assembled outer text is
// trimmed.
func ApplyPaymentProductNameAffix(productName string, cfg *PaymentConfig) string {
	if !HasPaymentProductNameAffix(cfg) {
		return productName
	}
	pf := strings.TrimSpace(cfg.ProductNamePrefix)
	sf := strings.TrimSpace(cfg.ProductNameSuffix)
	return strings.TrimSpace(pf + " " + productName + " " + sf)
}

// MaybeBuildWeChatOAuthRequiredResponseForSelection returns OAuth-required
// output only for WeChat-base browser requests without payer OpenID and no
// resolved nonofficial provider key. Other cases return nil,nil so checkout
// can continue; official credential/signing errors remain visible from the
// delegated builder.
func (s *Checkout) MaybeBuildWeChatOAuthRequiredResponseForSelection(ctx context.Context, req CreateOrderRequest, amount float64, feeBreakdown FeeBreakdown, sel *InstanceSelection) (*CreateOrderResponse, error) {
	if sel != nil && sel.ProviderKey != "" && sel.ProviderKey != TypeWxpay {
		return nil, nil
	}
	if strings.TrimSpace(req.OpenID) != "" || !req.IsWeChatBrowser || GetBasePaymentType(req.PaymentType) != TypeWxpay {
		return nil, nil
	}
	return s.BuildWeChatOAuthRequiredResponse(ctx, req, amount, feeBreakdown)
}

// BuildWeChatOAuthRequiredResponse returns official app/base-scope
// authorization details and the supplied amount/fee values before creating an
// order. It requires usable WeChat credentials and resume signing; its URL
// carries original request purchase inputs for the browser authorization flow.
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

// ValidateSelectedCreateOrderInstance verifies the selected official WeChat
// JSAPI app against current OAuth credentials when compatibility applies.
// Credential errors return, and missing/mismatched selected app becomes
// NO_AVAILABLE_INSTANCE; nonapplicable requests retain ordinary
// selected-provider validation.
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

// CalculateCreateOrderPayAmount validates input currency precision, computes
// configured fees and returns the formatted and float gateway charge. The fee
// helper requires finite numeric inputs, while minor-unit conversion validates
// the formatted representation.
//
// Input precision is checked before fee rounding, so unsupported fractional
// base input is rejected rather than silently reduced. The completed fee
// charge is formatted using currency precision, then checked for API
// conversion before ParseFloat produces the response value. No independent
// positive-business-amount or int64-overflow validation is added here;
// checkout input and downstream participants retain those obligations.
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

// CalculateCreateOrderPayAmountForOrderType converts the subscription
// principal when applicable before calculating gateway fees and formatting.
// Other order types use the supplied principal directly; callers provide
// validated finite purchase/fee quantities and the selected currency.
func CalculateCreateOrderPayAmountForOrderType(limitAmount float64, methodFee FeeConfig, currency, orderType string, usdToCnyRate float64) (FeeBreakdown, string, float64, error) {
	paymentAmount := limitAmount
	if orderType == OrderTypeSubscription {
		paymentAmount = CalculateSubscriptionGatewayBaseAmount(limitAmount, usdToCnyRate, currency)
	}
	return CalculateCreateOrderPayAmount(paymentAmount, methodFee, currency)
}

// CalculateSubscriptionGatewayBaseAmount converts a finite subscription price
// with a normalized positive rate only for exact CNY currency.
// Nonpositive/invalid rate or other currency uses the supplied price directly
// before fee calculation.
//
// Decimal multiplication rounds the converted base to the currency's accepted
// fraction digits before returning its approximate float representation. That
// base feeds fixed/percentage fee calculation, keeping the recorded plan price
// distinct from gateway currency money. Callers supply finite amount; this
// helper performs conversion and rate normalization rather than plan sale or
// positive-price validation.
func CalculateSubscriptionGatewayBaseAmount(amount, usdToCnyRate float64, currency string) float64 {
	rate := NormalizeSubscriptionUSDToCNYRate(usdToCnyRate)
	if rate <= 0 || currency != DefaultPaymentCurrency {
		return amount
	}
	// Convert the recorded subscription price before fee calculation, rounding
	// the decimal product at the target currency precision. The returned float is
	// gateway base money, while the plan amount stays recorded separately.
	return decimal.NewFromFloat(amount).
		Mul(decimal.NewFromFloat(rate)).
		Round(int32(CurrencyMaxFractionDigits(currency))).
		InexactFloat64()
}

// ValidateCreateOrderAmountCurrency checks the float's decimal representation
// through AmountToMinorUnit and returns INVALID_AMOUNT with currency metadata
// on failure. It validates text/currency precision, while positive business
// quantity and scaled int64 range remain separate caller obligations.
func ValidateCreateOrderAmountCurrency(amount float64, currency string) error {
	amountStr := strconv.FormatFloat(amount, 'f', -1, 64)
	if _, err := AmountToMinorUnit(amountStr, currency); err != nil {
		return infraerrors.BadRequest("INVALID_AMOUNT", err.Error()).
			WithMetadata(map[string]string{"currency": currency})
	}
	return nil
}

// ValidateSelectedCreateOrderAmountCurrency checks formatted gateway money
// against a selected provider's configured currency. Nil selection returns
// nil; representation failure becomes INVALID_AMOUNT with that currency
// metadata, and this helper adds no separate purchase-sign or overflow
// validation.
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

// RequiresWeChatJSAPICompatibleSelection reports official WeChat selections
// for WeChat-base requests in its browser or with a nonblank OpenID.
// Nil/other-provider selections return false, so post-selection app validation
// applies only to that official payer flow.
func RequiresWeChatJSAPICompatibleSelection(req CreateOrderRequest, sel *InstanceSelection) bool {
	if sel == nil || sel.ProviderKey != TypeWxpay || GetBasePaymentType(req.PaymentType) != TypeWxpay {
		return false
	}
	return req.IsWeChatBrowser || strings.TrimSpace(req.OpenID) != ""
}

// ClassifyCreatePaymentError maps the official WeChat H5 unauthorized
// diagnostic to an action-bearing service error for a WeChat-base request. Nil
// error returns nil; other errors become PAYMENT_GATEWAY_ERROR with the
// upstream error text retained in the message.
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

// BuildCreateOrderResponse assembles initiation output from nonnil persisted
// order, selected merchant and provider response with the supplied result
// type. Status is literal pending even if the returned order observation has
// progressed to another state.
//
// Order identity, price currency, fees and expiration come from persistence.
// Gateway PayAmount uses the caller's calculated value and current initiation details
// come from the provider response. OAuth and JSAPI pointers remain shared, and
// JSAPI plus JSAPIPayload reference the same object. Resume token and Alipay
// deep-link availability are assigned by InvokeProvider afterward; this helper
// does not requery current state.
func BuildCreateOrderResponse(order *Order, req CreateOrderRequest, payAmount float64, sel *InstanceSelection, pr *CreatePaymentResponse, resultType CreatePaymentResultType) *CreateOrderResponse {
	return &CreateOrderResponse{
		OrderID:        order.ID,
		Amount:         order.Amount,
		AmountCurrency: PaymentOrderAmountCurrency(order),
		PayAmount:      payAmount,
		FeeRate:        order.FeeRate,
		FeeFixed:       order.FeeFixed,
		FeeRateAmount:  order.FeeRateAmount,
		FeeAmount:      order.FeeAmount,
		Status:         OrderStatusPending,
		ResultType:     resultType,
		PaymentType:    req.PaymentType,
		OutTradeNo:     order.OutTradeNo,
		PayURL:         pr.PayURL,
		QRCode:         pr.QRCode,
		ClientSecret:   pr.ClientSecret,
		CustomerID:     pr.CustomerID,
		InvoiceID:      pr.InvoiceID,
		InvoiceURL:     pr.InvoiceURL,
		InvoicePDF:     pr.InvoicePDF,
		InvoiceStatus:  pr.InvoiceStatus,
		IntentID:       pr.IntentID,
		Currency:       pr.Currency,
		CountryCode:    pr.CountryCode,
		PaymentEnv:     pr.PaymentEnv,
		OAuth:          pr.OAuth,
		JSAPI:          pr.JSAPI,
		JSAPIPayload:   pr.JSAPI,
		ExpiresAt:      order.ExpiresAt,
		PaymentMode:    sel.PaymentMode,
	}
}

// BuildWeChatPaymentOAuthStartURL builds the relative payment OAuth start URL
// from the original request fields and optional scope. Method is trimmed,
// positive requested amount/plan ID are encoded, and nonblank order type/scope
// are carried.
//
// The purchase redirect is derived from SrcURL's path/query with its
// payment-to-purchase compatibility mapping; the origin is not used as the
// redirect target. Query encoding preserves each value across the browser
// authorization request. Subscription effective price is not substituted here,
// and amount text remains an input for later continuation application and
// checkout validation.
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

// PaymentRedirectPathFromURL extracts a purchase continuation path/query from
// request source text and maps supported payment-page forms. Empty,
// parse-error or nonlocal path results choose /purchase, and an already
// single-slash local path is normalized directly.
//
// Parsed absolute URLs contribute EscapedPath before Path and retain their raw
// query, while origin and fragment are discarded. This extracts navigation
// context rather than validating a payment return origin;
// CanonicalizeReturnURL owns that separate check. A protocol-relative input is
// parsed for its local path, and the final helper performs only the documented
// text compatibility mapping.
func PaymentRedirectPathFromURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "/purchase"
	}
	if strings.HasPrefix(rawURL, "/") && !strings.HasPrefix(rawURL, "//") {
		return NormalizePaymentRedirectPath(rawURL)
	}
	u, err := url.Parse(rawURL)
	// An unreadable source URL supplies no usable navigation path. Use the
	// purchase default so OAuth continuation still has a local destination rather
	// than propagating malformed referer text.
	if err != nil {
		return "/purchase"
	}
	// Keep the parsed escaped path before its decoded form and append the raw
	// query afterward. This preserves percent-encoded navigation input while
	// discarding source origin and fragment.
	path := strings.TrimSpace(u.EscapedPath())
	if path == "" {
		path = strings.TrimSpace(u.Path)
	}
	// A parsed value without a single-slash local path cannot provide purchase
	// navigation. Choose the default destination before retaining any query from
	// that source.
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "/purchase"
	}
	if strings.TrimSpace(u.RawQuery) != "" {
		path += "?" + u.RawQuery
	}
	return NormalizePaymentRedirectPath(path)
}

// NormalizePaymentRedirectPath trims navigation text and maps exact /payment
// or /payment? forms to /purchase while retaining the query suffix. Empty
// input returns /purchase; other text is returned unchanged.
//
// The mapping uses prefix text rather than URL parsing, so child paths and
// fragment forms remain as supplied. PaymentRedirectPathFromURL handles its
// own parsed-origin/fragment removal before calling this helper, while direct
// local input can retain its text. Callers use the result as browser
// navigation context for OAuth continuation.
func NormalizePaymentRedirectPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/purchase"
	}
	// Payment-page navigation is handled by the purchase route for checkout
	// continuation. Rewrite only the exact and query-prefixed forms so existing
	// purchase parameters keep their encoding.
	if path == "/payment" {
		return "/purchase"
	}
	if strings.HasPrefix(path, "/payment?") {
		return "/purchase" + strings.TrimPrefix(path, "/payment")
	}
	return path
}

// ResolveWxpayJSAPIAppID returns trimmed mpAppId when configured, then trimmed
// appId. Checkout app matching and snapshot construction use that precedence;
// nil configuration returns empty text for the provider's later configuration
// checks.
func ResolveWxpayJSAPIAppID(config map[string]string) string {
	if appID := strings.TrimSpace(config["mpAppId"]); appID != "" {
		return appID
	}
	// When official-account mpAppId is absent, use merchant appId for the payer
	// flow. Selection and stored snapshot construction share this precedence so
	// later merchant validation compares the chosen app.
	return strings.TrimSpace(config["appId"])
}
