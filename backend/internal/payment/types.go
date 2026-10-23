package payment

import (
	"context"
	"time"
)

// types.go defines payment adapter requests, results, and interfaces and
// normalizes historical payment-method labels.
// Checkout, confirmation, and refund adapters exchange merchant references
// separately from provider transaction, invoice, and refund identities. Their
// request and response values retain gateway currency quantities, optional
// frontend payloads, and merchant metadata used by the later order comparison.
//
// Adapter interfaces leave protocol-specific verification, reference
// resolution, and configuration copying with each implementation. A nil
// verified notification can be acknowledged as irrelevant, and some refund
// adapters return pending progress alongside an error for local recovery.
// Compatibility method mapping collapses known Stripe labels and Alipay/WeChat
// prefixes while retaining unrelated custom method text. Local uppercase order
// states and lowercase provider progress labels therefore describe separate
// stages rather than one interchangeable status value.

const (
	// Payment method labels include canonical visible methods, historical
	// aliases, and adapter identities interpreted by configuration and dispatch.
	TypeAlipay PaymentType = "alipay" // visible Alipay method
	TypeWxpay  PaymentType = "wxpay"  // visible WeChat method
	// historical official Alipay alias.
	TypeAlipayDirect PaymentType = "alipay_direct"
	// historical official WeChat alias.
	TypeWxpayDirect PaymentType = "wxpay_direct"
	TypeStripe      PaymentType = "stripe"  // hosted Stripe checkout method
	TypeCard        PaymentType = "card"    // historical Stripe card alias
	TypeLink        PaymentType = "link"    // historical Stripe Link alias
	TypeEasyPay     PaymentType = "easypay" // EasyPay adapter identity
	// Airwallex checkout adapter identity.
	TypeAirwallex PaymentType = "airwallex"

	// Order states distinguish external payment progress, entitlement delivery,
	// closing, and refund recovery; persisted orders and conditional updates use these labels.
	OrderStatusPending = "PENDING" // awaiting payment confirmation
	// provider payment is processing.
	OrderStatusProcessing = "PROCESSING"
	// payment confirmed before grant completion.
	OrderStatusPaid       = "PAID"
	OrderStatusRecharging = "RECHARGING" // entitlement claim or recovery state
	// local entitlement delivery completed.
	OrderStatusCompleted = "COMPLETED"
	OrderStatusExpired   = "EXPIRED" // locally expired payment order
	// locally cancelled pending order.
	OrderStatusCancelled = "CANCELLED"
	// entitlement or checkout work failed.
	OrderStatusFailed = "FAILED"
	// user requested administrative refund.
	OrderStatusRefundRequested = "REFUND_REQUESTED"
	// prepared refund recovery in progress.
	OrderStatusRefunding = "REFUNDING"
	// provider refund awaits confirmation.
	OrderStatusRefundPending = "REFUND_PENDING"
	// accepted partial local refund.
	OrderStatusPartiallyRefunded = "PARTIALLY_REFUNDED"
	OrderStatusRefunded          = "REFUNDED" // accepted full local refund
	// pending provider refund reported failure.
	OrderStatusRefundFailed = "REFUND_FAILED"

	// Order types select balance-code fulfillment or source-order subscription
	// issuance and determine which recorded quantities refund planning interprets.
	OrderTypeBalance      = "balance"      // balance entitlement purchase
	OrderTypeSubscription = "subscription" // subscription plan purchase

	// EntityStatusActive is the account state accepted by checkout after its
	// buyer-profile read; other observed account states reject creation.
	EntityStatusActive = "active" // checkout-eligible account state

	// Deduction kinds choose wallet recovery, subscription day/grant operations,
	// or no entitlement mutation while the gateway refund still progresses.
	DeductionTypeBalance = "balance" // wallet quantity recovery
	// subscription duration or revocation.
	DeductionTypeSubscription = "subscription"
	DeductionTypeNone         = "none" // no local entitlement recovery

	// Notification success labels select confirmation after provider binding
	// and amount checks; adapters also use the lowercase provider progress labels.
	NotificationStatusSuccess = "success" // adapter-reported successful payment
	NotificationStatusPaid    = "paid"    // adapter-reported paid observation

	// Provider status labels normalize adapter observations for confirmation,
	// reconciliation, and refund handling before local uppercase order transitions.
	ProviderStatusPending    = "pending"    // unconfirmed provider progress
	ProviderStatusProcessing = "processing" // unfinished provider payment
	ProviderStatusPaid       = "paid"       // provider query confirmed payment
	ProviderStatusSuccess    = "success"    // provider operation succeeded
	ProviderStatusFailed     = "failed"     // provider operation reported failure
	ProviderStatusRefunded   = "refunded"   // provider reports refunded progress

	// DefaultLoadBalanceStrategy selects the configured round-robin label when
	// payment settings omit their strategy; the selector owns actual candidate ordering.
	DefaultLoadBalanceStrategy = "round-robin"

	// ConfigKeyPublishableKey names the Stripe public credential read from a
	// selected enabled instance for checkout configuration responses.
	ConfigKeyPublishableKey = "publishableKey"

	// Create-payment result variants direct the frontend to a stored order, OAuth
	// authorization, or the supplied WeChat invocation payload.
	// ordinary created-order result.
	CreatePaymentResultOrderCreated CreatePaymentResultType = "order_created"
	// WeChat authorization is required.
	CreatePaymentResultOAuthRequired CreatePaymentResultType = "oauth_required"
	// WeChat invocation payload is ready.
	CreatePaymentResultJSAPIReady CreatePaymentResultType = "jsapi_ready"
)

// PaymentType is the string method label used by payment adapter interfaces.
type PaymentType = string

// CreatePaymentRequest carries the stored order and payer inputs sent to one
// adapter.
type CreatePaymentRequest struct {
	OrderID               string // stored merchant order number
	Amount                string // formatted gateway currency amount
	PaymentType           string // selected visible payment method
	Subject               string // recorded checkout product description
	NotifyURL             string // request notification URL override
	ReturnURL             string // request browser return URL
	OpenID                string // WeChat payer OpenID
	ClientIP              string // payer client IP address
	IsMobile              bool   // payer mobile-client indication
	AlipayMobilePrecreate bool   // mobile Alipay precreate selection
	// instance comma-separated supported methods
	InstanceSubMethods string
	ExpiresAt          time.Time // local requested expiration time
	UserEmail          string    // checkout buyer email address
	// BillingInfo retains the supplied invoice identity pointer. Stripe
	// normalization can mutate its shared Address fields, so direct adapter
	// callers account for that input sharing.
	BillingInfo *BillingInfo
}

// BillingAddress retains optional invoice address fields supplied by the
// payer.
type BillingAddress struct {
	Country    string `json:"country,omitempty"`     // billing country code text
	Line1      string `json:"line1,omitempty"`       // first billing address line
	Line2      string `json:"line2,omitempty"`       // second billing address line
	City       string `json:"city,omitempty"`        // billing city name
	State      string `json:"state,omitempty"`       // billing region or state
	PostalCode string `json:"postal_code,omitempty"` // billing postal code text
}

// BillingInfo retains invoice contact, address, and optional tax identity
// input.
type BillingInfo struct {
	Name  string `json:"name,omitempty"`  // payer invoice display name
	Email string `json:"email,omitempty"` // payer invoice email address
	// Address is optional invoice input retained by reference. Stripe
	// normalization updates its fields in place, while checkout's earlier
	// trimming stage supplies its own prepared input.
	Address *BillingAddress `json:"address,omitempty"`
	// provider tax identifier kind
	// TaxIDType and TaxID identify one optional provider tax record. Stripe
	// customer construction writes that pair when both normalized values are
	// nonempty; checkout validation checks their supplied pairing before
	// initiation.
	TaxIDType string `json:"tax_id_type,omitempty"`
	// payer tax identifier text
	TaxID string `json:"tax_id,omitempty"`
}

// CreatePaymentResultType is the frontend variant label returned by payment
// initiation.
type CreatePaymentResultType = string

// WechatOAuthInfo carries the authorization step needed before an official
// WeChat payment.
type WechatOAuthInfo struct {
	// browser authorization redirect URL
	AuthorizeURL string `json:"authorize_url,omitempty"`
	// WeChat authorization application ID
	AppID  string `json:"appid,omitempty"`
	OpenID string `json:"openid,omitempty"` // resolved payer OpenID
	// requested authorization scope text
	Scope string `json:"scope,omitempty"`
	State string `json:"state,omitempty"` // authorization state token text
	// frontend authorization callback path
	RedirectURL string `json:"redirect_url,omitempty"`
}

// WechatJSAPIPayload retains the SDK-generated strings used for browser WeChat
// invocation.
type WechatJSAPIPayload struct {
	// WeChat invocation application ID
	AppID     string `json:"appId,omitempty"`
	TimeStamp string `json:"timeStamp,omitempty"` // invocation timestamp text
	NonceStr  string `json:"nonceStr,omitempty"`  // invocation random nonce text
	Package   string `json:"package,omitempty"`   // prepay package parameter text
	// invocation signature algorithm name
	SignType string `json:"signType,omitempty"`
	// invocation signature value text
	PaySign string `json:"paySign,omitempty"`
}

// CreatePaymentResponse retains provider references and frontend initiation
// data for checkout.
type CreatePaymentResponse struct {
	TradeNo       string // provider payment query reference
	PayURL        string // provider checkout browser URL
	QRCode        string // provider scannable QR payload
	ClientSecret  string // provider frontend client secret
	CustomerID    string // provider customer account identity
	InvoiceID     string // provider invoice object identity
	InvoiceURL    string // hosted provider invoice URL
	InvoicePDF    string // provider invoice PDF URL
	InvoiceStatus string // reported provider invoice state
	IntentID      string // provider SDK intent identity
	Currency      string // configured provider currency code
	CountryCode   string // provider frontend country setting
	PaymentEnv    string // provider frontend environment label
	// ExpiresAt replaces the locally stored expiration when nonzero during
	// response persistence. A zero value keeps the draft expiration, and Stripe
	// supplies its accepted session expiration here.
	ExpiresAt  time.Time
	ResultType CreatePaymentResultType // frontend initiation variant label
	// OAuth is copied into the checkout response when present. The selected
	// result variant determines whether the frontend follows its authorization
	// URL before payment can continue.
	OAuth *WechatOAuthInfo
	// JSAPI is copied into both checkout invocation aliases when present. Those
	// aliases retain the same payload pointer, and the result variant determines
	// the frontend invocation path.
	JSAPI *WechatJSAPIPayload
}

// QueryOrderResponse carries one adapter's normalized payment observation and
// metadata.
type QueryOrderResponse struct {
	TradeNo string  // reported provider transaction reference
	Status  string  // normalized provider payment progress
	Amount  float64 // observed gateway currency amount
	PaidAt  string  // provider-reported payment time text
	// Metadata carries reported merchant and currency strings into order snapshot
	// checks. Reconciliation forwards this mutable map into PaymentNotification
	// as supplied by the adapter.
	Metadata map[string]string
}

// PaymentNotification retains a verified adapter observation for local payment
// processing.
type PaymentNotification struct {
	TradeNo string  // reported provider transaction identity
	OrderID string  // reported merchant order number
	Amount  float64 // reported gateway payment amount
	Status  string  // normalized provider notification progress
	RawData string  // original notification text
	// Metadata is compared with recorded snapshot attributes after provider
	// binding. Empty metadata selects the validator's legacy skip path; callers
	// establish the appropriate adapter observation before processing.
	Metadata map[string]string
}

// PaymentDocumentResponse exposes the available provider invoice or receipt
// references.
type PaymentDocumentResponse struct {
	Type string `json:"type"`          // invoice or receipt document kind
	URL  string `json:"url,omitempty"` // preferred openable document URL
	// hosted provider invoice URL
	HostedInvoiceURL string `json:"hosted_invoice_url,omitempty"`
	// provider invoice PDF URL
	InvoicePDF string `json:"invoice_pdf,omitempty"`
	// provider payment receipt URL
	ReceiptURL string `json:"receipt_url,omitempty"`
	// provider invoice object identity
	InvoiceID string `json:"invoice_id,omitempty"`
	// reported provider invoice state
	InvoiceStatus string `json:"invoice_status,omitempty"`
}

// RefundRequest carries stored order references and formatted gateway refund
// money.
type RefundRequest struct {
	TradeNo string // stored upstream payment reference
	OrderID string // stored merchant order number
	Amount  string // formatted gateway refund amount
	Reason  string // supplied provider refund explanation
}

// RefundQueryRequest identifies an existing provider refund and its query
// amount text.
type RefundQueryRequest struct {
	TradeNo  string // stored upstream payment reference
	OrderID  string // stored merchant order number
	RefundID string // known provider refund identity
	Amount   string // supplied formatted query amount
}

// RefundResponse carries the provider refund identity and normalized
// settlement progress.
type RefundResponse struct {
	RefundID string // provider refund operation identity
	Status   string // normalized provider refund progress
}

// InstanceSelection retains the chosen instance, decrypted configuration, and
// checkout mode.
type InstanceSelection struct {
	InstanceID  string // decimal provider instance identity
	ProviderKey string // selected adapter implementation key
	// Config is the selected instance's decrypted string map passed to the
	// adapter constructor. Provider copying varies by adapter, so the selected
	// configuration stays stable while that adapter uses it.
	Config         map[string]string
	SupportedTypes string // comma-separated enabled payment methods
	PaymentMode    string // configured checkout display mode
}

// Provider defines adapter identity and external payment operations used by
// checkout and recovery.
type Provider interface {
	// Name returns the adapter's human-readable display name for identity
	// consumers.
	Name() string
	// ProviderKey returns the adapter implementation key used by binding and
	// dispatch.
	ProviderKey() string
	// SupportedTypes returns the method labels registered for this provider.
	// Registry.Register calls it while holding its write lock, so implementations
	// return their data without reentering that registry; copying varies by
	// adapter.
	SupportedTypes() []PaymentType
	// CreatePayment initiates the selected adapter's payment flow and returns
	// provider/frontend references. Browser-URL construction or external calls
	// depend on the adapter, and checkout persists a local order before invoking
	// this operation.
	CreatePayment(ctx context.Context, req CreatePaymentRequest) (*CreatePaymentResponse, error)
	// QueryOrder reads payment progress using the adapter's required reference
	// kind. Callers choose merchant, Checkout, invoice, or intent references
	// through PaymentOrderQueryReference and confirm actionable paid money
	// against the stored order.
	QueryOrder(ctx context.Context, tradeNo string) (*QueryOrderResponse, error)
	// VerifyNotification validates and interprets the original callback
	// body/query and required headers under adapter rules. A nil result with nil
	// error is an irrelevant event acknowledged by HTTP handlers; verification
	// errors request retry.
	VerifyNotification(ctx context.Context, rawBody string, headers map[string]string) (*PaymentNotification, error)
	// Refund submits the formatted gateway amount using the adapter's stored
	// payment references. A pending response can accompany an error for some
	// adapters, and the workflow preserves that progress for compensation and
	// later query.
	Refund(ctx context.Context, req RefundRequest) (*RefundResponse, error)
}

// RefundQueryProvider adds observation of an already submitted refund to a
// payment adapter.
type RefundQueryProvider interface {
	Provider // base payment and identity operations
	// QueryRefund observes an already submitted refund using the supplied or
	// adapter-derived identity. It returns settlement progress for local
	// recovery; provider-specific missing identity behavior is described by that
	// adapter.
	QueryRefund(ctx context.Context, req RefundQueryRequest) (*RefundResponse, error)
}

// NotificationConfirmer adds upstream verification for gateways whose signed
// callbacks are not sufficient payment proof by themselves. Webhook handling
// invokes it only for a verified success notification after confirming that
// its local order exists, and uses the same provider instance that accepted
// the callback. Confirmation errors are retryable and stop fulfillment.
type NotificationConfirmer interface {
	Provider // base payment and identity operations
	// ConfirmNotification queries upstream state for the verified notification.
	// Implementations must reject unpaid, mismatched, or indeterminate results;
	// they may replace provider-owned references such as TradeNo on the supplied
	// notification before the caller enters local fulfillment.
	ConfirmNotification(ctx context.Context, notification *PaymentNotification) error
}

// CancelableProvider adds provider-side closing for a still-pending payment
// reference.
type CancelableProvider interface {
	Provider // base payment and identity operations
	// CancelPayment closes a pending provider reference using its adapter's
	// operation. Lifecycle queries before closing and requeries a cancellation
	// error so a concurrently completed payment can determine the local outcome.
	CancelPayment(ctx context.Context, tradeNo string) error
}

// DocumentProvider reads available invoice or receipt links for an order's
// provider reference.
type DocumentProvider interface {
	Provider // base payment and identity operations
	// GetPaymentDocument returns a provider invoice or receipt using the supplied
	// stored references. Stripe prefers invoice links and follows its receipt
	// lookup rules when appropriate; callers inspect whether a usable URL was
	// returned.
	GetPaymentDocument(ctx context.Context, invoiceID string, tradeNo string) (*PaymentDocumentResponse, error)
}

// MerchantIdentityProvider exposes current configured merchant attributes for
// order-snapshot comparisons.
type MerchantIdentityProvider interface {
	// MerchantIdentityMetadata returns current non-secret merchant or currency
	// attributes for snapshot comparisons. Refund callers compare its supplied
	// map under ValidateProviderSnapshotMetadata; empty metadata selects that
	// validator's legacy skip path.
	MerchantIdentityMetadata() map[string]string
}

// EncryptionKey distinguishes the application's decoded legacy payment key
// during dependency assembly.
type EncryptionKey []byte

// GetBasePaymentType maps canonical and compatibility method text to its base
// identity. Exact stripe, card, and link become stripe; text beginning with
// alipay or wxpay becomes that method, while exact easypay and airwallex
// retain their names.
//
// Other input, including an empty value or a custom method name, is returned
// as supplied. The helper performs no trimming, case conversion, or
// supported-method validation, so callers such as NormalizeVisibleMethod apply
// their own normalization before this mapping. Prefix matching covers every
// suffix, not just the historical direct alias.
func GetBasePaymentType(t string) string {
	// Map legacy method representations before provider selection while retaining
	// unrelated custom method identities. Validation of supported custom prefixes
	// belongs to their configuration callers.
	switch {
	case t == TypeEasyPay:
		return TypeEasyPay
	case t == TypeAirwallex:
		return TypeAirwallex
	case t == TypeStripe || t == TypeCard || t == TypeLink:
		return TypeStripe
	case len(t) >= len(TypeAlipay) && t[:len(TypeAlipay)] == TypeAlipay:
		return TypeAlipay
	case len(t) >= len(TypeWxpay) && t[:len(TypeWxpay)] == TypeWxpay:
		return TypeWxpay
	default:
		return t
	}
}
