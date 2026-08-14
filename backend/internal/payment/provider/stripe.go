package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/payment"
	stripe "github.com/stripe/stripe-go/v85"
	"github.com/stripe/stripe-go/v85/webhook"
)

// Stripe constants.
const (
	stripeEventPaymentSuccess         = "payment_intent.succeeded"
	stripeEventPaymentFailed          = "payment_intent.payment_failed"
	stripeEventCheckoutDone           = "checkout.session.completed"
	stripeEventCheckoutExpired        = "checkout.session.expired"
	stripeEventCheckoutAsyncSucceeded = "checkout.session.async_payment_succeeded"
	stripeEventCheckoutAsyncFailed    = "checkout.session.async_payment_failed"
	stripeEventInvoicePaid            = "invoice.paid"
	stripeEventInvoiceFailed          = "invoice.payment_failed"
)

// Stripe implements the payment.CancelableProvider interface for Stripe payments.
type Stripe struct {
	instanceID string
	config     map[string]string

	mu          sync.Mutex
	initialized bool
	sc          *stripe.Client
}

// NewStripe creates a new Stripe provider instance.
func NewStripe(instanceID string, config map[string]string) (*Stripe, error) {
	if config["secretKey"] == "" {
		return nil, fmt.Errorf("stripe config missing required key: secretKey")
	}
	cfg := cloneStringMap(config)
	currency, err := payment.NormalizePaymentCurrency(cfg["currency"])
	if err != nil {
		return nil, fmt.Errorf("stripe config currency: %w", err)
	}
	cfg["currency"] = currency
	return &Stripe{
		instanceID: instanceID,
		config:     cfg,
	}, nil
}

func (s *Stripe) ensureInit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		s.sc = stripe.NewClient(s.config["secretKey"])
		s.initialized = true
	}
}

// GetPublishableKey returns the publishable key for frontend use.
func (s *Stripe) GetPublishableKey() string {
	return s.config["publishableKey"]
}

func (s *Stripe) Name() string        { return "Stripe" }
func (s *Stripe) ProviderKey() string { return payment.TypeStripe }
func (s *Stripe) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeStripe}
}

func (s *Stripe) MerchantIdentityMetadata() map[string]string {
	if s == nil {
		return nil
	}
	return map[string]string{"currency": s.currency()}
}

func (s *Stripe) currency() string {
	if s == nil {
		return payment.DefaultPaymentCurrency
	}
	currency, err := payment.NormalizePaymentCurrency(s.config["currency"])
	if err != nil {
		return payment.DefaultPaymentCurrency
	}
	return currency
}

// CreatePayment 使用 Stripe Checkout Session 创建托管收银台支付单。
func (s *Stripe) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	s.ensureInit()

	currency := s.currency()
	amountInMinorUnit, err := payment.AmountToMinorUnit(req.Amount, currency)
	if err != nil {
		return nil, fmt.Errorf("stripe create payment: %w", err)
	}
	if strings.TrimSpace(req.ReturnURL) == "" {
		return nil, fmt.Errorf("stripe checkout requires return_url")
	}

	billing := normalizeStripeBillingInfo(req)
	customerParams := buildStripeCustomerCreateParams(req, billing, s.instanceID)
	customerParams.SetIdempotencyKey(fmt.Sprintf("cus-%s", req.OrderID))
	customer, err := s.sc.V1Customers.Create(ctx, customerParams)
	if err != nil {
		return nil, fmt.Errorf("stripe create customer: %w", err)
	}

	checkoutParams := buildStripeCheckoutSessionCreateParams(customer.ID, req, amountInMinorUnit, s.instanceID, currency)
	checkoutParams.SetIdempotencyKey(fmt.Sprintf("cs-%s", req.OrderID))
	checkoutSession, err := s.sc.V1CheckoutSessions.Create(ctx, checkoutParams)
	if err != nil {
		return nil, fmt.Errorf("stripe create checkout session: %w", err)
	}
	doc := stripeCheckoutInvoiceDocument(checkoutSession)

	return &payment.CreatePaymentResponse{
		TradeNo:       strings.TrimSpace(checkoutSession.ID),
		PayURL:        strings.TrimSpace(checkoutSession.URL),
		CustomerID:    customer.ID,
		InvoiceID:     doc.InvoiceID,
		InvoiceURL:    doc.HostedInvoiceURL,
		InvoicePDF:    doc.InvoicePDF,
		InvoiceStatus: doc.InvoiceStatus,
		Currency:      currency,
		ExpiresAt:     stripeCheckoutSessionExpiresAt(checkoutSession),
	}, nil
}

func buildStripeCheckoutSessionCreateParams(customerID string, req payment.CreatePaymentRequest, amountInMinorUnit int64, instanceID string, currency string) *stripe.CheckoutSessionCreateParams {
	normalizedCurrency, err := payment.NormalizePaymentCurrency(currency)
	if err != nil {
		normalizedCurrency = payment.DefaultPaymentCurrency
	}
	metadata := stripePaymentMetadata(req.OrderID, instanceID)
	// 账单资料来自本地表单预建的 Customer；不要允许 Checkout 或 Link 回写并覆盖发票抬头和地址。
	params := &stripe.CheckoutSessionCreateParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModePayment)),
		Customer:          stripe.String(customerID),
		ClientReferenceID: stripe.String(req.OrderID),
		SuccessURL:        stripe.String(stripeCheckoutReturnURL(req.ReturnURL, "success")),
		CancelURL:         stripe.String(stripeCheckoutReturnURL(req.ReturnURL, "cancelled")),
		InvoiceCreation:   buildStripeCheckoutInvoiceCreationParams(req, instanceID),
		LineItems:         buildStripeCheckoutLineItems(req, amountInMinorUnit, normalizedCurrency),
		Metadata:          metadata,
		PaymentIntentData: buildStripeCheckoutPaymentIntentDataParams(req, instanceID),
		BillingAddressCollection: stripe.String(
			string(stripe.CheckoutSessionBillingAddressCollectionAuto),
		),
	}
	params.ExpiresAt = stripe.Int64(stripeCheckoutExpiresAt(req.ExpiresAt))
	// Checkout 不传 payment_method_types，让 Stripe Dashboard 的动态支付方式决定 Alipay/Link/微信/卡片展示。
	params.AddExpand("customer")
	params.AddExpand("invoice")
	params.AddExpand("payment_intent")
	return params
}

func buildStripeCheckoutInvoiceCreationParams(req payment.CreatePaymentRequest, instanceID string) *stripe.CheckoutSessionCreateInvoiceCreationParams {
	return &stripe.CheckoutSessionCreateInvoiceCreationParams{
		Enabled: stripe.Bool(true),
		InvoiceData: &stripe.CheckoutSessionCreateInvoiceCreationInvoiceDataParams{
			Description: stripe.String(req.Subject),
			Metadata:    stripePaymentMetadata(req.OrderID, instanceID),
		},
	}
}

func buildStripeCheckoutPaymentIntentDataParams(req payment.CreatePaymentRequest, instanceID string) *stripe.CheckoutSessionCreatePaymentIntentDataParams {
	billing := normalizeStripeBillingInfo(req)
	params := &stripe.CheckoutSessionCreatePaymentIntentDataParams{
		Description: stripe.String(req.Subject),
		Metadata:    stripePaymentMetadata(req.OrderID, instanceID),
	}
	if billing.Email != "" {
		params.ReceiptEmail = stripe.String(billing.Email)
	}
	return params
}

func buildStripeCheckoutLineItems(req payment.CreatePaymentRequest, amountInMinorUnit int64, currency string) []*stripe.CheckoutSessionCreateLineItemParams {
	productName := strings.TrimSpace(req.Subject)
	if productName == "" {
		productName = "TokenRouter payment"
	}
	return []*stripe.CheckoutSessionCreateLineItemParams{{
		Quantity: stripe.Int64(1),
		PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
			Currency:   stripe.String(strings.ToLower(currency)),
			UnitAmount: stripe.Int64(amountInMinorUnit),
			ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{
				Name:     stripe.String(productName),
				Metadata: stripePaymentMetadata(req.OrderID, ""),
			},
		},
	}}
}

func stripeCheckoutReturnURL(returnURL string, status string) string {
	returnURL = strings.TrimSpace(returnURL)
	if returnURL == "" || strings.TrimSpace(status) == "" {
		return returnURL
	}
	parsed, err := url.Parse(returnURL)
	if err != nil {
		return returnURL
	}
	query := parsed.Query()
	query.Set("status", strings.TrimSpace(status))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func stripeCheckoutExpiresAt(expiresAt time.Time) int64 {
	return stripeCheckoutExpiresAtAt(expiresAt, time.Now())
}

func stripeCheckoutExpiresAtAt(expiresAt time.Time, now time.Time) int64 {
	// 额外预留一分钟创建耗时，确保请求到达 Stripe 时仍满足最短三十分钟限制。
	minExpiresAt := now.Add(31 * time.Minute)
	if expiresAt.IsZero() || expiresAt.Before(minExpiresAt) {
		return minExpiresAt.Unix()
	}
	maxExpiresAt := now.Add(24 * time.Hour)
	if expiresAt.After(maxExpiresAt) {
		return maxExpiresAt.Unix()
	}
	return expiresAt.Unix()
}

func stripeCheckoutSessionExpiresAt(session *stripe.CheckoutSession) time.Time {
	if session == nil || session.ExpiresAt <= 0 {
		return time.Time{}
	}
	return time.Unix(session.ExpiresAt, 0).UTC()
}

func stripePaymentMetadata(orderID string, instanceID string) map[string]string {
	return map[string]string{
		"orderId":            orderID,
		"providerInstanceId": strings.TrimSpace(instanceID),
	}
}

// QueryOrder 支持按 Checkout Session、Invoice ID 和旧 PaymentIntent 查询订单。
func (s *Stripe) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	s.ensureInit()

	tradeNo = strings.TrimSpace(tradeNo)
	if strings.HasPrefix(tradeNo, "cs_") {
		return s.queryCheckoutSession(ctx, tradeNo)
	}
	if strings.HasPrefix(tradeNo, "in_") {
		return s.queryInvoice(ctx, tradeNo)
	}

	pi, err := s.sc.V1PaymentIntents.Retrieve(ctx, tradeNo, nil)
	if err != nil {
		return nil, fmt.Errorf("stripe query order: %w", err)
	}

	status := payment.ProviderStatusPending
	switch pi.Status {
	case stripe.PaymentIntentStatusSucceeded:
		status = payment.ProviderStatusPaid
	case stripe.PaymentIntentStatusProcessing:
		status = payment.ProviderStatusProcessing
	case stripe.PaymentIntentStatusCanceled:
		status = payment.ProviderStatusFailed
	}

	currency := stripeIntentCurrency(pi.Currency, s.currency())
	return &payment.QueryOrderResponse{
		TradeNo: pi.ID,
		Status:  status,
		Amount:  payment.MinorUnitToAmount(pi.Amount, currency),
		Metadata: map[string]string{
			"currency": currency,
		},
	}, nil
}

func (s *Stripe) queryCheckoutSession(ctx context.Context, sessionID string) (*payment.QueryOrderResponse, error) {
	params := &stripe.CheckoutSessionRetrieveParams{}
	params.AddExpand("customer")
	params.AddExpand("invoice")
	params.AddExpand("payment_intent")
	session, err := s.sc.V1CheckoutSessions.Retrieve(ctx, sessionID, params)
	if err != nil {
		return nil, fmt.Errorf("stripe query checkout session: %w", err)
	}

	status := stripeCheckoutProviderStatus(session)

	currency := stripeIntentCurrency(session.Currency, s.currency())
	tradeNo := stripeCheckoutTradeNo(session)
	if tradeNo == "" {
		tradeNo = sessionID
	}
	return &payment.QueryOrderResponse{
		TradeNo:  tradeNo,
		Status:   status,
		Amount:   payment.MinorUnitToAmount(session.AmountTotal, currency),
		Metadata: stripeCheckoutMetadata(session, currency),
	}, nil
}

func stripeCheckoutProviderStatus(session *stripe.CheckoutSession) string {
	if session == nil {
		return payment.ProviderStatusPending
	}
	if session.PaymentStatus == stripe.CheckoutSessionPaymentStatusPaid {
		return payment.ProviderStatusPaid
	}
	if session.PaymentIntent != nil && session.PaymentIntent.Status == stripe.PaymentIntentStatusSucceeded {
		return payment.ProviderStatusPaid
	}
	if session.Invoice != nil && session.Invoice.Status == stripe.InvoiceStatusPaid {
		return payment.ProviderStatusPaid
	}
	if session.Status == stripe.CheckoutSessionStatusExpired {
		return payment.ProviderStatusFailed
	}
	if session.Status == stripe.CheckoutSessionStatusComplete && session.PaymentStatus == stripe.CheckoutSessionPaymentStatusUnpaid {
		if stripeCheckoutCompletedPaymentFailed(session) {
			return payment.ProviderStatusFailed
		}
		return payment.ProviderStatusProcessing
	}
	return payment.ProviderStatusPending
}

func stripeCheckoutCompletedPaymentFailed(session *stripe.CheckoutSession) bool {
	if session == nil || session.Status != stripe.CheckoutSessionStatusComplete {
		return false
	}
	if session.PaymentIntent != nil {
		switch session.PaymentIntent.Status {
		case stripe.PaymentIntentStatusCanceled, stripe.PaymentIntentStatusRequiresPaymentMethod:
			return true
		}
	}
	if session.Invoice != nil {
		switch session.Invoice.Status {
		case stripe.InvoiceStatusVoid, stripe.InvoiceStatusUncollectible:
			return true
		}
	}
	return false
}

func (s *Stripe) queryInvoice(ctx context.Context, invoiceID string) (*payment.QueryOrderResponse, error) {
	params := &stripe.InvoiceRetrieveParams{}
	params.AddExpand("confirmation_secret")
	params.AddExpand("payments.data.payment.payment_intent")
	inv, err := s.sc.V1Invoices.Retrieve(ctx, invoiceID, params)
	if err != nil {
		return nil, fmt.Errorf("stripe query invoice: %w", err)
	}

	status := payment.ProviderStatusPending
	switch inv.Status {
	case stripe.InvoiceStatusPaid:
		status = payment.ProviderStatusPaid
	case stripe.InvoiceStatusVoid, stripe.InvoiceStatusUncollectible:
		status = payment.ProviderStatusFailed
	}

	amount := inv.AmountPaid
	if amount <= 0 {
		amount = inv.AmountDue
	}
	tradeNo := stripeInvoicePaymentIntentID(inv)
	if tradeNo == "" && inv.ConfirmationSecret != nil {
		tradeNo = stripePaymentIntentIDFromClientSecret(inv.ConfirmationSecret.ClientSecret)
	}
	if tradeNo == "" {
		tradeNo = inv.ID
	}
	return &payment.QueryOrderResponse{
		TradeNo: tradeNo,
		Status:  status,
		Amount:  payment.MinorUnitToAmount(amount, s.currency()),
		Metadata: map[string]string{
			"invoice_id":     inv.ID,
			"invoice_status": string(inv.Status),
			"invoice_url":    inv.HostedInvoiceURL,
			"invoice_pdf":    inv.InvoicePDF,
			"currency":       s.currency(),
		},
	}, nil
}

// VerifyNotification verifies a Stripe webhook event.
func (s *Stripe) VerifyNotification(_ context.Context, rawBody string, headers map[string]string) (*payment.PaymentNotification, error) {
	s.ensureInit()

	webhookSecret := s.config["webhookSecret"]
	if webhookSecret == "" {
		return nil, fmt.Errorf("stripe webhookSecret not configured")
	}

	sig := headers["stripe-signature"]
	if sig == "" {
		return nil, fmt.Errorf("stripe notification missing stripe-signature header")
	}

	event, err := webhook.ConstructEvent([]byte(rawBody), sig, webhookSecret)
	if err != nil {
		return nil, fmt.Errorf("stripe verify notification: %w", err)
	}
	return parseStripeEvent(&event, rawBody)
}

func parseStripeEvent(event *stripe.Event, rawBody string) (*payment.PaymentNotification, error) {
	if event == nil {
		return nil, nil
	}
	switch event.Type {
	case stripeEventCheckoutDone:
		return parseStripeCheckoutSession(event, payment.ProviderStatusProcessing, rawBody)
	case stripeEventCheckoutExpired:
		return parseStripeCheckoutSession(event, payment.ProviderStatusFailed, rawBody)
	case stripeEventCheckoutAsyncSucceeded:
		return parseStripeCheckoutSession(event, payment.ProviderStatusSuccess, rawBody)
	case stripeEventCheckoutAsyncFailed:
		return parseStripeCheckoutSession(event, payment.ProviderStatusFailed, rawBody)
	case stripeEventInvoicePaid:
		return parseStripeInvoice(event, payment.ProviderStatusSuccess, rawBody)
	case stripeEventPaymentSuccess:
		return parseStripePaymentIntent(event, payment.ProviderStatusSuccess, rawBody)
	case stripeEventInvoiceFailed, stripeEventPaymentFailed:
		// 这两个事件只代表一次付款尝试失败，Checkout 仍可能允许用户重试。
		return nil, nil
	}

	return nil, nil
}

func parseStripeCheckoutSession(event *stripe.Event, status string, rawBody string) (*payment.PaymentNotification, error) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		return nil, fmt.Errorf("stripe parse checkout session: %w", err)
	}
	currency := stripeIntentCurrency(session.Currency, payment.DefaultPaymentCurrency)
	tradeNo := stripeCheckoutTradeNo(&session)
	if tradeNo == "" {
		tradeNo = session.ID
	}
	if status == payment.ProviderStatusProcessing {
		// completed 事件必须明确为 paid 或 unpaid，其他状态不改变本地订单。
		switch session.PaymentStatus {
		case stripe.CheckoutSessionPaymentStatusPaid:
			status = payment.ProviderStatusSuccess
		case stripe.CheckoutSessionPaymentStatusUnpaid:
		default:
			return nil, nil
		}
	}
	// 成功事件若携带未付款状态则拒绝履约，防止异常或不完整载荷提前发放。
	if status == payment.ProviderStatusSuccess && session.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		return nil, nil
	}
	return &payment.PaymentNotification{
		TradeNo:  tradeNo,
		OrderID:  session.Metadata["orderId"],
		Amount:   payment.MinorUnitToAmount(session.AmountTotal, currency),
		Status:   status,
		RawData:  rawBody,
		Metadata: stripeCheckoutMetadata(&session, currency),
	}, nil
}

func parseStripePaymentIntent(event *stripe.Event, status string, rawBody string) (*payment.PaymentNotification, error) {
	var pi stripe.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
		return nil, fmt.Errorf("stripe parse payment_intent: %w", err)
	}
	currency := stripeIntentCurrency(pi.Currency, payment.DefaultPaymentCurrency)
	return &payment.PaymentNotification{
		TradeNo: pi.ID,
		OrderID: pi.Metadata["orderId"],
		Amount:  payment.MinorUnitToAmount(pi.Amount, currency),
		Status:  status,
		RawData: rawBody,
		Metadata: map[string]string{
			"currency": currency,
		},
	}, nil
}

func parseStripeInvoice(event *stripe.Event, status string, rawBody string) (*payment.PaymentNotification, error) {
	var inv stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &inv); err != nil {
		return nil, fmt.Errorf("stripe parse invoice: %w", err)
	}
	amount := inv.AmountPaid
	if amount <= 0 {
		amount = inv.AmountDue
	}
	currency := stripeIntentCurrency(inv.Currency, payment.DefaultPaymentCurrency)
	tradeNo := stripeInvoicePaymentIntentID(&inv)
	if tradeNo == "" {
		tradeNo = inv.ID
	}
	return &payment.PaymentNotification{
		TradeNo: tradeNo,
		OrderID: inv.Metadata["orderId"],
		Amount:  payment.MinorUnitToAmount(amount, currency),
		Status:  status,
		RawData: rawBody,
		Metadata: map[string]string{
			"invoice_id":     inv.ID,
			"invoice_status": string(inv.Status),
			"invoice_url":    inv.HostedInvoiceURL,
			"invoice_pdf":    inv.InvoicePDF,
			"currency":       currency,
		},
	}, nil
}

// Refund creates a Stripe refund.
func (s *Stripe) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	s.ensureInit()

	amountInMinorUnit, err := payment.AmountToMinorUnit(req.Amount, s.currency())
	if err != nil {
		return nil, fmt.Errorf("stripe refund: %w", err)
	}
	// 托管账单订单可能只持久化 invoice id，退款前需要解析到实际的 PaymentIntent。
	paymentIntentID := strings.TrimSpace(req.TradeNo)
	switch {
	case strings.HasPrefix(paymentIntentID, "cs_"):
		paymentIntentID, err = s.findCheckoutSessionPaymentIntentID(ctx, paymentIntentID)
		if err != nil {
			return nil, err
		}
		if paymentIntentID == "" {
			return nil, fmt.Errorf("stripe refund: checkout session payment intent is unavailable")
		}
	case strings.HasPrefix(paymentIntentID, "in_"):
		paymentIntentID, err = s.findInvoicePaymentIntentID(ctx, paymentIntentID)
		if err != nil {
			return nil, err
		}
		if paymentIntentID == "" {
			return nil, fmt.Errorf("stripe refund: invoice payment intent is unavailable")
		}
	}

	params := &stripe.RefundCreateParams{
		PaymentIntent: stripe.String(paymentIntentID),
		Amount:        stripe.Int64(amountInMinorUnit),
		Reason:        stripe.String(string(stripe.RefundReasonRequestedByCustomer)),
	}
	// 同一订单和退款金额在重试时复用 Stripe 请求，金额变化则使用新的幂等键。
	params.SetIdempotencyKey(fmt.Sprintf("re-%s-%d", req.OrderID, amountInMinorUnit))
	params.Context = ctx

	r, err := s.sc.V1Refunds.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("stripe refund: %w", err)
	}

	refundStatus := payment.ProviderStatusPending
	if r.Status == stripe.RefundStatusSucceeded {
		refundStatus = payment.ProviderStatusSuccess
	}

	return &payment.RefundResponse{
		RefundID: r.ID,
		Status:   refundStatus,
	}, nil
}

// QueryRefund 按 refund id 查询 Stripe 退款；缺少 refund id 时回退到 PaymentIntent 最新退款。
func (s *Stripe) QueryRefund(ctx context.Context, req payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	s.ensureInit()

	var r *stripe.Refund
	var err error
	if refundID := strings.TrimSpace(req.RefundID); refundID != "" {
		r, err = s.sc.V1Refunds.Retrieve(ctx, refundID, nil)
		if err != nil {
			return nil, fmt.Errorf("stripe query refund: %w", err)
		}
	} else {
		paymentIntentID := strings.TrimSpace(req.TradeNo)
		switch {
		case strings.HasPrefix(paymentIntentID, "cs_"):
			paymentIntentID, err = s.findCheckoutSessionPaymentIntentID(ctx, paymentIntentID)
			if err != nil {
				return nil, err
			}
		case strings.HasPrefix(paymentIntentID, "in_"):
			paymentIntentID, err = s.findInvoicePaymentIntentID(ctx, paymentIntentID)
			if err != nil {
				return nil, err
			}
		}
		if paymentIntentID == "" {
			return nil, fmt.Errorf("stripe query refund: missing payment intent id")
		}
		params := &stripe.RefundListParams{PaymentIntent: stripe.String(paymentIntentID)}
		params.Limit = stripe.Int64(1)
		list := s.sc.V1Refunds.List(ctx, params)
		if list.Err() != nil {
			return nil, fmt.Errorf("stripe query refund: %w", list.Err())
		}
		refunds := list.Data()
		if len(refunds) == 0 {
			return nil, fmt.Errorf("stripe query refund: no refund found")
		}
		r = refunds[0]
	}

	return &payment.RefundResponse{RefundID: r.ID, Status: stripeRefundProviderStatus(r.Status)}, nil
}

func stripeRefundProviderStatus(status stripe.RefundStatus) string {
	switch status {
	case stripe.RefundStatusSucceeded:
		return payment.ProviderStatusSuccess
	case stripe.RefundStatusFailed, stripe.RefundStatusCanceled:
		return payment.ProviderStatusFailed
	default:
		return payment.ProviderStatusPending
	}
}

func stripeIntentCurrency(raw stripe.Currency, fallback string) string {
	currency, err := payment.NormalizePaymentCurrency(string(raw))
	if err != nil || currency == payment.DefaultPaymentCurrency && strings.TrimSpace(string(raw)) == "" {
		normalizedFallback, fallbackErr := payment.NormalizePaymentCurrency(fallback)
		if fallbackErr == nil {
			return normalizedFallback
		}
		return payment.DefaultPaymentCurrency
	}
	return currency
}

// CancelPayment 对 Checkout Session 执行 expire，对 Invoice 执行 void，对旧 PaymentIntent 执行 cancel。
func (s *Stripe) CancelPayment(ctx context.Context, tradeNo string) error {
	s.ensureInit()

	tradeNo = strings.TrimSpace(tradeNo)
	if strings.HasPrefix(tradeNo, "cs_") {
		_, err := s.sc.V1CheckoutSessions.Expire(ctx, tradeNo, nil)
		if err != nil {
			return fmt.Errorf("stripe expire checkout session: %w", err)
		}
		return nil
	}
	if strings.HasPrefix(tradeNo, "in_") {
		_, err := s.sc.V1Invoices.VoidInvoice(ctx, tradeNo, nil)
		if err != nil {
			return fmt.Errorf("stripe void invoice: %w", err)
		}
		return nil
	}

	_, err := s.sc.V1PaymentIntents.Cancel(ctx, tradeNo, nil)
	if err != nil {
		return fmt.Errorf("stripe cancel payment: %w", err)
	}
	return nil
}

// GetPaymentDocument 优先返回 Stripe Invoice 链接，旧订单回退到 Charge receipt。
func (s *Stripe) GetPaymentDocument(ctx context.Context, invoiceID string, tradeNo string) (*payment.PaymentDocumentResponse, error) {
	s.ensureInit()

	invoiceID = strings.TrimSpace(invoiceID)
	tradeNo = strings.TrimSpace(tradeNo)
	if invoiceID != "" {
		params := &stripe.InvoiceRetrieveParams{}
		params.AddExpand("payments.data.payment.payment_intent")
		inv, err := s.sc.V1Invoices.Retrieve(ctx, invoiceID, params)
		if err != nil {
			return nil, fmt.Errorf("stripe get invoice document: %w", err)
		}
		return stripeInvoiceDocumentResponse(inv), nil
	}
	if strings.HasPrefix(tradeNo, "in_") {
		return s.GetPaymentDocument(ctx, tradeNo, "")
	}
	if strings.HasPrefix(tradeNo, "cs_") {
		params := &stripe.CheckoutSessionRetrieveParams{}
		params.AddExpand("invoice")
		params.AddExpand("payment_intent")
		session, err := s.sc.V1CheckoutSessions.Retrieve(ctx, tradeNo, params)
		if err != nil {
			return nil, fmt.Errorf("stripe get checkout session document: %w", err)
		}
		doc := stripeCheckoutInvoiceDocument(session)
		if strings.TrimSpace(doc.URL) != "" {
			return doc, nil
		}
		// Checkout 账单尚未生成时，回退到底层 PaymentIntent 的 receipt。
		tradeNo = stripeCheckoutTradeNo(session)
		if !strings.HasPrefix(tradeNo, "pi_") {
			return nil, fmt.Errorf("stripe checkout session document is unavailable")
		}
	}
	if tradeNo == "" {
		return nil, fmt.Errorf("stripe payment document requires invoice id or trade no")
	}

	params := &stripe.PaymentIntentRetrieveParams{}
	params.AddExpand("latest_charge")
	pi, err := s.sc.V1PaymentIntents.Retrieve(ctx, tradeNo, params)
	if err != nil {
		return nil, fmt.Errorf("stripe get receipt payment intent: %w", err)
	}
	var charge *stripe.Charge
	if pi.LatestCharge != nil && pi.LatestCharge.ID != "" {
		charge = pi.LatestCharge
		if charge.ReceiptURL == "" {
			charge, err = s.sc.V1Charges.Retrieve(ctx, pi.LatestCharge.ID, nil)
			if err != nil {
				return nil, fmt.Errorf("stripe get receipt charge: %w", err)
			}
		}
	}
	if charge == nil || strings.TrimSpace(charge.ReceiptURL) == "" {
		return nil, fmt.Errorf("stripe receipt url is unavailable")
	}
	return &payment.PaymentDocumentResponse{
		Type:       "receipt",
		URL:        charge.ReceiptURL,
		ReceiptURL: charge.ReceiptURL,
	}, nil
}

func stripeInvoiceDocumentResponse(inv *stripe.Invoice) *payment.PaymentDocumentResponse {
	if inv == nil {
		return &payment.PaymentDocumentResponse{Type: "invoice"}
	}
	url := strings.TrimSpace(inv.HostedInvoiceURL)
	if url == "" {
		url = strings.TrimSpace(inv.InvoicePDF)
	}
	return &payment.PaymentDocumentResponse{
		Type:             "invoice",
		URL:              url,
		HostedInvoiceURL: inv.HostedInvoiceURL,
		InvoicePDF:       inv.InvoicePDF,
		InvoiceID:        inv.ID,
		InvoiceStatus:    string(inv.Status),
	}
}

func stripeCheckoutTradeNo(session *stripe.CheckoutSession) string {
	if session == nil {
		return ""
	}
	if session.PaymentIntent != nil && strings.TrimSpace(session.PaymentIntent.ID) != "" {
		return strings.TrimSpace(session.PaymentIntent.ID)
	}
	return strings.TrimSpace(session.ID)
}

func stripeCheckoutMetadata(session *stripe.CheckoutSession, currency string) map[string]string {
	metadata := map[string]string{
		"currency": currency,
	}
	if session == nil {
		return metadata
	}
	if sessionID := strings.TrimSpace(session.ID); sessionID != "" {
		metadata["checkout_session_id"] = sessionID
	}
	if doc := stripeCheckoutInvoiceDocument(session); doc != nil {
		if strings.TrimSpace(doc.InvoiceID) != "" {
			metadata["invoice_id"] = strings.TrimSpace(doc.InvoiceID)
		}
		if strings.TrimSpace(doc.HostedInvoiceURL) != "" {
			metadata["invoice_url"] = strings.TrimSpace(doc.HostedInvoiceURL)
		}
		if strings.TrimSpace(doc.InvoicePDF) != "" {
			metadata["invoice_pdf"] = strings.TrimSpace(doc.InvoicePDF)
		}
		if strings.TrimSpace(doc.InvoiceStatus) != "" {
			metadata["invoice_status"] = strings.TrimSpace(doc.InvoiceStatus)
		}
	}
	return metadata
}

func stripeCheckoutInvoiceDocument(session *stripe.CheckoutSession) *payment.PaymentDocumentResponse {
	if session == nil || session.Invoice == nil {
		return &payment.PaymentDocumentResponse{Type: "invoice"}
	}
	return stripeInvoiceDocumentResponse(session.Invoice)
}

func normalizeStripeBillingInfo(req payment.CreatePaymentRequest) payment.BillingInfo {
	var billing payment.BillingInfo
	if req.BillingInfo != nil {
		billing = *req.BillingInfo
	}
	billing.Name = strings.TrimSpace(billing.Name)
	billing.Email = strings.TrimSpace(billing.Email)
	if billing.Email == "" {
		billing.Email = strings.TrimSpace(req.UserEmail)
	}
	billing.TaxIDType = strings.TrimSpace(billing.TaxIDType)
	billing.TaxID = strings.TrimSpace(billing.TaxID)
	if billing.Address != nil {
		billing.Address.Country = strings.ToUpper(strings.TrimSpace(billing.Address.Country))
		billing.Address.Line1 = strings.TrimSpace(billing.Address.Line1)
		billing.Address.Line2 = strings.TrimSpace(billing.Address.Line2)
		billing.Address.City = strings.TrimSpace(billing.Address.City)
		billing.Address.State = strings.TrimSpace(billing.Address.State)
		billing.Address.PostalCode = strings.TrimSpace(billing.Address.PostalCode)
	}
	return billing
}

func buildStripeCustomerCreateParams(req payment.CreatePaymentRequest, billing payment.BillingInfo, instanceID string) *stripe.CustomerCreateParams {
	params := &stripe.CustomerCreateParams{
		Name:        stripe.String(billing.Name),
		Email:       stripe.String(billing.Email),
		Description: stripe.String(req.Subject),
		Metadata: map[string]string{
			"orderId":            req.OrderID,
			"providerInstanceId": strings.TrimSpace(instanceID),
		},
	}
	if addr := stripeAddressParams(billing.Address); addr != nil {
		params.Address = addr
	}
	if billing.TaxIDType != "" && billing.TaxID != "" {
		params.TaxIDData = []*stripe.CustomerCreateTaxIDDataParams{{
			Type:  stripe.String(billing.TaxIDType),
			Value: stripe.String(billing.TaxID),
		}}
	}
	return params
}

func stripeAddressParams(addr *payment.BillingAddress) *stripe.AddressParams {
	if addr == nil {
		return nil
	}
	params := &stripe.AddressParams{}
	hasValue := false
	if addr.Country != "" {
		params.Country = stripe.String(addr.Country)
		hasValue = true
	}
	if addr.Line1 != "" {
		params.Line1 = stripe.String(addr.Line1)
		hasValue = true
	}
	if addr.Line2 != "" {
		params.Line2 = stripe.String(addr.Line2)
		hasValue = true
	}
	if addr.City != "" {
		params.City = stripe.String(addr.City)
		hasValue = true
	}
	if addr.State != "" {
		params.State = stripe.String(addr.State)
		hasValue = true
	}
	if addr.PostalCode != "" {
		params.PostalCode = stripe.String(addr.PostalCode)
		hasValue = true
	}
	if !hasValue {
		return nil
	}
	return params
}

func stripeInvoicePaymentIntentID(inv *stripe.Invoice) string {
	if inv == nil || inv.Payments == nil {
		return ""
	}
	for _, p := range inv.Payments.Data {
		if p == nil || p.Payment == nil || p.Payment.PaymentIntent == nil {
			continue
		}
		if id := strings.TrimSpace(p.Payment.PaymentIntent.ID); id != "" {
			return id
		}
	}
	return ""
}

func stripePaymentIntentIDFromClientSecret(clientSecret string) string {
	clientSecret = strings.TrimSpace(clientSecret)
	if !strings.HasPrefix(clientSecret, "pi_") {
		return ""
	}
	if idx := strings.Index(clientSecret, "_secret_"); idx > 0 {
		return clientSecret[:idx]
	}
	return ""
}

func (s *Stripe) findCheckoutSessionPaymentIntentID(ctx context.Context, sessionID string) (string, error) {
	params := &stripe.CheckoutSessionRetrieveParams{}
	params.AddExpand("payment_intent")
	session, err := s.sc.V1CheckoutSessions.Retrieve(ctx, sessionID, params)
	if err != nil {
		return "", fmt.Errorf("stripe retrieve checkout session: %w", err)
	}
	if session.PaymentIntent == nil {
		return "", nil
	}
	return strings.TrimSpace(session.PaymentIntent.ID), nil
}

func (s *Stripe) findInvoicePaymentIntentID(ctx context.Context, invoiceID string) (string, error) {
	params := &stripe.InvoicePaymentListParams{
		Invoice: stripe.String(invoiceID),
	}
	params.AddExpand("data.payment.payment_intent")
	list := s.sc.V1InvoicePayments.List(ctx, params)
	if err := list.Err(); err != nil {
		return "", fmt.Errorf("stripe list invoice payments: %w", err)
	}
	for _, p := range list.Data() {
		if p == nil || p.Payment == nil || p.Payment.PaymentIntent == nil {
			continue
		}
		if id := strings.TrimSpace(p.Payment.PaymentIntent.ID); id != "" {
			return id, nil
		}
	}
	return "", nil
}

// Ensure interface compliance.
var (
	_ payment.Provider                 = (*Stripe)(nil)
	_ payment.CancelableProvider       = (*Stripe)(nil)
	_ payment.DocumentProvider         = (*Stripe)(nil)
	_ payment.MerchantIdentityProvider = (*Stripe)(nil)
)
