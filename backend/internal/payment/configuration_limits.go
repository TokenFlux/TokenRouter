// 支付配置与管理规则的唯一实现；存储、环境和渠道构造由端口提供。
package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// GetAvailableMethodLimits collects all payment types from enabled provider
// instances and returns limits for each, plus the global widest range.
// Stripe 实例按服务商级 "stripe" 聚合，兼容旧实例保存的 card/link 等子方式。
func (s *ConfigService) GetAvailableMethodLimits(ctx context.Context) (*MethodLimitsResponse, error) {
	instances, err := s.store.ListInstances(ctx, InstanceFilter{EnabledOnly: true})
	if err != nil {
		return nil, fmt.Errorf("query provider instances: %w", err)
	}
	cfg, err := s.GetPaymentConfig(ctx)
	if err != nil {
		return nil, err
	}
	typeInstances := ConfigPcGroupByPaymentType(instances)
	typeInstances = s.ConfigPcApplyEnabledVisibleMethodInstances(ctx, typeInstances, instances)
	resp := &MethodLimitsResponse{
		Methods: make(map[string]MethodLimits, len(typeInstances)),
	}
	for pt, insts := range typeInstances {
		currency, ok := s.ConfigPcAggregateMethodCurrency(insts)
		if !ok {
			continue
		}
		ml := ConfigPcAggregateMethodLimits(pt, insts)
		ml = ConfigPcApplyEffectiveMethodFee(cfg, ml)
		ml.DisplayName = s.ConfigPcAggregateMethodDisplayName(pt, insts)
		ml.Currency = currency
		resp.Methods[ml.PaymentType] = ml
	}
	resp.GlobalMin, resp.GlobalMax = ConfigPcComputeGlobalRange(resp.Methods)
	return resp, nil
}

func (s *ConfigService) ConfigPcApplyEnabledVisibleMethodInstances(ctx context.Context, typeInstances map[string][]*ProviderInstance, instances []*ProviderInstance) map[string][]*ProviderInstance {
	if len(typeInstances) == 0 {
		return typeInstances
	}

	filtered := make(map[string][]*ProviderInstance, len(typeInstances))
	for paymentType, groupedInstances := range typeInstances {
		filtered[paymentType] = groupedInstances
	}

	for _, method := range []string{TypeAlipay, TypeWxpay} {
		matching := ConfigFilterEnabledVisibleMethodInstances(instances, method)
		providerKey, err := s.ConfigResolveVisibleMethodProviderKey(ctx, method, matching)
		if err != nil {
			delete(filtered, method)
			continue
		}
		if providerKey == "" {
			if len(matching) == 0 {
				delete(filtered, method)
				continue
			}
			filtered[method] = matching
			continue
		}
		selectedInstances := ConfigFilterVisibleMethodInstancesByProviderKey(instances, method, providerKey)
		if len(selectedInstances) == 0 {
			delete(filtered, method)
			continue
		}
		filtered[method] = selectedInstances
	}
	return filtered
}

// GetMethodLimits returns per-payment-type limits from enabled provider instances.
func (s *ConfigService) GetMethodLimits(ctx context.Context, types []string) ([]MethodLimits, error) {
	instances, err := s.store.ListInstances(ctx, InstanceFilter{EnabledOnly: true})
	if err != nil {
		return nil, fmt.Errorf("query provider instances: %w", err)
	}
	cfg, err := s.GetPaymentConfig(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]MethodLimits, 0, len(types))
	for _, pt := range types {
		var matching []*ProviderInstance
		for _, inst := range instances {
			if InstanceSupportsType(inst.SupportedTypes, pt) {
				matching = append(matching, inst)
			}
		}
		currency, ok := s.ConfigPcAggregateMethodCurrency(matching)
		if !ok {
			continue
		}
		ml := ConfigPcAggregateMethodLimits(pt, matching)
		ml = ConfigPcApplyEffectiveMethodFee(cfg, ml)
		ml.DisplayName = s.ConfigPcAggregateMethodDisplayName(pt, matching)
		ml.Currency = currency
		result = append(result, ml)
	}
	return result, nil
}

func ConfigPcApplyEffectiveMethodFee(cfg *PaymentConfig, ml MethodLimits) MethodLimits {
	fee := cfg.EffectiveMethodFee(ml.PaymentType)
	ml.FixedFee = fee.FixedFee
	ml.FeeRate = fee.FeeRate
	return ml
}

func (s *ConfigService) ValidateMethodCurrencyConsistency(ctx context.Context, paymentType string) (string, error) {
	method := NormalizeVisibleMethod(paymentType)
	if method == "" || s == nil || s.store == nil {
		return DefaultPaymentCurrency, nil
	}

	instances, err := s.store.ListInstances(ctx, InstanceFilter{EnabledOnly: true})
	if err != nil {
		return "", fmt.Errorf("query provider instances: %w", err)
	}

	typeInstances := ConfigPcGroupByPaymentType(instances)
	typeInstances = s.ConfigPcApplyEnabledVisibleMethodInstances(ctx, typeInstances, instances)
	matching := typeInstances[method]
	if len(matching) == 0 {
		return DefaultPaymentCurrency, nil
	}

	currency, ok := s.ConfigPcAggregateMethodCurrency(matching)
	if !ok {
		return "", infraerrors.ServiceUnavailable(
			"PAYMENT_METHOD_CURRENCY_CONFLICT",
			"payment method has enabled provider instances with mixed currencies",
		).WithMetadata(map[string]string{"payment_type": method})
	}
	return currency, nil
}

func (s *ConfigService) ConfigPcAggregateMethodCurrency(instances []*ProviderInstance) (string, bool) {
	currency := ""
	for _, inst := range instances {
		next := s.ConfigPcInstancePaymentCurrency(inst)
		if next == "" {
			continue
		}
		if currency == "" {
			currency = next
			continue
		}
		if currency != next {
			return "", false
		}
	}
	if currency == "" {
		return DefaultPaymentCurrency, true
	}
	return currency, true
}

func (s *ConfigService) ConfigPcInstancePaymentCurrency(inst *ProviderInstance) string {
	if inst == nil {
		return DefaultPaymentCurrency
	}
	cfg := map[string]string{}
	if s != nil {
		decrypted, err := s.ConfigDecryptConfig(inst.Config)
		if err == nil && decrypted != nil {
			cfg = decrypted
		}
	}
	return ProviderConfigCurrency(inst.ProviderKey, cfg)
}

type ConfigEasyPayCustomMethodDisplayConfig struct {
	Type        string `json:"type"`
	DisplayName string `json:"displayName"`
}

func (s *ConfigService) ConfigPcAggregateMethodDisplayName(pt string, instances []*ProviderInstance) string {
	pt = strings.TrimSpace(pt)
	if pt == "" {
		return ""
	}
	for _, inst := range instances {
		displayName := s.ConfigPcInstanceEasyPayCustomMethodDisplayName(inst, pt)
		if displayName != "" {
			return displayName
		}
	}
	return ""
}

func (s *ConfigService) ConfigPcInstanceEasyPayCustomMethodDisplayName(inst *ProviderInstance, pt string) string {
	if inst == nil || inst.ProviderKey != TypeEasyPay {
		return ""
	}
	cfg := map[string]string{}
	if s != nil {
		decrypted, err := s.ConfigDecryptConfig(inst.Config)
		if err == nil && decrypted != nil {
			cfg = decrypted
		}
	}
	raw := strings.TrimSpace(cfg["customMethods"])
	if raw == "" {
		return ""
	}

	var methods []ConfigEasyPayCustomMethodDisplayConfig
	if err := json.Unmarshal([]byte(raw), &methods); err != nil {
		return ""
	}
	for _, method := range methods {
		if strings.TrimSpace(method.Type) == pt {
			return strings.TrimSpace(method.DisplayName)
		}
	}
	return ""
}

// ConfigPcGroupByPaymentType 按用户可见支付方式聚合服务商实例。
// Stripe 统一映射为 "stripe"，因为 Checkout 只展示一个 Stripe 入口，具体支付方式由 Stripe Dashboard 控制。
// seen 用于避免同一个实例在一个分组里重复计数。
func ConfigPcGroupByPaymentType(instances []*ProviderInstance) map[string][]*ProviderInstance {
	typeInstances := make(map[string][]*ProviderInstance)
	seen := make(map[string]map[int64]bool)
	add := func(key string, inst *ProviderInstance) {
		if seen[key] == nil {
			seen[key] = make(map[int64]bool)
		}
		if !seen[key][int64(inst.ID)] {
			seen[key][int64(inst.ID)] = true
			typeInstances[key] = append(typeInstances[key], inst)
		}
	}
	for _, inst := range instances {
		// Stripe 服务商统一归到单个 "stripe" 分组。
		if inst.ProviderKey == TypeStripe {
			add(TypeStripe, inst)
			continue
		}
		for _, t := range ConfigSplitTypes(inst.SupportedTypes) {
			add(t, inst)
		}
	}
	return typeInstances
}

// ConfigPcInstanceTypeLimits extracts per-type limits from a provider instance.
// Returns (limits, true) if configured; (zero, false) if unlimited.
// For Stripe instances, limits are stored under "stripe" key regardless of sub-types.
func ConfigPcInstanceTypeLimits(inst *ProviderInstance, pt string) (ChannelLimits, bool) {
	if inst.Limits == "" {
		return ChannelLimits{}, false
	}
	var limits InstanceLimits
	if err := json.Unmarshal([]byte(inst.Limits), &limits); err != nil {
		return ChannelLimits{}, false
	}
	cl, ok := limits[pt]
	return cl, ok
}

// ConfigUnionFloat merges a single limit value into the aggregate using UNION semantics.
//   - For "min" fields (wantMin=true): keeps the lowest non-zero value
//   - For "max"/"cap" fields (wantMin=false): keeps the highest non-zero value
//   - If any value is 0 (unlimited), the result is unlimited.
//
// Returns (aggregated value, still limited).
func ConfigUnionFloat(agg float64, limited bool, val float64, wantMin bool) (float64, bool) {
	if val == 0 {
		return agg, false
	}
	if !limited {
		return agg, false
	}
	if agg == 0 {
		return val, true
	}
	if wantMin && val < agg {
		return val, true
	}
	if !wantMin && val > agg {
		return val, true
	}
	return agg, true
}

// ConfigPcAggregateMethodLimits computes the UNION (least restrictive) of limits
// across all provider instances for a given payment type.
//
// Since the load balancer can route an order to any available instance,
// the user should see the widest possible range:
//   - SingleMin: lowest floor across instances; 0 if any is unlimited
//   - SingleMax: highest ceiling across instances; 0 if any is unlimited
//   - DailyLimit: highest cap across instances; 0 if any is unlimited
func ConfigPcAggregateMethodLimits(pt string, instances []*ProviderInstance) MethodLimits {
	ml := MethodLimits{PaymentType: pt}
	minLimited, maxLimited, dailyLimited := true, true, true

	for _, inst := range instances {
		cl, hasLimits := ConfigPcInstanceTypeLimits(inst, pt)
		if !hasLimits {
			return MethodLimits{PaymentType: pt} // any unlimited instance → all zeros
		}
		ml.SingleMin, minLimited = ConfigUnionFloat(ml.SingleMin, minLimited, cl.SingleMin, true)
		ml.SingleMax, maxLimited = ConfigUnionFloat(ml.SingleMax, maxLimited, cl.SingleMax, false)
		ml.DailyLimit, dailyLimited = ConfigUnionFloat(ml.DailyLimit, dailyLimited, cl.DailyLimit, false)
	}

	if !minLimited {
		ml.SingleMin = 0
	}
	if !maxLimited {
		ml.SingleMax = 0
	}
	if !dailyLimited {
		ml.DailyLimit = 0
	}
	return ml
}

// ConfigPcComputeGlobalRange computes the widest [min, max] across all methods.
// Uses the same union logic: lowest min, highest max, 0 if any is unlimited.
func ConfigPcComputeGlobalRange(methods map[string]MethodLimits) (globalMin, globalMax float64) {
	minLimited, maxLimited := true, true
	for _, ml := range methods {
		globalMin, minLimited = ConfigUnionFloat(globalMin, minLimited, ml.SingleMin, true)
		globalMax, maxLimited = ConfigUnionFloat(globalMax, maxLimited, ml.SingleMax, false)
	}
	if !minLimited {
		globalMin = 0
	}
	if !maxLimited {
		globalMax = 0
	}
	return globalMin, globalMax
}
