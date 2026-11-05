package httpapi

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// user.go handles buyer checkout and order operations and builds
// route-specific public recovery responses.
// Buyer endpoints pass the authenticated account identity to payment
// operations, while two public recovery endpoints accept a merchant number or
// signed resume token. Each response builder selects fields for that route;
// shared optional pointers remain attached to the original order observation,
// and public paid state comes from the stored status rather than PaidAt alone.
//
// WeChat continuation claims can replace checkout method/OpenID and selected
// purchase fields before order creation. Their signed token is parsed first,
// but applying claims can partially mutate the HTTP request before an amount
// error. Checkout page data also combines independent configuration, limit and
// sale-plan reads; plan-list errors are discarded, and official Alipay source
// resolution controls the exposed mobile-precreate preference.

// PaymentHandler adapts buyer payment requests using runtime, configuration
// and sale-plan dependencies.
type PaymentHandler struct {
	paymentService *payment.Runtime       // buyer payment runtime
	configService  *payment.ConfigService // checkout configuration service
	plans          *billing.Plans         // sale plan lookup
}

// UserCheckoutInfoResponse combines selected checkout configuration, available
// method limits and sale-plan displays.
type UserCheckoutInfoResponse struct {
	// Methods receives the available-limit map from ConfigService directly. This
	// response does not clone that map; callers keep its values stable through
	// JSON serialization.
	Methods   map[string]payment.MethodLimits `json:"methods"`
	GlobalMin float64                         `json:"global_min"` // global minimum purchase amount
	GlobalMax float64                         `json:"global_max"` // global maximum purchase amount
	// Plans is a newly assembled sale display slice. Its group collections are
	// copied, while each optional quota/original-price pointer retains the sale
	// plan's observation.
	Plans []UserCheckoutPlan `json:"plans"`
	// balance purchase disabled flag.
	BalanceDisabled bool `json:"balance_disabled"`
	// effective credited points per principal.
	BalanceRechargeMultiplier float64 `json:"balance_recharge_multiplier"`
	// normalized global recharge rate.
	DefaultBalanceRechargeMultiplier float64 `json:"default_balance_recharge_multiplier"`
	// UserBalanceRechargeMultiplier is null when the buyer follows global terms.
	// A supplied value remains explicit even when it equals the global rate.
	UserBalanceRechargeMultiplier *float64 `json:"user_balance_recharge_multiplier"`
	// CNY per subscription USD.
	SubscriptionUSDToCNYRate float64 `json:"subscription_usd_to_cny_rate"`
	// legacy recharge percentage rate.
	RechargeFeeRate float64 `json:"recharge_fee_rate"`
	// MethodFees receives the configuration map directly. The response shares
	// that map with the returned configuration, so handlers treat it as immutable
	// during serialization.
	MethodFees   payment.MethodFeeSettings `json:"method_fees"`
	HelpText     string                    `json:"help_text"`      // checkout help display text
	HelpImageURL string                    `json:"help_image_url"` // checkout help image URL
	// Stripe public browser key.
	StripePublishableKey string `json:"stripe_publishable_key"`
	// AlipayForceQRCode passes the saved QR preference to PaymentView. Its
	// payload builder sends is_mobile=false for Alipay when this is true and
	// mobile-precreate preference is absent.
	AlipayForceQRCode bool `json:"alipay_force_qrcode"`
	// AlipayMobilePrecreateDeepLink is exposed only when the setting is enabled
	// and current Alipay source resolves to official Alipay. Resolution errors
	// abort GetCheckoutInfo.
	AlipayMobilePrecreateDeepLink bool `json:"alipay_mobile_precreate_deep_link"`
}

// UserCheckoutPlan is the sale-plan display assembled for the combined
// checkout endpoint.
type UserCheckoutPlan struct {
	ID int64 `json:"id"` // sale plan numeric identity
	// GroupID is an optional legacy group field in this response shape.
	// GetCheckoutInfo leaves it unset while publishing the plan's GroupIDs
	// collection.
	GroupID *int64 `json:"group_id,omitempty"`
	// GroupIDs is copied into a new slice from the sale plan. The handler
	// publishes all returned group memberships without retaining the original
	// slice storage.
	GroupIDs []int64 `json:"group_ids"`
	// GroupRateMultipliers is copied into a writable map through
	// UserCloneInt64Float64Map. Missing or empty input yields an allocated map
	// for the response object.
	GroupRateMultipliers map[int64]float64 `json:"group_rate_multipliers"`
	// GroupName is an optional legacy display field retained in this response
	// shape. GetCheckoutInfo leaves it empty and therefore omits it from JSON.
	GroupName string `json:"group_name,omitempty"`
	//
	// DailyLimitUSD, WeeklyLimitUSD and MonthlyLimitUSD retain the sale plan's
	// optional quota pointers for serialization. Nil remains unlimited under plan
	// quota rules; the handler does not copy these pointed-to values.
	DailyLimitUSD *float64 `json:"daily_limit_usd"` // daily USD quota
	//
	WeeklyLimitUSD *float64 `json:"weekly_limit_usd"` // weekly USD quota
	//
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd"` // monthly USD quota
	// ModelScopes is the optional supported-model display collection in this
	// shape. GetCheckoutInfo does not populate it, so its empty value is omitted
	// from JSON.
	ModelScopes []string `json:"supported_model_scopes,omitempty"`
	Name        string   `json:"name"`        // localized sale plan name
	Description string   `json:"description"` // localized sale plan description
	Price       float64  `json:"price"`       // listed sale purchase price
	// OriginalPrice is an optional display price retained by pointer from the
	// plan. The handler serializes the supplied value without cloning that
	// pointed-to number.
	OriginalPrice *float64 `json:"original_price,omitempty"`
	Currency      string   `json:"currency,omitempty"` // listed sale price currency
	// configured validity interval quantity.
	ValidityDays int `json:"validity_days"`
	// configured validity interval unit.
	ValidityUnit string `json:"validity_unit"`
	// Features contains newly parsed nonblank display lines from the sale plan's
	// text. UserParseFeatures allocates the returned slice, including its empty
	// array representation.
	Features    []string `json:"features"`
	ProductName string   `json:"product_name"` // plan payment product name
}

// CreateOrderRequest carries buyer checkout input before optional signed
// WeChat continuation is applied.
type CreateOrderRequest struct {
	// Amount is the requested balance principal unless signed continuation
	// supplies its nonblank amount. Subscription checkout derives purchase amount
	// from the selected plan later.
	Amount float64 `json:"amount"`
	// PaymentType is required by JSON binding before continuation handling.
	// Signed WeChat claims can normalize and replace it only after their method
	// comparison succeeds.
	PaymentType string `json:"payment_type" binding:"required"`
	// OpenID supplies the WeChat payer identity for checkout. A parsed
	// continuation with nonblank OpenID replaces the request value before the
	// runtime selects its provider.
	OpenID string `json:"openid"`
	// WechatResumeToken is parsed when nonblank and supplies signed
	// method/payer/purchase context. Invalid tokens return an error before
	// runtime order creation.
	WechatResumeToken string `json:"wechat_resume_token"`
	ReturnURL         string `json:"return_url"` // requested payment return URL
	// requested provider source label.
	PaymentSource string `json:"payment_source"`
	// OrderType selects balance or subscription checkout in the runtime. A
	// nonempty continuation order type replaces the request field before that
	// validation.
	OrderType string `json:"order_type"`
	// PlanID identifies the selected subscription plan. A positive continuation
	// plan ID replaces this field; nonpositive claim IDs retain the request
	// value.
	PlanID int64 `json:"plan_id"`
	// BillingInfo is an optional invoice request passed directly to runtime
	// checkout. The HTTP adapter keeps its pointer and does not clone the
	// supplied address fields.
	BillingInfo *payment.BillingInfo `json:"billing_info"`
	// IsMobile overrides the User-Agent heuristic when supplied, including false.
	// A nil pointer leaves the detected mobile flag passed to runtime checkout.
	IsMobile *bool `json:"is_mobile,omitempty"`
}

// RefundRequestBody carries the buyer's reason for a later administrator
// refund decision.
type RefundRequestBody struct {
	Reason string `json:"reason"` // buyer refund request reason
}

// VerifyOrderRequest carries an exact merchant order number for buyer or
// public verification.
type VerifyOrderRequest struct {
	// exact merchant order number.
	OutTradeNo string `json:"out_trade_no" binding:"required"`
}

// ResolveOrderByResumeTokenRequest carries the signed session credential for
// public order resolution.
type ResolveOrderByResumeTokenRequest struct {
	// signed payment session token.
	ResumeToken string `json:"resume_token" binding:"required"`
}

// PublicOrderResult exposes selected order and refund fields after signed
// public session resolution.
type PublicOrderResult struct {
	// AmountCurrency labels subscription Amount; balance quantities use the
	// site's configurable unit and therefore omit this monetary currency field.
	AmountCurrency string  `json:"amount_currency,omitempty"`
	ID             int64   `json:"id"`           // local payment order identity
	OutTradeNo     string  `json:"out_trade_no"` // exact merchant order number
	Amount         float64 `json:"amount"`       // credited points or plan price
	PayAmount      float64 `json:"pay_amount"`   // gateway amount including fees
	FeeRate        float64 `json:"fee_rate"`     // applied percentage fee rate
	FeeFixed       float64 `json:"fee_fixed"`    // fixed gateway currency fee
	// percentage component fee amount.
	FeeRateAmount float64   `json:"fee_rate_amount"`
	FeeAmount     float64   `json:"fee_amount"`   // total gateway currency fees
	Currency      string    `json:"currency"`     // resolved order payment currency
	PaymentType   string    `json:"payment_type"` // stored visible payment method
	OrderType     string    `json:"order_type"`   // stored purchase category label
	Status        string    `json:"status"`       // stored payment workflow state
	CreatedAt     time.Time `json:"created_at"`   // local order creation time
	ExpiresAt     time.Time `json:"expires_at"`   // saved payment expiration time
	// PaidAt retains the order's optional payment-confirmation pointer. Response
	// serialization shares that observation and does not itself infer or update
	// payment state.
	PaidAt *time.Time `json:"paid_at,omitempty"`
	// CompletedAt retains the order's optional entitlement-completion pointer. It
	// describes stored fulfillment progress and remains shared with the source
	// order observation.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// local recorded refund quantity.
	RefundAmount float64 `json:"refund_amount"`
	// RefundReason retains the source order's optional processing reason pointer.
	// The response publishes its saved text while its nil value omits the JSON
	// field.
	RefundReason *string `json:"refund_reason,omitempty"`
	// RefundRequestedAt retains the source order's optional buyer-request
	// timestamp pointer. It describes the saved request and remains shared
	// through response serialization.
	RefundRequestedAt *time.Time `json:"refund_requested_at,omitempty"`
	// RefundRequestedBy retains the source order's optional requester label
	// pointer. It publishes saved request attribution and remains shared through
	// response serialization.
	RefundRequestedBy *string `json:"refund_requested_by,omitempty"`
	// RefundRequestReason retains the source order's optional buyer reason
	// pointer. It publishes saved request text and remains shared through
	// response serialization.
	RefundRequestReason *string `json:"refund_request_reason,omitempty"`
	// PlanID retains the source order's optional plan identity pointer.
	// Balance-like orders can leave it nil; response construction does not copy
	// the pointed-to value.
	PlanID *int64 `json:"plan_id,omitempty"`
}

// PublicOrderVerifyResult exposes minimal saved state for anonymous
// merchant-number compatibility lookup.
type PublicOrderVerifyResult struct {
	OutTradeNo string `json:"out_trade_no"` // exact merchant order number
	Status     string `json:"status"`       // stored payment workflow state
	// Paid is classified from Status by UserPublicOrderStatusPaid. FAILED is
	// false even when PaidAt exists; confirmed refund workflow states remain
	// true.
	Paid      bool      `json:"paid"`
	CreatedAt time.Time `json:"created_at"` // local order creation time
	ExpiresAt time.Time `json:"expires_at"` // saved payment expiration time
	// PaidAt retains the order's optional payment-confirmation pointer. Response
	// serialization shares that observation and does not itself infer or update
	// payment state.
	PaidAt *time.Time `json:"paid_at,omitempty"`
	// CompletedAt retains the order's optional entitlement-completion pointer. It
	// describes stored fulfillment progress and remains shared with the source
	// order observation.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// PaymentOrderResult exposes selected persisted order fields to an
// authenticated buyer.
type PaymentOrderResult struct {
	// AmountCurrency labels subscription Amount; balance quantities use the
	// site's configurable unit and therefore omit this monetary currency field.
	AmountCurrency string  `json:"amount_currency,omitempty"`
	ID             int64   `json:"id"`         // local payment order identity
	UserID         int64   `json:"user_id"`    // owning buyer numeric identity
	Amount         float64 `json:"amount"`     // credited points or plan price
	PayAmount      float64 `json:"pay_amount"` // gateway amount including fees
	FeeRate        float64 `json:"fee_rate"`   // applied percentage fee rate
	FeeFixed       float64 `json:"fee_fixed"`  // fixed gateway currency fee
	// percentage component fee amount.
	FeeRateAmount float64   `json:"fee_rate_amount"`
	FeeAmount     float64   `json:"fee_amount"`   // total gateway currency fees
	Currency      string    `json:"currency"`     // resolved order payment currency
	PaymentType   string    `json:"payment_type"` // stored visible payment method
	OutTradeNo    string    `json:"out_trade_no"` // exact merchant order number
	Status        string    `json:"status"`       // stored payment workflow state
	OrderType     string    `json:"order_type"`   // stored purchase category label
	CreatedAt     time.Time `json:"created_at"`   // local order creation time
	ExpiresAt     time.Time `json:"expires_at"`   // saved payment expiration time
	// PaidAt retains the order's optional payment-confirmation pointer. Response
	// serialization shares that observation and does not itself infer or update
	// payment state.
	PaidAt *time.Time `json:"paid_at,omitempty"`
	// CompletedAt retains the order's optional entitlement-completion pointer. It
	// describes stored fulfillment progress and remains shared with the source
	// order observation.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// local recorded refund quantity.
	RefundAmount float64 `json:"refund_amount"`
	// RefundReason retains the source order's optional processing reason pointer.
	// The response publishes its saved text while its nil value omits the JSON
	// field.
	RefundReason *string `json:"refund_reason,omitempty"`
	// RefundRequestedAt retains the source order's optional buyer-request
	// timestamp pointer. It describes the saved request and remains shared
	// through response serialization.
	RefundRequestedAt *time.Time `json:"refund_requested_at,omitempty"`
	// RefundRequestedBy retains the source order's optional requester label
	// pointer. It publishes saved request attribution and remains shared through
	// response serialization.
	RefundRequestedBy *string `json:"refund_requested_by,omitempty"`
	// RefundRequestReason retains the source order's optional buyer reason
	// pointer. It publishes saved request text and remains shared through
	// response serialization.
	RefundRequestReason *string `json:"refund_request_reason,omitempty"`
	// PlanID retains the source order's optional plan identity pointer.
	// Balance-like orders can leave it nil; response construction does not copy
	// the pointed-to value.
	PlanID *int64 `json:"plan_id,omitempty"`
	// ProviderInstanceID exposes the saved instance column for buyer eligibility
	// display. Its pointer is shared from the order and does not expose provider
	// credentials or merchant snapshot fields.
	ProviderInstanceID *string `json:"provider_instance_id,omitempty"`
}

// NewPaymentHandler retains the supplied runtime, configuration and plan
// services for buyer endpoints. Callers mount its handlers with authentication
// guards and supply the dependencies required by each invoked operation.
func NewPaymentHandler(paymentService *payment.Runtime, configService *payment.ConfigService, plans *billing.Plans) *PaymentHandler {
	return &PaymentHandler{
		paymentService: paymentService,
		configService:  configService,
		plans:          plans,
	}
}

// GetPaymentConfig writes the current checkout configuration returned by
// ConfigService. Read errors use the shared HTTP error response; the route's
// supplied buyer middleware controls access before this handler.
func (h *PaymentHandler) GetPaymentConfig(c *gin.Context) {
	cfg, err := h.configService.GetPaymentConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

// GetPlans delegates the buyer sale-plan response to billing's plan handler.
func (h *PaymentHandler) GetPlans(c *gin.Context) {
	billinghttpapi.NewPlanHandler(h.plans).GetPlans(c)
}

// GetCheckoutInfo assembles available method limits, checkout configuration
// and sale-plan displays for the authenticated buyer. Limit, configuration and
// buyer lookup errors abort the request, while the sale-plan read error is discarded.
//
// The Alipay mobile preference requires its setting and a current
// official-source resolution; source errors remain visible. Plan group
// slices/rate maps are copied, while quota/original-price pointers remain
// shared for serialization. Separate configuration and limit reads can observe
// different updates, and a failed plan list can leave an empty or partial
// display list rather than failing this combined endpoint. Recharge terms report
// the default and explicit personal rate alongside the effective credit rate.
func (h *PaymentHandler) GetCheckoutInfo(c *gin.Context) {
	subject, ok := UserRequireAuth(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	limitsResp, err := h.configService.GetAvailableMethodLimits(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	cfg, err := h.configService.GetPaymentConfig(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	rates, err := h.paymentService.GetBalanceRechargeRates(ctx, subject.UserID, cfg.BalanceRechargeMultiplier)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	alipayMobilePrecreateDeepLink := false
	if cfg.AlipayMobilePrecreateDeepLink {
		alipayMobilePrecreateDeepLink, err = h.configService.UsesOfficialAlipayVisibleMethod(ctx)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}

	// Keep any returned sale plans while discarding the list error so
	// configuration and limits can still reach checkout. Missing results then
	// yield an empty plan display list.
	plans, _ := h.plans.ListPlansForSale(ctx)
	planList := make([]UserCheckoutPlan, 0, len(plans))
	for _, p := range plans {
		planList = append(planList, UserCheckoutPlan{
			ID:                   p.ID,
			DailyLimitUSD:        p.DailyLimitUSD,
			WeeklyLimitUSD:       p.WeeklyLimitUSD,
			MonthlyLimitUSD:      p.MonthlyLimitUSD,
			Name:                 p.Name,
			Description:          p.Description,
			Price:                p.Price,
			OriginalPrice:        p.OriginalPrice,
			Currency:             p.Currency,
			ValidityDays:         p.ValidityDays,
			ValidityUnit:         p.ValidityUnit,
			GroupIDs:             append([]int64(nil), p.GroupIDs...),
			GroupRateMultipliers: UserCloneInt64Float64Map(p.GroupRateMultipliers),
			Features:             UserParseFeatures(p.Features),
			ProductName:          p.ProductName,
		})
	}

	response.Success(c, UserCheckoutInfoResponse{
		Methods:                          limitsResp.Methods,
		GlobalMin:                        limitsResp.GlobalMin,
		GlobalMax:                        limitsResp.GlobalMax,
		Plans:                            planList,
		BalanceDisabled:                  cfg.BalanceDisabled,
		BalanceRechargeMultiplier:        rates.Effective,
		DefaultBalanceRechargeMultiplier: rates.Default,
		UserBalanceRechargeMultiplier:    rates.User,
		SubscriptionUSDToCNYRate:         cfg.SubscriptionUSDToCNYRate,
		RechargeFeeRate:                  cfg.RechargeFeeRate,
		MethodFees:                       cfg.MethodFees,
		HelpText:                         cfg.HelpText,
		HelpImageURL:                     cfg.HelpImageURL,
		StripePublishableKey:             cfg.StripePublishableKey,
		AlipayForceQRCode:                cfg.AlipayForceQRCode,
		AlipayMobilePrecreateDeepLink:    alipayMobilePrecreateDeepLink,
	})
}

// UserParseFeatures delegates newline feature parsing to billing's shared
// display helper. It returns trimmed nonempty entries in order, using an
// allocated empty slice for empty input so JSON remains an array.
func UserParseFeatures(raw string) []string { return billinghttpapi.ParsePlanFeatures(raw) }

// UserCloneInt64Float64Map delegates scalar rate copying to billing's offer
// helper. It returns independent map storage, including an allocated empty map
// for nil/empty input, so response mutations do not change plan rates.
func UserCloneInt64Float64Map(in map[int64]float64) map[int64]float64 {
	return billinghttpapi.ClonePlanOfferRates(in)
}

// GetLimits writes method availability and limits derived by ConfigService
// from current eligible provider instances. Configuration/read errors use the
// shared HTTP error response, and the returned map is serialized as supplied.
func (h *PaymentHandler) GetLimits(c *gin.Context) {
	resp, err := h.configService.GetAvailableMethodLimits(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, resp)
}

// CreateOrder binds buyer JSON, optionally applies parsed WeChat continuation
// claims, and delegates runtime checkout with the authenticated user ID.
// Missing authentication, binding errors or token/application errors return
// before order creation.
//
// Signed continuation can replace payer/method/purchase fields but retains the
// authenticated account as owner. An optional IsMobile value wins over header
// detection; WeChat browser, client address, host, referer and language come
// from the HTTP request. Runtime checkout validates return URL, provider and
// purchase state, and the handler returns either its order response or its
// OAuth-required response under the shared error policy.
func (h *PaymentHandler) CreateOrder(c *gin.Context) {
	subject, ok := UserRequireAuth(c)
	if !ok {
		return
	}

	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if strings.TrimSpace(req.WechatResumeToken) != "" {
		claims, err := h.paymentService.ParseWeChatPaymentResumeToken(req.WechatResumeToken)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if err := UserApplyWeChatPaymentResumeClaims(&req, claims); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}

	// Use header detection only when the client omitted IsMobile. A supplied
	// false remains authoritative, so payment presentation need not follow
	// browser token heuristics in that case.
	mobile := UserIsMobile(c)
	if req.IsMobile != nil {
		mobile = *req.IsMobile
	}
	result, err := h.paymentService.CreateOrder(c.Request.Context(), payment.CreateOrderRequest{
		UserID:          subject.UserID,
		Amount:          req.Amount,
		PaymentType:     req.PaymentType,
		OpenID:          req.OpenID,
		ClientIP:        c.ClientIP(),
		IsMobile:        mobile,
		IsWeChatBrowser: UserIsWeChatBrowser(c),
		SrcHost:         c.Request.Host,
		SrcURL:          c.Request.Referer(),
		ReturnURL:       req.ReturnURL,
		PaymentSource:   req.PaymentSource,
		OrderType:       req.OrderType,
		PlanID:          req.PlanID,
		BillingInfo:     req.BillingInfo,
		Locale:          c.GetHeader("Accept-Language"),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// UserApplyWeChatPaymentResumeClaims applies a previously parsed continuation
// to a nonnil request after checking nonblank OpenID and normalized method
// compatibility. A blank normalized claim method defaults to WeChat, and a
// request method that normalizes empty does not trigger mismatch.
//
// Method and OpenID are written before optional amount parsing, so an invalid
// amount can leave those two fields changed when the error returns. Amount
// must parse and compare positive, but this helper adds no finite-value check.
// Nonempty order type and positive plan ID then replace those purchase fields;
// token signature/expiry validation belongs to the caller's parser.
func UserApplyWeChatPaymentResumeClaims(req *CreateOrderRequest, claims *payment.WeChatPaymentResumeClaims) error {
	if req == nil || claims == nil {
		return infraerrors.BadRequest("INVALID_WECHAT_PAYMENT_RESUME_TOKEN", "wechat payment resume context is missing")
	}
	openid := strings.TrimSpace(claims.OpenID)
	if openid == "" {
		return infraerrors.BadRequest("INVALID_WECHAT_PAYMENT_RESUME_TOKEN", "wechat payment resume token missing openid")
	}

	paymentType := payment.NormalizeVisibleMethod(claims.PaymentType)
	if paymentType == "" {
		paymentType = payment.TypeWxpay
	}
	if req.PaymentType != "" {
		requestPaymentType := payment.NormalizeVisibleMethod(req.PaymentType)
		if requestPaymentType != "" && requestPaymentType != paymentType {
			return infraerrors.BadRequest("INVALID_WECHAT_PAYMENT_RESUME_TOKEN", "wechat payment resume token payment type mismatch")
		}
	}
	// Apply the accepted payer and method before optional purchase values. Amount
	// parsing can still fail afterward, so callers treat this request as
	// partially modified on that error.
	req.PaymentType = paymentType
	req.OpenID = openid

	if strings.TrimSpace(claims.Amount) != "" {
		amount, err := strconv.ParseFloat(strings.TrimSpace(claims.Amount), 64)
		if err != nil || amount <= 0 {
			return infraerrors.BadRequest("INVALID_WECHAT_PAYMENT_RESUME_TOKEN", fmt.Sprintf("invalid resume amount: %s", claims.Amount))
		}
		req.Amount = amount
	}
	if claims.OrderType != "" {
		req.OrderType = claims.OrderType
	}
	if claims.PlanID > 0 {
		req.PlanID = claims.PlanID
	}
	return nil
}

// GetMyOrders lists selected order filters for the authenticated buyer and
// writes sanitized paginated values. Missing authentication returns
// immediately; repository errors use the shared HTTP response before
// count/page data is emitted.
func (h *PaymentHandler) GetMyOrders(c *gin.Context) {
	subject, ok := UserRequireAuth(c)
	if !ok {
		return
	}

	page, pageSize := response.ParsePagination(c)
	orders, total, err := h.paymentService.GetUserOrders(c.Request.Context(), subject.UserID, payment.OrderListParams{
		Page:        page,
		PageSize:    pageSize,
		Status:      c.Query("status"),
		OrderType:   c.Query("order_type"),
		PaymentType: c.Query("payment_type"),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, UserSanitizePaymentOrdersForResponse(orders), int64(total), page, pageSize)
}

// GetOrder parses a numeric route ID and writes the authenticated buyer's
// selected order fields. Parse errors return bad request, while runtime
// ownership/read errors use the shared HTTP response.
func (h *PaymentHandler) GetOrder(c *gin.Context) {
	subject, ok := UserRequireAuth(c)
	if !ok {
		return
	}

	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	order, err := h.paymentService.GetOrder(c.Request.Context(), orderID, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, UserSanitizePaymentOrderForResponse(order))
}

// GetOrderInvoice parses a numeric order ID and asks the runtime for the
// authenticated buyer's payment document. Runtime ownership, provider and
// document errors remain HTTP errors; successful invoice/receipt data is
// serialized as returned.
func (h *PaymentHandler) GetOrderInvoice(c *gin.Context) {
	subject, ok := UserRequireAuth(c)
	if !ok {
		return
	}

	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	doc, err := h.paymentService.GetOrderPaymentDocument(c.Request.Context(), orderID, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, doc)
}

// CancelOrder delegates cancellation of the authenticated buyer's numeric
// order and writes its returned message. Parse, ownership, provider-query and
// close errors use the shared HTTP policy before a success response.
func (h *PaymentHandler) CancelOrder(c *gin.Context) {
	subject, ok := UserRequireAuth(c)
	if !ok {
		return
	}

	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	msg, err := h.paymentService.CancelOrder(c.Request.Context(), orderID, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": msg})
}

// RequestRefund binds a buyer reason and delegates refund-request eligibility
// for the authenticated owner's numeric order. It writes request success after
// the conditional local update; actual provider refund processing belongs to
// the later administrator operation.
func (h *PaymentHandler) RequestRefund(c *gin.Context) {
	subject, ok := UserRequireAuth(c)
	if !ok {
		return
	}

	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid order ID")
		return
	}

	var req RefundRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if err := h.paymentService.RequestRefund(c.Request.Context(), orderID, subject.UserID, req.Reason); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "refund requested"})
}

// GetRefundEligibleProviders writes IDs whose administrative and user-refund
// flags are both set, independently of instance Enabled state. Read errors use
// the shared HTTP response; refund execution separately validates each order's
// historical binding and eligibility.
func (h *PaymentHandler) GetRefundEligibleProviders(c *gin.Context) {
	ids, err := h.configService.GetUserRefundEligibleInstanceIDs(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"provider_instance_ids": ids})
}

// VerifyOrder binds an exact merchant number and asks the runtime to verify
// payment for the authenticated buyer. The runtime can query its bound
// provider and resume fulfillment; the handler then emits selected order
// fields or its returned error.
func (h *PaymentHandler) VerifyOrder(c *gin.Context) {
	subject, ok := UserRequireAuth(c)
	if !ok {
		return
	}

	var req VerifyOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	order, err := h.paymentService.VerifyOrderByOutTradeNo(c.Request.Context(), req.OutTradeNo, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, UserSanitizePaymentOrderForResponse(order))
}

// UserBuildPublicOrderResult builds selected public order/refund fields from a
// nonnil order after token resolution. Price and payment currencies are
// resolved separately; optional pointers remain shared with the source observation and
// credentials/merchant snapshot fields are omitted from this shape.
func UserBuildPublicOrderResult(order *payment.Order) PublicOrderResult {
	return PublicOrderResult{
		AmountCurrency:      payment.PaymentOrderAmountCurrency(order),
		ID:                  order.ID,
		OutTradeNo:          order.OutTradeNo,
		Amount:              order.Amount,
		PayAmount:           order.PayAmount,
		FeeRate:             order.FeeRate,
		FeeFixed:            order.FeeFixed,
		FeeRateAmount:       order.FeeRateAmount,
		FeeAmount:           order.FeeAmount,
		Currency:            payment.PaymentOrderCurrency(order),
		PaymentType:         order.PaymentType,
		OrderType:           order.OrderType,
		Status:              order.Status,
		CreatedAt:           order.CreatedAt,
		ExpiresAt:           order.ExpiresAt,
		PaidAt:              order.PaidAt,
		CompletedAt:         order.CompletedAt,
		RefundAmount:        order.RefundAmount,
		RefundReason:        order.RefundReason,
		RefundRequestedAt:   order.RefundRequestedAt,
		RefundRequestedBy:   order.RefundRequestedBy,
		RefundRequestReason: order.RefundRequestReason,
		PlanID:              order.PlanID,
	}
}

// UserBuildPublicOrderVerifyResult builds minimal anonymous saved state from a
// nonnil order. Paid is determined by UserPublicOrderStatusPaid rather than
// timestamp presence; optional confirmation/completion pointers remain shared
// with the source observation.
func UserBuildPublicOrderVerifyResult(order *payment.Order) PublicOrderVerifyResult {
	return PublicOrderVerifyResult{
		OutTradeNo:  order.OutTradeNo,
		Status:      order.Status,
		Paid:        UserPublicOrderStatusPaid(order.Status),
		CreatedAt:   order.CreatedAt,
		ExpiresAt:   order.ExpiresAt,
		PaidAt:      order.PaidAt,
		CompletedAt: order.CompletedAt,
	}
}

// UserPublicOrderStatusPaid reports confirmed and refund workflow states as
// paid for anonymous display. Pending, processing, failed, cancelled, expired
// and unknown states return false, independently of any PaidAt value.
func UserPublicOrderStatusPaid(status string) bool {
	switch status {
	case payment.OrderStatusPaid,
		payment.OrderStatusRecharging,
		payment.OrderStatusCompleted,
		payment.OrderStatusRefundRequested,
		payment.OrderStatusRefunding,
		payment.OrderStatusRefundPending,
		payment.OrderStatusPartiallyRefunded,
		payment.OrderStatusRefunded,
		payment.OrderStatusRefundFailed:
		return true
	default:
		return false
	}
}

// VerifyOrderPublic binds the merchant number for anonymous compatibility
// lookup and writes minimal saved state. It calls the runtime's saved-state
// operation without buyer ownership authentication or active provider
// verification; input/read errors use shared HTTP responses.
func (h *PaymentHandler) VerifyOrderPublic(c *gin.Context) {
	var req VerifyOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	order, err := h.paymentService.VerifyOrderPublic(c.Request.Context(), req.OutTradeNo)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, UserBuildPublicOrderVerifyResult(order))
}

// ResolveOrderPublicByResumeToken binds a signed session token and asks the
// runtime to resolve its order. Token, identity and read errors use shared
// HTTP responses; success emits the selected public order/refund shape.
func (h *PaymentHandler) ResolveOrderPublicByResumeToken(c *gin.Context) {
	var req ResolveOrderByResumeTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	order, err := h.paymentService.GetPublicOrderByResumeToken(c.Request.Context(), req.ResumeToken)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, UserBuildPublicOrderResult(order))
}

// UserRequireAuth reads the authenticated subject from Gin context and reports
// whether it exists. Absence writes Unauthorized and returns false, so callers
// stop before using the returned user identity.
func UserRequireAuth(c *gin.Context) (middleware2.AuthSubject, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return middleware2.AuthSubject{}, false
	}
	return subject, true
}

// UserIsMobile reports whether the lowercased User-Agent contains a listed
// mobile device token.
func UserIsMobile(c *gin.Context) bool {
	ua := strings.ToLower(c.GetHeader("User-Agent"))
	for _, kw := range []string{"mobile", "android", "iphone", "ipad", "ipod"} {
		if strings.Contains(ua, kw) {
			return true
		}
	}
	return false
}

// UserSanitizePaymentOrdersForResponse builds buyer display values in order
// and skips nil source orders. The outer slice is new, while optional pointers
// in each selected result remain shared with its source observation.
func UserSanitizePaymentOrdersForResponse(orders []*payment.Order) []PaymentOrderResult {
	out := make([]PaymentOrderResult, 0, len(orders))
	for _, order := range orders {
		if item := UserSanitizePaymentOrderForResponse(order); item != nil {
			out = append(out, *item)
		}
	}
	return out
}

// UserSanitizePaymentOrderForResponse selects buyer-visible fields from an
// order, returning nil for nil input. Price and payment currencies are resolved
// separately, and optional timestamps/reasons/IDs remain shared; this is field selection
// rather than a deep copy of mutable values.
func UserSanitizePaymentOrderForResponse(order *payment.Order) *PaymentOrderResult {
	if order == nil {
		return nil
	}
	return &PaymentOrderResult{
		AmountCurrency:      payment.PaymentOrderAmountCurrency(order),
		ID:                  order.ID,
		UserID:              order.UserID,
		Amount:              order.Amount,
		PayAmount:           order.PayAmount,
		FeeRate:             order.FeeRate,
		FeeFixed:            order.FeeFixed,
		FeeRateAmount:       order.FeeRateAmount,
		FeeAmount:           order.FeeAmount,
		Currency:            payment.PaymentOrderCurrency(order),
		PaymentType:         order.PaymentType,
		OutTradeNo:          order.OutTradeNo,
		Status:              order.Status,
		OrderType:           order.OrderType,
		CreatedAt:           order.CreatedAt,
		ExpiresAt:           order.ExpiresAt,
		PaidAt:              order.PaidAt,
		CompletedAt:         order.CompletedAt,
		RefundAmount:        order.RefundAmount,
		RefundReason:        order.RefundReason,
		RefundRequestedAt:   order.RefundRequestedAt,
		RefundRequestedBy:   order.RefundRequestedBy,
		RefundRequestReason: order.RefundRequestReason,
		PlanID:              order.PlanID,
		ProviderInstanceID:  order.ProviderInstanceID,
	}
}

// UserIsWeChatBrowser reports whether User-Agent contains the WeChat browser
// token, case-insensitively.
func UserIsWeChatBrowser(c *gin.Context) bool {
	return strings.Contains(strings.ToLower(c.GetHeader("User-Agent")), "micromessenger")
}
