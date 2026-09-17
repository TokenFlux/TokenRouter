package service

import (
	"context"
	"os"
	"sync"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
)

// --- Order Status Constants ---

const (
	OrderStatusPending           = payment.OrderStatusPending
	OrderStatusProcessing        = payment.OrderStatusProcessing
	OrderStatusPaid              = payment.OrderStatusPaid
	OrderStatusRecharging        = payment.OrderStatusRecharging
	OrderStatusCompleted         = payment.OrderStatusCompleted
	OrderStatusExpired           = payment.OrderStatusExpired
	OrderStatusCancelled         = payment.OrderStatusCancelled
	OrderStatusFailed            = payment.OrderStatusFailed
	OrderStatusRefundRequested   = payment.OrderStatusRefundRequested
	OrderStatusRefunding         = payment.OrderStatusRefunding
	OrderStatusRefundPending     = payment.OrderStatusRefundPending
	OrderStatusPartiallyRefunded = payment.OrderStatusPartiallyRefunded
	OrderStatusRefunded          = payment.OrderStatusRefunded
	OrderStatusRefundFailed      = payment.OrderStatusRefundFailed
)

const paymentResumeSigningKeyEnv = "PAYMENT_RESUME_SIGNING_KEY"

// --- Types ---

type CreateOrderRequest = payment.CreateOrderRequest

type CreateOrderResponse = payment.CreateOrderResponse

type OrderListParams = payment.OrderListParams

type RefundPlan struct {
	OperationID     string
	ChannelRefundID string
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

type RefundResult = payment.RefundResult

type DashboardStats = payment.DashboardStats

type CurrencyAmounts = payment.CurrencyAmounts

type DailyStats = payment.DailyStats

type PaymentMethodStat = payment.PaymentMethodStat

type PurchaseDistributionStat = payment.PurchaseDistributionStat

type TopUserStat = payment.TopUserStat

type TopUsersByCurrency = payment.TopUsersByCurrency

// --- Service ---

type PaymentService struct {
	orderLifecycleOnce       sync.Once
	orderLifecycle           *payment.OrderLifecycle
	fulfillment              *payment.Fulfillment
	checkout                 *payment.Checkout
	queries                  *payment.OrderQueries
	refundStore              *paymentpostgres.RefundStore
	refunds                  *payment.RefundWorkflow
	bindingOnce              sync.Once
	bindings                 *payment.ProviderBindings
	providersLoaded          bool
	entClient                *dbent.Client
	registry                 *payment.Registry
	loadBalancer             payment.LoadBalancer
	redeemService            *RedeemService
	subscriptionSvc          *SubscriptionService
	configService            *PaymentConfigService
	userRepo                 UserRepository
	groupRepo                GroupRepository
	affiliateService         *AffiliateService
	resumeService            *PaymentResumeService
	notificationEmailService *NotificationEmailService
}

func NewPaymentService(entClient *dbent.Client, registry *payment.Registry, loadBalancer payment.LoadBalancer, redeemService *RedeemService, subscriptionSvc *SubscriptionService, configService *PaymentConfigService, userRepo UserRepository, groupRepo GroupRepository, affiliateService *AffiliateService) *PaymentService {
	svc := &PaymentService{entClient: entClient, registry: registry, loadBalancer: newVisibleMethodLoadBalancer(loadBalancer, configService), redeemService: redeemService, subscriptionSvc: subscriptionSvc, configService: configService, userRepo: userRepo, groupRepo: groupRepo, affiliateService: affiliateService}
	svc.resumeService = psNewPaymentResumeService(configService)
	return svc
}

func (s *PaymentService) SetNotificationEmailService(notificationEmailService *NotificationEmailService) {
	s.notificationEmailService = notificationEmailService
}

// --- Provider Registry ---

func (s *PaymentService) EnsureProviders(ctx context.Context) {
	s.paymentBindings().EnsureProviders(ctx)
}

func (s *PaymentService) RefreshProviders(ctx context.Context) {
	s.paymentBindings().RefreshProviders(ctx)
}

// --- Helpers ---

func psBillingInfoSnapshot(info *payment.BillingInfo) map[string]any {
	return payment.BillingInfoSnapshot(info)
}

func psValidateBillingInfo(info *payment.BillingInfo, fallbackEmail string) error {
	return payment.ValidateBillingInfo(info, fallbackEmail)
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
	return payment.ResolvePaymentResumeSigningKeys(os.Getenv(paymentResumeSigningKeyEnv), legacyKey)
}

// 订阅有效期单位常量。
const ()

func psComputeValidityDays(days int, unit string) int { return billing.ComputeValidityDays(days, unit) }

// NativeRuntime 为仍未清理的旧构造入口返回同一用例图，不复制状态。
func (s *PaymentService) NativeRuntime() *payment.Runtime {
	if s == nil {
		return nil
	}
	return &payment.Runtime{Checkout: s.paymentCheckout(), OrderQueries: s.paymentQueries(), RefundWorkflow: s.paymentRefunds(), OrderLifecycle: s.paymentOrderLifecycle(), ProviderBindings: s.paymentBindings()}
}
