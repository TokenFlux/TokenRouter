// 渠道加载及订单实例绑定只有一份状态与规则。
package payment

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// GetWebhookProvider returns the provider instance that should verify a webhook.
// It resolves the original provider instance from the order whenever possible and
// only falls back to a registry provider for legacy/single-instance scenarios.
func (s *ProviderBindings) GetWebhookProvider(ctx context.Context, providerKey, outTradeNo string) (Provider, error) {
	providers, err := s.GetWebhookProviders(ctx, providerKey, outTradeNo)
	if err != nil {
		return nil, err
	}
	if len(providers) == 0 {
		return nil, ErrProviderNotFound
	}
	return providers[0], nil
}

// GetWebhookProviders returns provider candidates that can verify the webhook.
// Official WeChat Pay may require multiple candidates because the callback body
// cannot be bound to a merchant before decryption.
func (s *ProviderBindings) GetWebhookProviders(ctx context.Context, providerKey, outTradeNo string) ([]Provider, error) {
	if outTradeNo != "" {
		order, err := s.store.OrderByTradeNumber(ctx, outTradeNo)
		if err == nil {
			if PsHasPinnedProviderInstance(order) {
				prov, err := s.GetPinnedOrderProvider(ctx, order)
				if err != nil {
					return nil, err
				}
				return []Provider{prov}, nil
			}
			inst, err := s.GetOrderProviderInstance(ctx, order)
			if err != nil {
				return nil, fmt.Errorf("load order provider instance: %w", err)
			}
			if inst != nil {
				prov, err := s.CreateProviderFromInstance(ctx, inst)
				if err != nil {
					return nil, err
				}
				return []Provider{prov}, nil
			}
			if strings.TrimSpace(providerKey) == TypeWxpay {
				return s.GetEnabledWebhookProvidersByKey(ctx, providerKey)
			}
			if !s.WebhookRegistryFallbackAllowed(ctx, providerKey) {
				return nil, fmt.Errorf("webhook provider fallback is ambiguous for %s", providerKey)
			}
			s.EnsureProviders(ctx)
			prov, err := s.registry.GetProviderByKey(providerKey)
			if err != nil {
				return nil, err
			}
			return []Provider{prov}, nil
		}
	}

	if strings.TrimSpace(providerKey) == TypeWxpay {
		return s.GetEnabledWebhookProvidersByKey(ctx, providerKey)
	}

	if !s.WebhookRegistryFallbackAllowed(ctx, providerKey) {
		return nil, fmt.Errorf("webhook provider fallback is ambiguous for %s", providerKey)
	}

	s.EnsureProviders(ctx)
	prov, err := s.registry.GetProviderByKey(providerKey)
	if err != nil {
		return nil, err
	}
	return []Provider{prov}, nil
}
func (s *ProviderBindings) GetPinnedOrderProvider(ctx context.Context, o *Order) (Provider, error) {
	inst, err := s.GetOrderProviderInstance(ctx, o)
	if err != nil {
		return nil, fmt.Errorf("load order provider instance: %w", err)
	}
	if inst == nil {
		return nil, fmt.Errorf("order %d provider instance is missing", o.ID)
	}
	return s.CreateProviderFromInstance(ctx, inst)
}
func (s *ProviderBindings) WebhookRegistryFallbackAllowed(ctx context.Context, providerKey string) bool {
	providerKey = strings.TrimSpace(providerKey)
	if providerKey == "" || s == nil || s.store == nil {
		return false
	}

	count, err := s.store.CountEnabledInstances(ctx, providerKey)
	if err != nil {
		s.warn("payment webhook fallback instance count failed", "provider", providerKey, "error", err)
		return false
	}
	return count <= 1
}
func PsHasPinnedProviderInstance(order *Order) bool {
	return order != nil && (PsOrderProviderSnapshot(order) != nil || (order.ProviderInstanceID != nil && strings.TrimSpace(*order.ProviderInstanceID) != ""))
}
func (s *ProviderBindings) GetEnabledWebhookProvidersByKey(ctx context.Context, providerKey string) ([]Provider, error) {
	providerKey = strings.TrimSpace(providerKey)
	instances, err := s.store.ListInstances(ctx, InstanceFilter{ProviderKey: providerKey, EnabledOnly: true, SortByOrder: true})
	if err != nil {
		return nil, fmt.Errorf("query webhook provider instances: %w", err)
	}
	if len(instances) == 0 {
		return nil, ErrProviderNotFound
	}

	providers := make([]Provider, 0, len(instances))
	for _, inst := range instances {
		prov, provErr := s.CreateProviderFromInstance(ctx, inst)
		if provErr != nil {
			s.warn("skip webhook provider instance", "provider", providerKey, "instanceID", inst.ID, "error", provErr)
			continue
		}
		providers = append(providers, prov)
	}
	if len(providers) == 0 {
		return nil, ErrProviderNotFound
	}
	return providers, nil
}

// GetOrderProvider creates a provider using the order's original instance config.
// Falls back to registry lookup if instance ID is missing (legacy orders).
func (s *ProviderBindings) GetOrderProvider(ctx context.Context, o *Order) (Provider, error) {
	inst, err := s.GetOrderProviderInstance(ctx, o)
	if err != nil {
		return nil, fmt.Errorf("load order provider instance: %w", err)
	}
	if inst != nil {
		return s.CreateProviderFromInstance(ctx, inst)
	}
	if !PaymentOrderAllowsRegistryFallback(o) {
		return nil, fmt.Errorf("order %d provider instance is unresolved", o.ID)
	}
	providerKey := PaymentOrderFallbackProviderKey(s.registry, o)
	if providerKey == "" {
		return nil, fmt.Errorf("order %d provider fallback key is missing", o.ID)
	}
	if !s.WebhookRegistryFallbackAllowed(ctx, providerKey) {
		return nil, fmt.Errorf("order %d provider fallback is ambiguous for %s", o.ID, providerKey)
	}
	s.EnsureProviders(ctx)
	return s.registry.GetProvider(o.PaymentType)
}
func PaymentOrderAllowsRegistryFallback(order *Order) bool {
	if order == nil {
		return false
	}
	if PsOrderProviderSnapshot(order) != nil {
		return false
	}
	if strings.TrimSpace(refundStringValue(order.ProviderInstanceID)) != "" {
		return false
	}
	if strings.TrimSpace(refundStringValue(order.ProviderKey)) != "" {
		return false
	}
	return true
}
func PaymentOrderFallbackProviderKey(registry *Registry, order *Order) string {
	if order == nil {
		return ""
	}
	if registry != nil {
		if key := strings.TrimSpace(registry.GetProviderKey(PaymentType(order.PaymentType))); key != "" {
			return key
		}
	}
	return strings.TrimSpace(GetBasePaymentType(strings.TrimSpace(order.PaymentType)))
}
func (s *ProviderBindings) CreateProviderFromInstance(ctx context.Context, inst *ProviderInstance) (Provider, error) {
	if inst == nil {
		return nil, fmt.Errorf("payment provider instance is missing")
	}

	cfg, err := s.loadBalancer.GetInstanceConfig(ctx, int64(inst.ID))
	if err != nil {
		return nil, fmt.Errorf("load provider instance config: %w", err)
	}
	if inst.PaymentMode != "" {
		cfg["paymentMode"] = inst.PaymentMode
	}

	instID := strconv.FormatInt(int64(inst.ID), 10)
	prov, err := s.runtime.Factory(inst.ProviderKey, instID, cfg)
	if err != nil {
		return nil, fmt.Errorf("create provider from instance: %w", err)
	}
	return prov, nil
}
func (s *ProviderBindings) ResolveSnapshotOrderProviderInstance(ctx context.Context, order *Order, snapshot *OrderProviderSnapshot) (*ProviderInstance, error) {
	if s == nil || s.store == nil || order == nil || snapshot == nil {
		return nil, nil
	}

	snapshotInstanceID := strings.TrimSpace(snapshot.ProviderInstanceID)
	columnInstanceID := strings.TrimSpace(refundStringValue(order.ProviderInstanceID))
	if snapshotInstanceID == "" {
		snapshotInstanceID = columnInstanceID
	}
	if snapshotInstanceID == "" {
		return nil, fmt.Errorf("order %d provider snapshot is missing provider_instance_id", order.ID)
	}
	if columnInstanceID != "" && snapshot.ProviderInstanceID != "" && !strings.EqualFold(columnInstanceID, snapshot.ProviderInstanceID) {
		return nil, fmt.Errorf("order %d provider snapshot instance mismatch: snapshot=%s order=%s", order.ID, snapshot.ProviderInstanceID, columnInstanceID)
	}

	instID, err := strconv.ParseInt(snapshotInstanceID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("order %d provider snapshot instance id is invalid: %s", order.ID, snapshotInstanceID)
	}

	inst, err := s.store.Instance(ctx, instID)
	if err != nil {
		if s.store.IsNotFound(err) {
			return nil, fmt.Errorf("order %d provider snapshot instance %s is missing", order.ID, snapshotInstanceID)
		}
		return nil, err
	}

	if snapshot.ProviderKey != "" && !strings.EqualFold(strings.TrimSpace(inst.ProviderKey), snapshot.ProviderKey) {
		return nil, fmt.Errorf("order %d provider snapshot key mismatch: snapshot=%s instance=%s", order.ID, snapshot.ProviderKey, inst.ProviderKey)
	}

	return inst, nil
}

// GetOrderProviderInstance looks up the provider instance that processed this order.
// For legacy orders without provider_instance_id, it resolves only when the
// historical instance is uniquely identifiable from the stored order fields.
func (s *ProviderBindings) GetOrderProviderInstance(ctx context.Context, o *Order) (*ProviderInstance, error) {
	if s == nil || s.store == nil || o == nil {
		return nil, nil
	}

	if snapshot := PsOrderProviderSnapshot(o); snapshot != nil {
		return s.ResolveSnapshotOrderProviderInstance(ctx, o, snapshot)
	}

	instIDStr := strings.TrimSpace(refundStringValue(o.ProviderInstanceID))
	if instIDStr == "" {
		return s.ResolveUniqueLegacyOrderProviderInstance(ctx, o)
	}

	instID, err := strconv.ParseInt(instIDStr, 10, 64)
	if err != nil {
		return nil, nil
	}
	return s.store.Instance(ctx, instID)
}

// GetRefundOrderProviderInstance resolves the provider instance for refund paths.
// Refunds must be pinned to an explicit historical binding, so legacy
// "best-effort" provider guessing is intentionally not allowed here.
func (s *ProviderBindings) GetRefundOrderProviderInstance(ctx context.Context, o *Order) (*ProviderInstance, error) {
	if s == nil || s.store == nil || o == nil {
		return nil, nil
	}

	if snapshot := PsOrderProviderSnapshot(o); snapshot != nil {
		return s.ResolveSnapshotOrderProviderInstance(ctx, o, snapshot)
	}

	instIDStr := strings.TrimSpace(refundStringValue(o.ProviderInstanceID))
	if instIDStr == "" {
		return nil, nil
	}

	instID, err := strconv.ParseInt(instIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("order %d refund provider instance id is invalid: %s", o.ID, instIDStr)
	}
	inst, err := s.store.Instance(ctx, instID)
	if err != nil {
		if s.store.IsNotFound(err) {
			return nil, fmt.Errorf("order %d refund provider instance %s is missing", o.ID, instIDStr)
		}
		return nil, err
	}
	return inst, nil
}
func (s *ProviderBindings) ResolveUniqueLegacyOrderProviderInstance(ctx context.Context, o *Order) (*ProviderInstance, error) {
	paymentType := GetBasePaymentType(strings.TrimSpace(o.PaymentType))
	providerKey := strings.TrimSpace(refundStringValue(o.ProviderKey))
	if providerKey != "" {
		instances, err := s.store.ListInstances(ctx, InstanceFilter{ProviderKey: providerKey})
		if err != nil {
			return nil, err
		}
		matched := PsFilterLegacyOrderProviderInstances(paymentType, instances)
		if len(matched) == 1 {
			return matched[0], nil
		}
		return nil, nil
	}

	if paymentType == "" {
		return nil, nil
	}

	instances, err := s.store.ListInstances(ctx, InstanceFilter{})
	if err != nil {
		return nil, err
	}

	matched := PsFilterLegacyOrderProviderInstances(paymentType, instances)
	if len(matched) == 1 {
		return matched[0], nil
	}
	return nil, nil
}
func PsFilterLegacyOrderProviderInstances(orderPaymentType string, instances []*ProviderInstance) []*ProviderInstance {
	if len(instances) == 0 {
		return nil
	}
	if strings.TrimSpace(orderPaymentType) == "" {
		return instances
	}
	var matched []*ProviderInstance
	for _, inst := range instances {
		if PsLegacyOrderMatchesInstance(orderPaymentType, inst) {
			matched = append(matched, inst)
		}
	}
	return matched
}
func PsLegacyOrderMatchesInstance(orderPaymentType string, inst *ProviderInstance) bool {
	if inst == nil {
		return false
	}

	baseType := GetBasePaymentType(strings.TrimSpace(orderPaymentType))
	instanceProviderKey := strings.TrimSpace(inst.ProviderKey)
	if baseType == "" {
		return false
	}

	if baseType == TypeStripe {
		return instanceProviderKey == TypeStripe
	}
	if instanceProviderKey == TypeStripe {
		return false
	}
	if instanceProviderKey == baseType {
		return true
	}
	return InstanceSupportsType(inst.SupportedTypes, baseType)
}

// GetRefundProvider creates a provider using the order's original instance config.
// Delegates to GetOrderProvider which handles instance lookup and fallback.
func (s *ProviderBindings) GetRefundProvider(ctx context.Context, o *Order) (Provider, error) {
	inst, err := s.GetRefundOrderProviderInstance(ctx, o)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, fmt.Errorf("refund provider instance is unavailable for order %d", o.ID)
	}
	return s.CreateProviderFromInstance(ctx, inst)
}

// EnsureProviders lazily initializes the provider registry on first call.
// EnsureProviders 只有完整读取成功才发布并标记；失败保留下一次重试机会。
func (s *ProviderBindings) EnsureProviders(ctx context.Context) {
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	if s.providersLoaded {
		return
	}
	providers, err := s.LoadProviders(ctx)
	if err != nil {
		return
	}
	s.registry.Replace(providers)
	s.providersLoaded = true
}

// RefreshProviders 在候选构造完成后原子替换，查询失败不清空已发布的表。
func (s *ProviderBindings) RefreshProviders(ctx context.Context) {
	_ = s.RefreshProvidersChecked(ctx)
}

// RefreshProvidersChecked 让必须确认运行配置生效的综合设置入口取得加载失败。
func (s *ProviderBindings) RefreshProvidersChecked(ctx context.Context) error {
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	providers, err := s.LoadProviders(ctx)
	if err != nil {
		s.providersLoaded = false
		return err
	}
	s.registry.Replace(providers)
	s.providersLoaded = true
	return nil
}
func (s *ProviderBindings) LoadProviders(ctx context.Context) ([]Provider, error) {
	instances, err := s.store.ListInstances(ctx, InstanceFilter{EnabledOnly: true})
	if err != nil {
		s.warn("[PaymentService] failed to query provider instances", "error", err)
		return nil, err
	}
	providers := make([]Provider, 0, len(instances))
	for _, inst := range instances {
		cfg, err := s.loadBalancer.GetInstanceConfig(ctx, int64(inst.ID))
		if err != nil {
			s.warn("[PaymentService] failed to decrypt config for instance", "instanceID", inst.ID, "error", err)
			continue
		}
		if inst.PaymentMode != "" {
			cfg["paymentMode"] = inst.PaymentMode
		}
		instID := fmt.Sprintf("%d", inst.ID)
		p, err := s.runtime.RegistryFactory(inst.ProviderKey, instID, cfg)
		if err != nil {
			s.warn("[PaymentService] failed to create provider for instance", "instanceID", inst.ID, "key", inst.ProviderKey, "error", err)
			continue
		}
		providers = append(providers, p)
	}
	return providers, nil
}
