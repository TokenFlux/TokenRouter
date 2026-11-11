package httpapi

import (
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// admin.go handles administrative payment queries, recovery actions, refunds,
// and provider configuration requests.
// Administrator payment endpoints can inspect orders, request recovery and
// manage provider configuration independently of buyer ownership checks.
// Forced expiration supplies a required reason for a local transactional
// action; refund processing can return an early force-required result before
// any prepared deduction or provider request.
//
// Date-only dashboard ends advance to the next calendar midnight, preserving
// daylight-saving date ranges for half-open paid queries. Order responses
// select display fields and retain shared optional pointers, billing maps and
// quota pointers while excluding provider merchant snapshots. Detail audit
// errors are discarded, and provider management refresh follows committed
// configuration while its errorless wrapper can keep the previous registry.
// Route registration supplies administrator access, limiting and audit
// middleware before these handlers.

// AdminHandler adapts administrative payment operations, configuration changes
// and dashboard calendar requests.
type AdminHandler struct {
	paymentService *payment.Runtime       // administrative payment runtime
	configService  *payment.ConfigService // payment configuration service
	plans          *billing.Plans         // administrative plan operations
	calendar       timezone.Calendar      // dashboard timezone date parser
}

// ForceExpireOrderRequest contains the reason required for
// administrator-forced local expiration.
type ForceExpireOrderRequest struct {
	// Reason is supplied to ForceExpireOrder for validation and mandatory audit
	// persistence. Blank or otherwise invalid reasons are rejected by lifecycle
	// before the owned expiration transaction.
	Reason string `json:"reason"`
}

// AdminPaymentOrderResult selects administrator order fields while omitting
// the merchant identity snapshot.
type AdminPaymentOrderResult struct {
	// AmountCurrency labels subscription Amount; balance quantities use the
	// site's configurable unit and therefore omit this monetary currency field.
	AmountCurrency string `json:"amount_currency,omitempty"`
	ID             int64  `json:"id"`      // local payment order identity.
	UserID         int64  `json:"user_id"` // owning buyer numeric identity.
	// stored buyer email address.
	UserEmail string `json:"user_email,omitempty"`
	// stored buyer display name.
	UserName string `json:"user_name,omitempty"`
	// UserNotes retains the order's optional buyer notes pointer for
	// administrator serialization. Nil omits the JSON field; the response shares
	// this observation with the source order.
	UserNotes *string `json:"user_notes,omitempty"`
	// credited points or plan price.
	Amount float64 `json:"amount"`
	// gateway amount including fees.
	PayAmount float64 `json:"pay_amount"`
	FeeRate   float64 `json:"fee_rate"` // applied percentage fee rate.
	// fixed gateway currency fee.
	FeeFixed float64 `json:"fee_fixed"`
	// percentage component fee amount.
	FeeRateAmount float64 `json:"fee_rate_amount"`
	// total gateway currency fees.
	FeeAmount float64 `json:"fee_amount"`
	// resolved order payment currency.
	Currency string `json:"currency"`
	// order owned redeem credential.
	RechargeCode string `json:"recharge_code,omitempty"`
	// exact merchant order number.
	OutTradeNo string `json:"out_trade_no"`
	// stored visible payment method.
	PaymentType string `json:"payment_type"`
	// stored upstream payment identifier.
	PaymentTradeNo string `json:"payment_trade_no,omitempty"`
	// PayURL, QRCode, QRCodeImg, PaymentCustomerID, PaymentInvoiceID,
	// PaymentInvoiceURL, PaymentInvoicePdfURL and PaymentInvoiceStatus retain
	// optional source order pointers for administrator serialization. Nil fields
	// are omitted, and their pointed-to text remains shared.
	//
	// payment initiation URL.
	PayURL *string `json:"pay_url,omitempty"`
	// payment QR content.
	QRCode *string `json:"qr_code,omitempty"`
	// payment QR image URL.
	QRCodeImg *string `json:"qr_code_img,omitempty"`
	// upstream customer identity.
	PaymentCustomerID *string `json:"payment_customer_id,omitempty"`
	// upstream invoice identity.
	PaymentInvoiceID *string `json:"payment_invoice_id,omitempty"`
	// hosted invoice URL.
	PaymentInvoiceURL *string `json:"payment_invoice_url,omitempty"`
	// invoice PDF URL.
	PaymentInvoicePdfURL *string `json:"payment_invoice_pdf_url,omitempty"`
	// reported invoice state.
	PaymentInvoiceStatus *string `json:"payment_invoice_status,omitempty"`
	// BillingSnapshot retains the order's invoice-request map by reference. The
	// administrator shape publishes that billing data while omitting
	// ProviderSnapshot; callers keep shared mutable values stable through
	// serialization.
	BillingSnapshot map[string]any `json:"billing_snapshot,omitempty"`
	// stored purchase category label.
	OrderType string `json:"order_type"`
	// PlanID retains the order's optional purchased plan identity pointer for
	// administrator serialization. Nil omits the JSON field; the response shares
	// this observation with the source order.
	PlanID *int64 `json:"plan_id,omitempty"`
	// PlanSnapshot copies the recorded plan value for administrator display. Its
	// optional quota pointers remain shared with the source snapshot, so this
	// response is not a deep mutable copy.
	PlanSnapshot billing.SubscriptionPlanSnapshot `json:"plan_snapshot,omitempty"`
	// ProviderInstanceID and ProviderKey publish the saved order columns and
	// retain their optional pointers. Nil values are omitted; resolving
	// historical merchant identity still uses the runtime's snapshot precedence
	// rules.
	//
	// saved provider instance column.
	ProviderInstanceID *string `json:"provider_instance_id,omitempty"`
	// saved provider key column.
	ProviderKey *string `json:"provider_key,omitempty"`
	// stored payment workflow state.
	Status string `json:"status"`
	// local recorded refund quantity.
	RefundAmount float64 `json:"refund_amount"`
	// RefundReason and RefundAt retain the optional processing reason and
	// completion-time pointers from the same order observation. Nil fields are
	// omitted, and serialization shares the pointed-to values.
	//
	// refund processing reason.
	RefundReason *string `json:"refund_reason,omitempty"`
	// refund completion time.
	RefundAt *time.Time `json:"refund_at,omitempty"`
	// recorded forced refund flag.
	ForceRefund bool `json:"force_refund,omitempty"`
	// RefundRequestedAt, RefundRequestReason and RefundRequestedBy publish the
	// saved buyer request together, retaining optional source pointers. Nil
	// values are omitted and response serialization does not copy their
	// pointed-to data.
	//
	// buyer refund request time.
	RefundRequestedAt *time.Time `json:"refund_requested_at,omitempty"`
	// buyer refund request reason.
	RefundRequestReason *string `json:"refund_request_reason,omitempty"`
	// saved request attribution.
	RefundRequestedBy *string `json:"refund_requested_by,omitempty"`
	// saved payment expiration time.
	ExpiresAt time.Time `json:"expires_at"`
	// PaidAt, CompletedAt, FailedAt and FailedReason retain independent saved
	// progress observations from the order, using its optional pointers. Nil
	// fields are omitted; the response does not infer one state from another
	// timestamp.
	//
	// payment confirmation time.
	PaidAt *time.Time `json:"paid_at,omitempty"`
	// entitlement completion time.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// recorded failure time.
	FailedAt *time.Time `json:"failed_at,omitempty"`
	// recorded failure text.
	FailedReason *string `json:"failed_reason,omitempty"`
	// stored checkout client address.
	ClientIP string `json:"client_ip,omitempty"`
	// stored checkout request host.
	SrcHost string `json:"src_host,omitempty"`
	// SrcURL retains the order's optional checkout referer URL pointer for
	// administrator serialization. Nil omits the JSON field; the response shares
	// this observation with the source order.
	SrcURL *string `json:"src_url,omitempty"`
	// local order creation time.
	CreatedAt time.Time `json:"created_at"`
	// stored order version timestamp.
	UpdatedAt time.Time `json:"updated_at"`
}

// AdminProcessRefundRequest carries amount, reason and entitlement handling
// choices for refund preparation.
type AdminProcessRefundRequest struct {
	// Amount is the local requested refund quantity. Nonpositive input selects
	// the order amount during preparation; provider gateway quantity is derived
	// separately from paid versus credited amount.
	Amount float64 `json:"amount"`
	Reason string  `json:"reason"` // refund processing reason text
	// Force permits preparation's documented insufficient-entitlement paths to
	// continue. It does not establish that recorded subscription days were fully
	// removed or that provider refund must succeed.
	Force bool `json:"force"`
	// DeductBalance chooses entitlement deduction intent for preparation,
	// including false. The workflow persists that choice in required recovery
	// evidence before the external refund call.
	DeductBalance bool `json:"deduct_balance"`
}

// AdminQueryRefundRequest carries a missing channel reference for manual recovery.
type AdminQueryRefundRequest struct {
	// RefundID is an optional Stripe refund identifier. The service verifies
	// its association with the prepared operation before recording any result.
	RefundID string `json:"refund_id,omitempty"`
}

// NewAdminHandler retains runtime, configuration, plans and calendar
// dependencies for administrator payment routes. Callers supply their access
// middleware and usable services; custom date-range handling uses the given
// calendar parser.
func NewAdminHandler(paymentService *payment.Runtime, configService *payment.ConfigService, plans *billing.Plans, calendar timezone.Calendar) *AdminHandler {
	return &AdminHandler{
		paymentService: paymentService,
		configService:  configService,
		plans:          plans,
		calendar:       calendar,
	}
}

// GetDashboard writes payment statistics for either a paired explicit date
// range or a recent calendar-day count. Any supplied start/end selects range
// parsing; missing its pair is a bad request. Otherwise valid positive days
// overrides thirty, while malformed/nonpositive text keeps that default.
func (h *AdminHandler) GetDashboard(c *gin.Context) {
	if c.Query("start_date") != "" || c.Query("end_date") != "" {
		startTime, endTime, ok := parsePaymentDashboardRange(c, h.calendar)
		if !ok {
			return
		}
		stats, err := h.paymentService.GetDashboardStatsWithRange(c.Request.Context(), startTime, endTime)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, stats)
		return
	}

	days := 30
	if d := c.Query("days"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v > 0 {
			days = v
		}
	}
	stats, err := h.paymentService.GetDashboardStats(c.Request.Context(), days)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, stats)
}

// parsePaymentDashboardRange parses both date inputs in the requested user
// timezone and reports whether the handler can continue. Missing or invalid
// input writes bad request, and the final exclusive end must follow start.
//
// A date-only end advances by one calendar day so that selected date is
// included by the half-open query. AddDate retains the location's midnight
// behavior through daylight-saving changes; datetime ends remain as parsed.
// Callers pass the two returned observations directly to dashboard range
// assembly rather than converting the interval to a fixed elapsed-day
// duration.
func parsePaymentDashboardRange(c *gin.Context, calendar timezone.Calendar) (time.Time, time.Time, bool) {
	userTZ := c.Query("timezone")
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	if startDate == "" || endDate == "" {
		response.BadRequest(c, "start_date and end_date are required")
		return time.Time{}, time.Time{}, false
	}
	startTime, _, err := calendar.ParseDateTimeInUserLocation(startDate, userTZ)
	if err != nil {
		response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD or YYYY-MM-DDTHH:mm:ss")
		return time.Time{}, time.Time{}, false
	}
	endTime, dateOnly, err := calendar.ParseDateTimeInUserLocation(endDate, userTZ)
	if err != nil {
		response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD or YYYY-MM-DDTHH:mm:ss")
		return time.Time{}, time.Time{}, false
	}
	// Include a selected date-only end through the next calendar midnight.
	// AddDate follows timezone transitions, so this interval can span
	// twenty-three or twenty-five elapsed hours for one selected date.
	if dateOnly {
		endTime = endTime.AddDate(0, 0, 1)
	}
	if !endTime.After(startTime) {
		response.BadRequest(c, "end_date must be later than start_date")
		return time.Time{}, time.Time{}, false
	}
	return startTime, endTime, true
}

// ListOrders writes a sanitized administrator page using supplied
// status/type/method/keyword filters. A valid int64 user_id is passed through;
// malformed text keeps zero for the runtime's unfiltered-user handling.
// Repository errors abort before paginated output.
func (h *AdminHandler) ListOrders(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	var userID int64
	if uid := c.Query("user_id"); uid != "" {
		// Malformed optional user text retains zero so the runtime omits that
		// positive-user filter. Parsed zero/negative values also pass through for
		// the service's own filter rule.
		if v, err := strconv.ParseInt(uid, 10, 64); err == nil {
			userID = v
		}
	}
	orders, total, err := h.paymentService.AdminListOrders(c.Request.Context(), userID, payment.OrderListParams{
		Page:        page,
		PageSize:    pageSize,
		Status:      c.Query("status"),
		OrderType:   c.Query("order_type"),
		PaymentType: c.Query("payment_type"),
		Keyword:     c.Query("keyword"),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, AdminSanitizeAdminPaymentOrdersForResponse(orders), int64(total), page, pageSize)
}

// GetOrderDetail reads the numeric order and writes its selected administrator
// fields with any returned audit history. Order read errors abort, while
// audit-read errors are discarded, so the detail response can contain an order
// with absent or partial history.
func (h *AdminHandler) GetOrderDetail(c *gin.Context) {
	orderID, ok := AdminParseIDParam(c, "id")
	if !ok {
		return
	}
	order, err := h.paymentService.GetOrderByID(c.Request.Context(), orderID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// Retain returned audit rows while discarding that secondary read error.
	// Administrator detail still publishes the successfully read order, so absent
	// history is not proof that no action occurred.
	auditLogs, _ := h.paymentService.GetOrderAuditLogs(c.Request.Context(), orderID)
	response.Success(c, gin.H{"order": AdminSanitizeAdminPaymentOrderForResponse(order), "auditLogs": auditLogs})
}

// GetOrderInvoice parses a numeric order ID and requests its administrator
// payment document from the runtime. Provider, storage and
// unavailable-document errors use shared HTTP responses; success returns the
// invoice/receipt result as supplied.
func (h *AdminHandler) GetOrderInvoice(c *gin.Context) {
	orderID, ok := AdminParseIDParam(c, "id")
	if !ok {
		return
	}
	doc, err := h.paymentService.AdminGetOrderPaymentDocument(c.Request.Context(), orderID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, doc)
}

// CancelOrder delegates the numeric order's administrator cancellation and
// writes its returned message. Parse, provider-query and conditional-close
// errors use shared HTTP responses before any success result is emitted.
func (h *AdminHandler) CancelOrder(c *gin.Context) {
	orderID, ok := AdminParseIDParam(c, "id")
	if !ok {
		return
	}
	msg, err := h.paymentService.AdminCancelOrder(c.Request.Context(), orderID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": msg})
}

// ForceExpireOrder binds the reason and delegates forced expiration for a
// numeric order. Lifecycle validates reason/status and commits the required
// local audit with expiration; it does not query or cancel the upstream
// payment in this operation.
func (h *AdminHandler) ForceExpireOrder(c *gin.Context) {
	orderID, ok := AdminParseIDParam(c, "id")
	if !ok {
		return
	}
	var req ForceExpireOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.paymentService.ForceExpireOrder(c.Request.Context(), orderID, req.Reason); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "force_expired"})
}

// RetryFulfillment delegates recovery for the numeric payment order and writes
// a completion message when the runtime returns nil. State, grant or recovery
// errors use shared HTTP responses; underlying runtime determines which stored
// states are retryable.
func (h *AdminHandler) RetryFulfillment(c *gin.Context) {
	orderID, ok := AdminParseIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.paymentService.RetryFulfillment(c.Request.Context(), orderID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "fulfillment retried"})
}

// AdminSanitizeAdminPaymentOrdersForResponse creates administrator result
// pointers in source order and skips nil orders. Each selected result has new
// outer storage, while its optional pointers/maps and snapshot quota pointers
// remain shared with the source observation.
func AdminSanitizeAdminPaymentOrdersForResponse(orders []*payment.Order) []*AdminPaymentOrderResult {
	out := make([]*AdminPaymentOrderResult, 0, len(orders))
	for _, order := range orders {
		if item := AdminSanitizeAdminPaymentOrderForResponse(order); item != nil {
			out = append(out, item)
		}
	}
	return out
}

// AdminSanitizeAdminPaymentOrderForResponse selects administrator fields and
// resolves price and payment currencies, returning nil for nil input. Merchant
// ProviderSnapshot is omitted, while BillingSnapshot, optional scalar pointers
// and PlanSnapshot quota pointers remain shared through serialization.
func AdminSanitizeAdminPaymentOrderForResponse(order *payment.Order) *AdminPaymentOrderResult {
	if order == nil {
		return nil
	}
	return &AdminPaymentOrderResult{
		AmountCurrency:       payment.PaymentOrderAmountCurrency(order),
		ID:                   order.ID,
		UserID:               order.UserID,
		UserEmail:            order.UserEmail,
		UserName:             order.UserName,
		UserNotes:            order.UserNotes,
		Amount:               order.Amount,
		PayAmount:            order.PayAmount,
		FeeRate:              order.FeeRate,
		FeeFixed:             order.FeeFixed,
		FeeRateAmount:        order.FeeRateAmount,
		FeeAmount:            order.FeeAmount,
		Currency:             payment.PaymentOrderCurrency(order),
		RechargeCode:         order.RechargeCode,
		OutTradeNo:           order.OutTradeNo,
		PaymentType:          order.PaymentType,
		PaymentTradeNo:       order.PaymentTradeNo,
		PayURL:               order.PayURL,
		QRCode:               order.QrCode,
		QRCodeImg:            order.QrCodeImg,
		PaymentCustomerID:    order.PaymentCustomerID,
		PaymentInvoiceID:     order.PaymentInvoiceID,
		PaymentInvoiceURL:    order.PaymentInvoiceURL,
		PaymentInvoicePdfURL: order.PaymentInvoicePdfURL,
		PaymentInvoiceStatus: order.PaymentInvoiceStatus,
		BillingSnapshot:      order.BillingSnapshot,
		OrderType:            order.OrderType,
		PlanID:               order.PlanID,
		PlanSnapshot:         order.PlanSnapshot,
		ProviderInstanceID:   order.ProviderInstanceID,
		ProviderKey:          order.ProviderKey,
		Status:               order.Status,
		RefundAmount:         order.RefundAmount,
		RefundReason:         order.RefundReason,
		RefundAt:             order.RefundAt,
		ForceRefund:          order.ForceRefund,
		RefundRequestedAt:    order.RefundRequestedAt,
		RefundRequestReason:  order.RefundRequestReason,
		RefundRequestedBy:    order.RefundRequestedBy,
		ExpiresAt:            order.ExpiresAt,
		PaidAt:               order.PaidAt,
		CompletedAt:          order.CompletedAt,
		FailedAt:             order.FailedAt,
		FailedReason:         order.FailedReason,
		ClientIP:             order.ClientIP,
		SrcHost:              order.SrcHost,
		SrcURL:               order.SrcURL,
		CreatedAt:            order.CreatedAt,
		UpdatedAt:            order.UpdatedAt,
	}
}

// ProcessRefund binds administrator amount/reason/force/deduction choices and
// asks the runtime to prepare a refund plan. Parse and preparation errors
// return before execution, while a nonnil early result is returned as success
// for the caller's next decision.
//
// Only a usable plan proceeds to ExecuteRefund, which owns committed
// entitlement preparation before its external provider request and later
// finalization/compensation. The handler adds no transaction around that
// workflow. Execution errors use shared HTTP responses, and returned result
// can represent success, pending progress or force-required eligibility rather
// than one universal refunded state.
func (h *AdminHandler) ProcessRefund(c *gin.Context) {
	orderID, ok := AdminParseIDParam(c, "id")
	if !ok {
		return
	}

	var req AdminProcessRefundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	plan, earlyResult, err := h.paymentService.PrepareRefund(c.Request.Context(), orderID, req.Amount, req.Reason, req.Force, req.DeductBalance)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if earlyResult != nil {
		response.Success(c, earlyResult)
		return
	}

	result, err := h.paymentService.ExecuteRefund(c.Request.Context(), plan)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// QueryAndFinalizeRefund reads the current channel outcome and applies it to
// the existing refund. The JSON body is optional for ordinary status checks.
// An administrator can supply a missing Stripe reference for verification;
// parsing a body or receiving a reference never initiates another refund.
func (h *AdminHandler) QueryAndFinalizeRefund(c *gin.Context) {
	orderID, ok := AdminParseIDParam(c, "id")
	if !ok {
		return
	}
	var req AdminQueryRefundRequest
	// Existing browser and MCP callers send no body. Preserve that entry
	// point while rejecting malformed input before accessing the channel.
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			response.BadRequest(c, "Invalid request: "+err.Error())
			return
		}
	}
	result, err := h.paymentService.QueryAndFinalizeRefund(c.Request.Context(), orderID, req.RefundID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// ListPlans delegates the administrator plan listing to billing's shared HTTP
// handler.
func (h *AdminHandler) ListPlans(c *gin.Context) {
	billinghttpapi.NewPlanHandler(h.plans).ListPlans(c)
}

// CreatePlan delegates administrator plan creation to billing's shared HTTP
// handler.
func (h *AdminHandler) CreatePlan(c *gin.Context) {
	billinghttpapi.NewPlanHandler(h.plans).CreatePlan(c)
}

// UpdatePlan delegates administrator plan updates to billing's shared HTTP
// handler.
func (h *AdminHandler) UpdatePlan(c *gin.Context) {
	billinghttpapi.NewPlanHandler(h.plans).UpdatePlan(c)
}

// DeletePlan delegates administrator plan deletion to billing's shared HTTP
// handler.
func (h *AdminHandler) DeletePlan(c *gin.Context) {
	billinghttpapi.NewPlanHandler(h.plans).DeletePlan(c)
}

// ListProviders writes administrator instance data with configuration returned
// by ConfigService. Read errors use shared HTTP responses; the service omits
// recognized sensitive keys and can return nil configuration when recoverable
// decoding fails.
func (h *AdminHandler) ListProviders(c *gin.Context) {
	providers, err := h.configService.ListProviderInstancesWithConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, providers)
}

// CreateProvider binds and persists an administrator instance request through
// ConfigService, then invokes registry refresh before responding Created.
// Persistence/validation errors abort; the refresh wrapper discards load
// errors and can retain the previous table while the saved instance remains
// persisted.
func (h *AdminHandler) CreateProvider(c *gin.Context) {
	var req payment.CreateProviderInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	inst, err := h.configService.CreateProviderInstance(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// Refresh after instance creation has persisted, allowing the registry to
	// publish current enabled rows. Its wrapper discards load errors, so Created
	// can coexist with the previous registry table.
	h.paymentService.RefreshProviders(c.Request.Context())
	response.Created(c, inst)
}

// TestProvider binds an unsaved EasyPay draft and delegates its query-only
// connectivity probe to ConfigService. It returns diagnostic result or shared
// HTTP error; no provider instance is saved by this endpoint.
func (h *AdminHandler) TestProvider(c *gin.Context) {
	var req payment.TestProviderDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	result, err := h.configService.TestProviderDraft(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// UpdateProvider parses the instance ID and persists supplied administrator
// changes before invoking registry refresh. Request/service errors abort,
// while errorless refresh can retain the prior table and still allow the saved
// instance response.
func (h *AdminHandler) UpdateProvider(c *gin.Context) {
	id, ok := AdminParseIDParam(c, "id")
	if !ok {
		return
	}
	var req payment.UpdateProviderInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	inst, err := h.configService.UpdateProviderInstance(c.Request.Context(), id, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// Refresh after the instance update succeeds so later registry reads can use
	// current configuration. Load errors are discarded by the wrapper and do not
	// undo the persisted administrator changes.
	h.paymentService.RefreshProviders(c.Request.Context())
	response.Success(c, inst)
}

// DeleteProvider delegates protected-order-aware instance deletion before
// invoking registry refresh and writing its success message. Deletion errors
// abort, while refresh load errors are discarded by the compatibility wrapper
// after persistence.
func (h *AdminHandler) DeleteProvider(c *gin.Context) {
	id, ok := AdminParseIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.configService.DeleteProviderInstance(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// Refresh after protected-order checks and deletion complete. A discarded
	// load error can retain the prior provider table even though this instance
	// has already been removed from persistence.
	h.paymentService.RefreshProviders(c.Request.Context())
	response.Success(c, gin.H{"message": "deleted"})
}

// AdminParseIDParam parses the named route parameter as decimal int64 and
// reports whether the handler can continue. Parse failure writes bad request;
// zero and negative successfully parsed IDs are passed to the owning service
// without positivity validation here.
func AdminParseIDParam(c *gin.Context, paramName string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(paramName), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid "+paramName)
		return 0, false
	}
	return id, true
}

// GetConfig writes current payment configuration for the administrator route
// using ConfigService. Repository errors use shared HTTP responses; the same
// service supplies the checkout configuration representation used by buyer
// endpoints.
func (h *AdminHandler) GetConfig(c *gin.Context) {
	cfg, err := h.configService.GetPaymentConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

// UpdateConfig binds the partial administrator settings request and delegates
// configuration persistence. Binding or service errors return through shared
// HTTP responses; success is written only after the service completes its
// update.
func (h *AdminHandler) UpdateConfig(c *gin.Context) {
	var req payment.UpdatePaymentConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.configService.UpdatePaymentConfig(c.Request.Context(), req); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "updated"})
}
