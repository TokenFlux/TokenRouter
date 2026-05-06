package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/mail"
	"os"
	"strings"
	"sync"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentproviderinstance"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/payment/provider"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
)

// --- Order Status Constants ---

const (
	OrderStatusPending           = payment.OrderStatusPending
	OrderStatusPaid              = payment.OrderStatusPaid
	OrderStatusRecharging        = payment.OrderStatusRecharging
	OrderStatusCompleted         = payment.OrderStatusCompleted
	OrderStatusExpired           = payment.OrderStatusExpired
	OrderStatusCancelled         = payment.OrderStatusCancelled
	OrderStatusFailed            = payment.OrderStatusFailed
	OrderStatusRefundRequested   = payment.OrderStatusRefundRequested
	OrderStatusRefunding         = payment.OrderStatusRefunding
	OrderStatusPartiallyRefunded = payment.OrderStatusPartiallyRefunded
	OrderStatusRefunded          = payment.OrderStatusRefunded
	OrderStatusRefundFailed      = payment.OrderStatusRefundFailed
)

const (
	// defaultMaxPendingOrders and defaultOrderTimeoutMin are defined in
	// payment_config_service.go alongside other payment configuration defaults.
	paymentGraceMinutes = 5

	defaultPageSize    = 20
	maxPageSize        = 100
	topUsersLimit      = 10
	amountToleranceCNY = 0.01

	orderIDPrefix = "sub2_"
)

const paymentResumeSigningKeyEnv = "PAYMENT_RESUME_SIGNING_KEY"

// --- Types ---

// generateOutTradeNo creates a unique external order ID for payment providers.
// Format: sub2_20250409aB3kX9mQ (prefix + date + 8-char random)
func generateOutTradeNo() string {
	date := time.Now().Format("20060102")
	rnd := generateRandomString(8)
	return orderIDPrefix + date + rnd
}

func generateRandomString(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.IntN(len(charset))]
	}
	return string(b)
}

type CreateOrderRequest struct {
	UserID          int64
	Amount          float64
	PaymentType     string
	OpenID          string
	ClientIP        string
	IsMobile        bool
	IsWeChatBrowser bool
	SrcHost         string
	SrcURL          string
	ReturnURL       string
	PaymentSource   string
	OrderType       string
	PlanID          int64
	BillingInfo     *payment.BillingInfo
}

type CreateOrderResponse struct {
	OrderID       int64                           `json:"order_id"`
	Amount        float64                         `json:"amount"`
	PayAmount     float64                         `json:"pay_amount"`
	FeeRate       float64                         `json:"fee_rate"`
	Status        string                          `json:"status"`
	ResultType    payment.CreatePaymentResultType `json:"result_type,omitempty"`
	PaymentType   string                          `json:"payment_type"`
	OutTradeNo    string                          `json:"out_trade_no,omitempty"`
	PayURL        string                          `json:"pay_url,omitempty"`
	QRCode        string                          `json:"qr_code,omitempty"`
	ClientSecret  string                          `json:"client_secret,omitempty"`
	CustomerID    string                          `json:"customer_id,omitempty"`
	InvoiceID     string                          `json:"invoice_id,omitempty"`
	InvoiceURL    string                          `json:"invoice_url,omitempty"`
	InvoicePDF    string                          `json:"invoice_pdf,omitempty"`
	InvoiceStatus string                          `json:"invoice_status,omitempty"`
	OAuth         *payment.WechatOAuthInfo        `json:"oauth,omitempty"`
	JSAPI         *payment.WechatJSAPIPayload     `json:"jsapi,omitempty"`
	JSAPIPayload  *payment.WechatJSAPIPayload     `json:"jsapi_payload,omitempty"`
	ExpiresAt     time.Time                       `json:"expires_at"`
	PaymentMode   string                          `json:"payment_mode,omitempty"`
	ResumeToken   string                          `json:"resume_token,omitempty"`
}

type OrderListParams struct {
	Page        int
	PageSize    int
	Status      string
	OrderType   string
	PaymentType string
	Keyword     string
}

type RefundPlan struct {
	OrderID         int64
	Order           *dbent.PaymentOrder
	RefundAmount    float64
	GatewayAmount   float64
	Reason          string
	Force           bool
	DeductBalance   bool
	DeductionType   string
	BalanceToDeduct float64
	SubDaysToDeduct int
	SubscriptionID  int64
}

type RefundResult struct {
	Success         bool    `json:"success"`
	Warning         string  `json:"warning,omitempty"`
	RequireForce    bool    `json:"require_force,omitempty"`
	BalanceDeducted float64 `json:"balance_deducted,omitempty"`
	SubDaysDeducted int     `json:"subscription_days_deducted,omitempty"`
}

type DashboardStats struct {
	TodayAmount   float64 `json:"today_amount"`
	TotalAmount   float64 `json:"total_amount"`
	TodayCount    int     `json:"today_count"`
	TotalCount    int     `json:"total_count"`
	AvgAmount     float64 `json:"avg_amount"`
	PendingOrders int     `json:"pending_orders"`

	DailySeries    []DailyStats        `json:"daily_series"`
	PaymentMethods []PaymentMethodStat `json:"payment_methods"`
	TopUsers       []TopUserStat       `json:"top_users"`
}

type DailyStats struct {
	Date   string  `json:"date"`
	Amount float64 `json:"amount"`
	Count  int     `json:"count"`
}

type PaymentMethodStat struct {
	Type   string  `json:"type"`
	Amount float64 `json:"amount"`
	Count  int     `json:"count"`
}

type TopUserStat struct {
	UserID int64   `json:"user_id"`
	Email  string  `json:"email"`
	Amount float64 `json:"amount"`
}

// --- Service ---

type PaymentService struct {
	providerMu      sync.Mutex
	providersLoaded bool
	entClient       *dbent.Client
	registry        *payment.Registry
	loadBalancer    payment.LoadBalancer
	redeemService   *RedeemService
	subscriptionSvc *SubscriptionService
	configService   *PaymentConfigService
	userRepo        UserRepository
	groupRepo       GroupRepository
	resumeService   *PaymentResumeService
}

func NewPaymentService(entClient *dbent.Client, registry *payment.Registry, loadBalancer payment.LoadBalancer, redeemService *RedeemService, subscriptionSvc *SubscriptionService, configService *PaymentConfigService, userRepo UserRepository, groupRepo GroupRepository) *PaymentService {
	svc := &PaymentService{entClient: entClient, registry: registry, loadBalancer: newVisibleMethodLoadBalancer(loadBalancer, configService), redeemService: redeemService, subscriptionSvc: subscriptionSvc, configService: configService, userRepo: userRepo, groupRepo: groupRepo}
	svc.resumeService = psNewPaymentResumeService(configService)
	return svc
}

// --- Provider Registry ---

// EnsureProviders lazily initializes the provider registry on first call.
func (s *PaymentService) EnsureProviders(ctx context.Context) {
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	if !s.providersLoaded {
		s.loadProviders(ctx)
		s.providersLoaded = true
	}
}

// RefreshProviders clears and re-registers all providers from the database.
func (s *PaymentService) RefreshProviders(ctx context.Context) {
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	s.registry.Clear()
	s.loadProviders(ctx)
	s.providersLoaded = true
}

func (s *PaymentService) loadProviders(ctx context.Context) {
	instances, err := s.entClient.PaymentProviderInstance.Query().
		Where(paymentproviderinstance.EnabledEQ(true)).
		All(ctx)
	if err != nil {
		slog.Error("[PaymentService] failed to query provider instances", "error", err)
		return
	}
	for _, inst := range instances {
		cfg, err := s.loadBalancer.GetInstanceConfig(ctx, int64(inst.ID))
		if err != nil {
			slog.Warn("[PaymentService] failed to decrypt config for instance", "instanceID", inst.ID, "error", err)
			continue
		}
		if inst.PaymentMode != "" {
			cfg["paymentMode"] = inst.PaymentMode
		}
		instID := fmt.Sprintf("%d", inst.ID)
		p, err := provider.CreateProvider(inst.ProviderKey, instID, cfg)
		if err != nil {
			slog.Warn("[PaymentService] failed to create provider for instance", "instanceID", inst.ID, "key", inst.ProviderKey, "error", err)
			continue
		}
		s.registry.Register(p)
	}
}

// --- Helpers ---

func psIsRefundStatus(s string) bool {
	switch s {
	case OrderStatusRefundRequested, OrderStatusRefunding, OrderStatusPartiallyRefunded, OrderStatusRefunded, OrderStatusRefundFailed:
		return true
	}
	return false
}

func psErrMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func psNilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func psTrimBillingInfo(info *payment.BillingInfo) *payment.BillingInfo {
	if info == nil {
		return nil
	}
	trimmed := &payment.BillingInfo{
		Name:      strings.TrimSpace(info.Name),
		Email:     strings.TrimSpace(info.Email),
		TaxIDType: strings.TrimSpace(info.TaxIDType),
		TaxID:     strings.TrimSpace(info.TaxID),
	}
	if info.Address != nil {
		addr := &payment.BillingAddress{
			Country:    strings.ToUpper(strings.TrimSpace(info.Address.Country)),
			Line1:      strings.TrimSpace(info.Address.Line1),
			Line2:      strings.TrimSpace(info.Address.Line2),
			City:       strings.TrimSpace(info.Address.City),
			State:      strings.TrimSpace(info.Address.State),
			PostalCode: strings.TrimSpace(info.Address.PostalCode),
		}
		if addr.Country != "" || addr.Line1 != "" || addr.Line2 != "" || addr.City != "" || addr.State != "" || addr.PostalCode != "" {
			trimmed.Address = addr
		}
	}
	if trimmed.Name == "" && trimmed.Email == "" && trimmed.Address == nil && trimmed.TaxIDType == "" && trimmed.TaxID == "" {
		return nil
	}
	return trimmed
}

func psBillingInfoSnapshot(info *payment.BillingInfo) map[string]any {
	info = psTrimBillingInfo(info)
	if info == nil {
		return nil
	}
	snapshot := map[string]any{}
	if info.Name != "" {
		snapshot["name"] = info.Name
	}
	if info.Email != "" {
		snapshot["email"] = info.Email
	}
	if info.TaxIDType != "" {
		snapshot["tax_id_type"] = info.TaxIDType
	}
	if info.TaxID != "" {
		snapshot["tax_id"] = info.TaxID
	}
	if info.Address != nil {
		address := map[string]any{}
		if info.Address.Country != "" {
			address["country"] = info.Address.Country
		}
		if info.Address.Line1 != "" {
			address["line1"] = info.Address.Line1
		}
		if info.Address.Line2 != "" {
			address["line2"] = info.Address.Line2
		}
		if info.Address.City != "" {
			address["city"] = info.Address.City
		}
		if info.Address.State != "" {
			address["state"] = info.Address.State
		}
		if info.Address.PostalCode != "" {
			address["postal_code"] = info.Address.PostalCode
		}
		if len(address) > 0 {
			snapshot["address"] = address
		}
	}
	return snapshot
}

func psValidateBillingInfo(info *payment.BillingInfo, fallbackEmail string) error {
	info = psTrimBillingInfo(info)
	if info == nil {
		return infraerrors.BadRequest("BILLING_INFO_REQUIRED", "billing info is required for Stripe invoice")
	}
	if strings.TrimSpace(info.Name) == "" {
		return infraerrors.BadRequest("BILLING_NAME_REQUIRED", "billing name is required")
	}
	email := strings.TrimSpace(info.Email)
	if email == "" {
		email = strings.TrimSpace(fallbackEmail)
	}
	if email == "" {
		return infraerrors.BadRequest("BILLING_EMAIL_REQUIRED", "billing email is required")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return infraerrors.BadRequest("BILLING_EMAIL_INVALID", "billing email is invalid")
	}
	if (strings.TrimSpace(info.TaxIDType) == "") != (strings.TrimSpace(info.TaxID) == "") {
		return infraerrors.BadRequest("BILLING_TAX_ID_INVALID", "billing tax id type and value must be provided together")
	}
	return nil
}

func (s *PaymentService) paymentResume() *PaymentResumeService {
	if s.resumeService != nil {
		return s.resumeService
	}
	return psNewPaymentResumeService(s.configService)
}

func NewLegacyAwarePaymentResumeService(legacyKey []byte) *PaymentResumeService {
	return newLegacyAwarePaymentResumeService(legacyKey)
}

func psNewPaymentResumeService(configService *PaymentConfigService) *PaymentResumeService {
	return newLegacyAwarePaymentResumeService(psResumeLegacyVerificationKey(configService))
}

func newLegacyAwarePaymentResumeService(legacyKey []byte) *PaymentResumeService {
	signingKey, verifyFallbacks := resolvePaymentResumeSigningKeys(legacyKey)
	return NewPaymentResumeService(signingKey, verifyFallbacks...)
}

func psResumeLegacyVerificationKey(configService *PaymentConfigService) []byte {
	if configService == nil {
		return nil
	}
	return configService.encryptionKey
}

func resolvePaymentResumeSigningKeys(legacyKey []byte) ([]byte, [][]byte) {
	signingKey := parsePaymentResumeSigningKey(os.Getenv(paymentResumeSigningKeyEnv))
	if len(signingKey) == 0 {
		if len(legacyKey) == 0 {
			return nil, nil
		}
		return legacyKey, nil
	}
	if len(legacyKey) == 0 || bytes.Equal(legacyKey, signingKey) {
		return signingKey, nil
	}
	return signingKey, [][]byte{legacyKey}
}

func parsePaymentResumeSigningKey(raw string) []byte {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if len(raw) >= 64 && len(raw)%2 == 0 {
		if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) > 0 {
			return decoded
		}
	}
	return []byte(raw)
}

func psSliceContains(sl []string, s string) bool {
	for _, v := range sl {
		if v == s {
			return true
		}
	}
	return false
}

// Subscription validity period unit constants.
const (
	validityUnitWeek  = "week"
	validityUnitMonth = "month"
)

func psComputeValidityDays(days int, unit string) int {
	switch unit {
	case validityUnitWeek:
		return days * 7
	case validityUnitMonth:
		return days * 30
	default:
		return days
	}
}

func psStartOfDayUTC(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func applyPagination(pageSize, page int) (size, pg int) {
	size = pageSize
	if size <= 0 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	pg = page
	if pg < 1 {
		pg = 1
	}
	return size, pg
}
