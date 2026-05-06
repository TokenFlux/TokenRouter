package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	stripe "github.com/stripe/stripe-go/v85"
	"github.com/stripe/stripe-go/v85/webhook"
)

// Stripe constants.
const (
	stripeCurrency            = "cny"
	stripeEventPaymentSuccess = "payment_intent.succeeded"
	stripeEventPaymentFailed  = "payment_intent.payment_failed"
	stripeEventInvoicePaid    = "invoice.paid"
	stripeEventInvoiceFailed  = "invoice.payment_failed"
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
	return &Stripe{
		instanceID: instanceID,
		config:     config,
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

// stripePaymentMethodTypes maps our PaymentType to Stripe payment_method_types.
var stripePaymentMethodTypes = map[string][]string{
	payment.TypeCard:   {"card"},
	payment.TypeAlipay: {"alipay"},
	payment.TypeWxpay:  {"wechat_pay"},
	payment.TypeLink:   {"link"},
}

var stripeInvoicePaymentMethodTypes = map[string][]string{
	payment.TypeCard:   {"card"},
	payment.TypeStripe: {"card", "link", "wechat_pay"},
	payment.TypeWxpay:  {"wechat_pay"},
	payment.TypeLink:   {"link"},
}

// CreatePayment 使用 Stripe Invoice 创建可开具账单的支付单。
func (s *Stripe) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	s.ensureInit()

	amountInCents, err := payment.YuanToFen(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("stripe create payment: %w", err)
	}

	billing := normalizeStripeBillingInfo(req)
	customerParams := buildStripeCustomerCreateParams(req, billing, s.instanceID)
	customerParams.SetIdempotencyKey(fmt.Sprintf("cus-%s", req.OrderID))
	customer, err := s.sc.V1Customers.Create(ctx, customerParams)
	if err != nil {
		return nil, fmt.Errorf("stripe create customer: %w", err)
	}

	metadata := map[string]string{
		"orderId":            req.OrderID,
		"providerInstanceId": s.instanceID,
	}
	methods := resolveStripeInvoiceMethodTypes(req.InstanceSubMethods)
	invoiceParams := &stripe.InvoiceCreateParams{
		Customer:                    stripe.String(customer.ID),
		Currency:                    stripe.String(stripeCurrency),
		CollectionMethod:            stripe.String(string(stripe.InvoiceCollectionMethodChargeAutomatically)),
		AutoAdvance:                 stripe.Bool(false),
		PendingInvoiceItemsBehavior: stripe.String("exclude"),
		Description:                 stripe.String(req.Subject),
		PaymentSettings: &stripe.InvoiceCreatePaymentSettingsParams{
			PaymentMethodTypes: stripe.StringSlice(methods),
		},
		Metadata: metadata,
	}
	invoiceParams.SetIdempotencyKey(fmt.Sprintf("in-%s", req.OrderID))
	invoice, err := s.sc.V1Invoices.Create(ctx, invoiceParams)
	if err != nil {
		return nil, fmt.Errorf("stripe create invoice: %w", err)
	}

	itemParams := &stripe.InvoiceItemCreateParams{
		Amount:      stripe.Int64(amountInCents),
		Currency:    stripe.String(stripeCurrency),
		Customer:    stripe.String(customer.ID),
		Invoice:     stripe.String(invoice.ID),
		Description: stripe.String(req.Subject),
		Metadata:    metadata,
	}
	itemParams.SetIdempotencyKey(fmt.Sprintf("ii-%s", req.OrderID))
	if _, err := s.sc.V1InvoiceItems.Create(ctx, itemParams); err != nil {
		return nil, fmt.Errorf("stripe create invoice item: %w", err)
	}

	finalizeParams := &stripe.InvoiceFinalizeInvoiceParams{
		AutoAdvance: stripe.Bool(false),
	}
	finalizeParams.AddExpand("confirmation_secret")
	finalizeParams.AddExpand("payments.data.payment.payment_intent")
	finalized, err := s.sc.V1Invoices.FinalizeInvoice(ctx, invoice.ID, finalizeParams)
	if err != nil {
		return nil, fmt.Errorf("stripe finalize invoice: %w", err)
	}

	tradeNo := stripeInvoicePaymentIntentID(finalized)
	if tradeNo == "" {
		tradeNo, err = s.findInvoicePaymentIntentID(ctx, finalized.ID)
		if err != nil {
			return nil, err
		}
	}

	clientSecret := ""
	if finalized.ConfirmationSecret != nil {
		clientSecret = strings.TrimSpace(finalized.ConfirmationSecret.ClientSecret)
	}
	if clientSecret == "" {
		return nil, fmt.Errorf("stripe finalize invoice: confirmation secret is empty")
	}
	if tradeNo == "" {
		tradeNo = stripeInvoiceTradeNo(finalized, clientSecret)
	}

	return &payment.CreatePaymentResponse{
		TradeNo:       tradeNo,
		ClientSecret:  clientSecret,
		CustomerID:    customer.ID,
		InvoiceID:     finalized.ID,
		InvoiceURL:    finalized.HostedInvoiceURL,
		InvoicePDF:    finalized.InvoicePDF,
		InvoiceStatus: string(finalized.Status),
	}, nil
}

// QueryOrder 支持按 Invoice ID 查询新订单，也保留旧 PaymentIntent 订单查询。
func (s *Stripe) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	s.ensureInit()

	if strings.HasPrefix(strings.TrimSpace(tradeNo), "in_") {
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
	case stripe.PaymentIntentStatusCanceled:
		status = payment.ProviderStatusFailed
	}

	return &payment.QueryOrderResponse{
		TradeNo: pi.ID,
		Status:  status,
		Amount:  payment.FenToYuan(pi.Amount),
	}, nil
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
		Amount:  payment.FenToYuan(amount),
		Metadata: map[string]string{
			"invoice_id":     inv.ID,
			"invoice_status": string(inv.Status),
			"invoice_url":    inv.HostedInvoiceURL,
			"invoice_pdf":    inv.InvoicePDF,
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

	switch event.Type {
	case stripeEventInvoicePaid:
		return parseStripeInvoice(&event, payment.ProviderStatusSuccess, rawBody)
	case stripeEventInvoiceFailed:
		return parseStripeInvoice(&event, payment.ProviderStatusFailed, rawBody)
	case stripeEventPaymentSuccess:
		return parseStripePaymentIntent(&event, payment.ProviderStatusSuccess, rawBody)
	case stripeEventPaymentFailed:
		return parseStripePaymentIntent(&event, payment.ProviderStatusFailed, rawBody)
	}

	return nil, nil
}

func parseStripePaymentIntent(event *stripe.Event, status string, rawBody string) (*payment.PaymentNotification, error) {
	var pi stripe.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
		return nil, fmt.Errorf("stripe parse payment_intent: %w", err)
	}
	return &payment.PaymentNotification{
		TradeNo: pi.ID,
		OrderID: pi.Metadata["orderId"],
		Amount:  payment.FenToYuan(pi.Amount),
		Status:  status,
		RawData: rawBody,
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
	tradeNo := stripeInvoicePaymentIntentID(&inv)
	if tradeNo == "" {
		tradeNo = inv.ID
	}
	return &payment.PaymentNotification{
		TradeNo: tradeNo,
		OrderID: inv.Metadata["orderId"],
		Amount:  payment.FenToYuan(amount),
		Status:  status,
		RawData: rawBody,
		Metadata: map[string]string{
			"invoice_id":     inv.ID,
			"invoice_status": string(inv.Status),
			"invoice_url":    inv.HostedInvoiceURL,
			"invoice_pdf":    inv.InvoicePDF,
		},
	}, nil
}

// Refund creates a Stripe refund.
func (s *Stripe) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	s.ensureInit()

	amountInCents, err := payment.YuanToFen(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("stripe refund: %w", err)
	}

	params := &stripe.RefundCreateParams{
		PaymentIntent: stripe.String(req.TradeNo),
		Amount:        stripe.Int64(amountInCents),
		Reason:        stripe.String(string(stripe.RefundReasonRequestedByCustomer)),
	}
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

// resolveStripeMethodTypes converts instance supported_types (comma-separated)
// into Stripe API payment_method_types. Falls back to ["card"] if empty.
func resolveStripeMethodTypes(instanceSubMethods string) []string {
	if instanceSubMethods == "" {
		return []string{"card"}
	}
	var methods []string
	for _, t := range strings.Split(instanceSubMethods, ",") {
		t = strings.TrimSpace(t)
		if mapped, ok := stripePaymentMethodTypes[t]; ok {
			methods = append(methods, mapped...)
		}
	}
	if len(methods) == 0 {
		return []string{"card"}
	}
	return methods
}

// resolveStripeInvoiceMethodTypes 只返回 Invoice 支持且当前前端可安全确认的方法。
func resolveStripeInvoiceMethodTypes(instanceSubMethods string) []string {
	if strings.TrimSpace(instanceSubMethods) == "" {
		return []string{"card", "link"}
	}
	var methods []string
	seen := map[string]bool{}
	for _, t := range strings.Split(instanceSubMethods, ",") {
		t = strings.TrimSpace(t)
		if mapped, ok := stripeInvoicePaymentMethodTypes[t]; ok {
			for _, method := range mapped {
				if method == "" || seen[method] {
					continue
				}
				seen[method] = true
				methods = append(methods, method)
			}
		}
	}
	if len(methods) == 0 {
		return []string{"card"}
	}
	return methods
}

// hasStripeMethod checks if the given Stripe method list contains the target method.
func hasStripeMethod(methods []string, target string) bool {
	for _, m := range methods {
		if m == target {
			return true
		}
	}
	return false
}

// CancelPayment 对新 Invoice 订单执行 void，对旧 PaymentIntent 订单执行 cancel。
func (s *Stripe) CancelPayment(ctx context.Context, tradeNo string) error {
	s.ensureInit()

	if strings.HasPrefix(strings.TrimSpace(tradeNo), "in_") {
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

// stripeInvoiceTradeNo 按支付意图、确认密钥、账单 ID 的顺序选择可持久化的交易号。
func stripeInvoiceTradeNo(inv *stripe.Invoice, clientSecret string) string {
	if tradeNo := stripeInvoicePaymentIntentID(inv); tradeNo != "" {
		return tradeNo
	}
	if tradeNo := stripePaymentIntentIDFromClientSecret(clientSecret); tradeNo != "" {
		return tradeNo
	}
	if inv == nil {
		return ""
	}
	return strings.TrimSpace(inv.ID)
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
	_ payment.Provider           = (*Stripe)(nil)
	_ payment.CancelableProvider = (*Stripe)(nil)
	_ payment.DocumentProvider   = (*Stripe)(nil)
)
