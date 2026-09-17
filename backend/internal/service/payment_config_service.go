// 旧配置入口只投影与委托；唯一规则在 payment，S15/S16 清理。
package service

import (
	"context"
	"log/slog"
	"os"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment/provider"
)

type PaymentConfigService struct {
	core          *payment.ConfigService
	plans         *billing.Plans
	entClient     *dbent.Client
	settingRepo   SettingRepository
	encryptionKey []byte
}

func NewPaymentConfigService(client *dbent.Client, repo SettingRepository, key []byte) *PaymentConfigService {
	return &PaymentConfigService{entClient: client, settingRepo: repo, encryptionKey: key}
}
func (s *PaymentConfigService) paymentCoreConfig() *payment.ConfigService {
	if s == nil {
		return nil
	}
	if s.core != nil {
		return s.core
	}
	var store payment.ConfigurationStore
	if s.entClient != nil {
		store = paymentpostgres.NewInstanceStore(s.entClient)
	}
	return payment.NewConfigService(store, s.settingRepo, s.encryptionKey, s.billingPlans(), payment.ConfigurationRuntime{CreateProvider: provider.CreateProvider, LookupEnv: os.LookupEnv, Warn: slog.Warn})
}

// WrapPaymentConfigService 由 app 注入唯一核心；其余字段只供旧调用签名转接。
func WrapPaymentConfigService(core *payment.ConfigService, client *dbent.Client, settings SettingRepository, key []byte, plans *billing.Plans) *PaymentConfigService {
	return &PaymentConfigService{core: core, entClient: client, settingRepo: settings, encryptionKey: key, plans: plans}
}

const SettingPaymentEnabled = payment.SettingPaymentEnabled

const SettingMinRechargeAmount = payment.SettingMinRechargeAmount

const SettingMaxRechargeAmount = payment.SettingMaxRechargeAmount

const SettingDailyRechargeLimit = payment.SettingDailyRechargeLimit

const SettingOrderTimeoutMinutes = payment.SettingOrderTimeoutMinutes

const SettingMaxPendingOrders = payment.SettingMaxPendingOrders

const SettingEnabledPaymentTypes = payment.SettingEnabledPaymentTypes

const SettingLoadBalanceStrategy = payment.SettingLoadBalanceStrategy

const SettingBalancePayDisabled = payment.SettingBalancePayDisabled

const SettingBalanceRechargeMult = payment.SettingBalanceRechargeMult

const SettingSubscriptionUSDToCNYRate = payment.SettingSubscriptionUSDToCNYRate

const SettingRechargeFeeRate = payment.SettingRechargeFeeRate

const SettingPaymentMethodFees = payment.SettingPaymentMethodFees

const SettingProductNamePrefix = payment.SettingProductNamePrefix

const SettingProductNameSuffix = payment.SettingProductNameSuffix

const SettingHelpImageURL = payment.SettingHelpImageURL

const SettingHelpText = payment.SettingHelpText

const SettingCancelRateLimitOn = payment.SettingCancelRateLimitOn

const SettingCancelRateLimitMax = payment.SettingCancelRateLimitMax

const SettingCancelWindowSize = payment.SettingCancelWindowSize

const SettingCancelWindowUnit = payment.SettingCancelWindowUnit

const SettingCancelWindowMode = payment.SettingCancelWindowMode

const SettingAlipayForceQRCode = payment.SettingAlipayForceQRCode

const SettingAlipayMobilePrecreateDeepLink = payment.SettingAlipayMobilePrecreateDeepLink

type PaymentConfig = payment.PaymentConfig

type UpdatePaymentConfigRequest = payment.UpdatePaymentConfigRequest

type MethodLimits = payment.MethodLimits

type MethodFeeConfig = payment.MethodFeeConfig

type MethodFeeSettings = payment.MethodFeeSettings

type MethodLimitsResponse = payment.MethodLimitsResponse

type CreateProviderInstanceRequest = payment.CreateProviderInstanceRequest

type UpdateProviderInstanceRequest = payment.UpdateProviderInstanceRequest

type TestProviderDraftRequest = payment.TestProviderDraftRequest

type ProviderDraftTestResult = payment.ProviderDraftTestResult

type CreatePlanRequest = payment.CreatePlanRequest

type UpdatePlanRequest = payment.UpdatePlanRequest

func (s *PaymentConfigService) GetByID(ctx context.Context, id int64) (*SubscriptionPlan, error) {
	return s.paymentCoreConfig().GetByID(ctx, id)
}

func (s *PaymentConfigService) IsPaymentEnabled(ctx context.Context) bool {
	return s.paymentCoreConfig().IsPaymentEnabled(ctx)
}

func (s *PaymentConfigService) GetPaymentConfig(ctx context.Context) (*PaymentConfig, error) {
	return s.paymentCoreConfig().GetPaymentConfig(ctx)
}

func (s *PaymentConfigService) parsePaymentConfig(vals map[string]string) *PaymentConfig {
	return s.paymentCoreConfig().ConfigParsePaymentConfig(vals)
}

func validateMethodFeeSettings(settings MethodFeeSettings) error {
	return payment.ConfigValidateMethodFeeSettings(settings)
}

func (s *PaymentConfigService) UpdatePaymentConfig(ctx context.Context, req UpdatePaymentConfigRequest) error {
	return s.paymentCoreConfig().UpdatePaymentConfig(ctx, req)
}

func pcParseFloat(s string, defaultVal float64) float64 {
	return payment.ConfigPcParseFloat(s, defaultVal)
}

func pcParseInt(s string, defaultVal int) int { return payment.ConfigPcParseInt(s, defaultVal) }

func buildVisibleMethodSourceAvailability(instances []*dbent.PaymentProviderInstance) map[string]bool {
	return payment.ConfigBuildVisibleMethodSourceAvailability(paymentInstances(instances))
}

func applyVisibleMethodRoutingToEnabledTypes(base []string, vals map[string]string, available map[string]bool) []string {
	return payment.ConfigApplyVisibleMethodRoutingToEnabledTypes(base, vals, available)
}

// billingPlans 只兼容旧手工构造；生产由 app 显式注入。
func (s *PaymentConfigService) billingPlans() *billing.Plans {
	if s.plans != nil {
		return s.plans
	}
	return billing.NewPlans(billingpostgres.NewPlanStore(s.entClient), legacyPendingPlanOrders{s.entClient})
}

type legacyPendingPlanOrders struct{ client *dbent.Client }

func (r legacyPendingPlanOrders) CountInProgressByPlan(ctx context.Context, id int64) (int, error) {
	return CountPendingPlanOrders(ctx, r.client, id)
}

func NewPaymentConfigServiceWithPlans(client *dbent.Client, settings SettingRepository, key []byte, plans *billing.Plans) *PaymentConfigService {
	s := NewPaymentConfigService(client, settings, key)
	s.plans = plans
	return s
}

// Plans 供旧 HTTP 兼容入口取得 app 注入的权益用例，S12 删除该转接。
func (s *PaymentConfigService) Plans() *billing.Plans { return s.billingPlans() }

// NativeConfig 为旧 HTTP 构造入口投影唯一配置实例。
func (s *PaymentConfigService) NativeConfig() *payment.ConfigService { return s.paymentCoreConfig() }

// PreparePaymentConfig 保留旧调用签名，校验及格式化由 payment 唯一实现。
func (s *PaymentConfigService) PreparePaymentConfig(req UpdatePaymentConfigRequest) (map[string]string, error) {
	return payment.PreparePaymentConfig(req)
}
