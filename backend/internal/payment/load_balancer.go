package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// Strategy represents a load balancing strategy for provider instance selection.
type Strategy string

const (
	StrategyRoundRobin  Strategy = "round-robin"
	StrategyLeastAmount Strategy = "least-amount"
)

// ChannelLimits holds limits for a single payment channel within a provider instance.
type ChannelLimits struct {
	DailyLimit float64 `json:"dailyLimit,omitempty"`
	SingleMin  float64 `json:"singleMin,omitempty"`
	SingleMax  float64 `json:"singleMax,omitempty"`
}

// InstanceLimits holds per-channel limits for a provider instance (JSON).
type InstanceLimits map[string]ChannelLimits

// LoadBalancer selects a provider instance for a given payment type.
type LoadBalancer interface {
	GetInstanceConfig(ctx context.Context, instanceID int64) (map[string]string, error)
	SelectInstance(ctx context.Context, providerKey string, paymentType PaymentType, strategy Strategy, orderAmount float64) (*InstanceSelection, error)
}

// DefaultLoadBalancer implements LoadBalancer using database queries.
type DefaultLoadBalancer struct {
	source        InstanceSource
	encryptionKey []byte
	counter       atomic.Uint64
	runtime       SelectionRuntime
}

type contextKey string

const wxpayJSAPIAppIDContextKey contextKey = "payment.wxpay.jsapi_app_id"

// NewDefaultLoadBalancer creates a new load balancer.
func NewDefaultLoadBalancer(source InstanceSource, encryptionKey []byte, runtime ...SelectionRuntime) *DefaultLoadBalancer {
	options := SelectionRuntime{Now: time.Now}
	if len(runtime) > 0 {
		options = runtime[0]
		if options.Now == nil {
			options.Now = time.Now
		}
	}
	return &DefaultLoadBalancer{source: source, encryptionKey: encryptionKey, runtime: options}
}

func WithWxpayJSAPIAppID(ctx context.Context, appID string) context.Context {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return ctx
	}
	return context.WithValue(ctx, wxpayJSAPIAppIDContextKey, appID)
}

func wxpayJSAPIAppIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	appID, _ := ctx.Value(wxpayJSAPIAppIDContextKey).(string)
	return strings.TrimSpace(appID)
}

// instanceCandidate pairs an instance with its pre-fetched daily usage.
type instanceCandidate struct {
	inst      *ProviderInstance
	dailyUsed float64 // 包含待支付和渠道处理中的订单
}

// SelectInstance picks an enabled instance for the given provider key and payment type.
//
// Flow:
//  1. Query all enabled instances for providerKey, filter by supported paymentType
//  2. 批量查询所有候选实例的当日占用（PENDING + PROCESSING + PAID + COMPLETED + RECHARGING）
//  3. Filter out instances where: single-min/max violated OR daily remaining < orderAmount
//  4. Pick from survivors using the configured strategy (round-robin / least-amount)
//  5. If all filtered out, fall back to full list (let the provider itself reject)
func (lb *DefaultLoadBalancer) SelectInstance(
	ctx context.Context,
	providerKey string,
	paymentType PaymentType,
	strategy Strategy,
	orderAmount float64,
) (*InstanceSelection, error) {
	// Step 1: query enabled instances matching payment type.
	instances, err := lb.queryEnabledInstances(ctx, providerKey, paymentType)
	if err != nil {
		return nil, err
	}

	// Step 2: batch-fetch daily usage for all candidates.
	candidates := lb.attachDailyUsage(ctx, instances)

	// Step 3: filter by limits.
	available := filterByLimits(candidates, paymentType, orderAmount, lb.runtime.Observe)
	if len(available) == 0 {
		lb.observe("warn", "all instances exceeded limits, using full candidate list",
			"provider", providerKey, "payment_type", paymentType,
			"order_amount", orderAmount, "count", len(candidates))
		available = candidates
	}

	// Step 4: pick by strategy.
	selected := lb.pickByStrategy(available, strategy)
	return lb.buildSelection(selected.inst)
}

// queryEnabledInstances returns enabled instances that support paymentType.
// When providerKey is non-empty, only instances with that provider key are considered.
// When providerKey is empty, instances across all providers are considered,
// enabling cross-provider load balancing (e.g. EasyPay + Alipay direct for "alipay").
func (lb *DefaultLoadBalancer) queryEnabledInstances(
	ctx context.Context,
	providerKey string,
	paymentType PaymentType,
) ([]*ProviderInstance, error) {
	instances, err := lb.source.EnabledInstances(ctx, providerKey)
	if err != nil {
		return nil, fmt.Errorf("query provider instances: %w", err)
	}

	var matched []*ProviderInstance
	expectedWxpayJSAPIAppID := wxpayJSAPIAppIDFromContext(ctx)
	for _, inst := range instances {
		// Stripe 按 provider_key 匹配，兼容旧 supported_types 子方式和新的服务商级 "stripe" 配置。
		if paymentType == TypeStripe {
			if inst.ProviderKey == TypeStripe {
				matched = append(matched, inst)
			}
		} else if InstanceSupportsType(inst.SupportedTypes, paymentType) {
			if expectedWxpayJSAPIAppID != "" && normalizeVisibleMethodSupportType(paymentType) == TypeWxpay && inst.ProviderKey == TypeWxpay {
				config, cfgErr := lb.decryptConfig(inst.Config)
				if cfgErr != nil {
					lb.observe("warn", "skip wxpay instance with unreadable config during jsapi filtering", "instance_id", inst.ID, "error", cfgErr)
					continue
				}
				if resolveWxpayJSAPIAppID(config) != expectedWxpayJSAPIAppID {
					continue
				}
			}
			matched = append(matched, inst)
		}
	}
	if len(matched) == 0 {
		return nil, fmt.Errorf("no enabled instance for payment type %s", paymentType)
	}
	return matched, nil
}

// attachDailyUsage queries daily usage for each instance in a single pass.
// 占用量包含待支付和渠道处理中的订单，避免实例容量被超额分配。
func (lb *DefaultLoadBalancer) attachDailyUsage(
	ctx context.Context,
	instances []*ProviderInstance,
) []instanceCandidate {
	todayStart := startOfDay(lb.runtime.Now())

	// Collect instance IDs.
	ids := make([]string, len(instances))
	for i, inst := range instances {
		ids[i] = fmt.Sprintf("%d", inst.ID)
	}

	usageMap, err := lb.source.DailyUsage(ctx, ids, todayStart)
	if err != nil {
		lb.observe("warn", "batch daily usage query failed, treating all as zero", "error", err)
	}

	candidates := make([]instanceCandidate, len(instances))
	for i, inst := range instances {
		candidates[i] = instanceCandidate{
			inst:      inst,
			dailyUsed: usageMap[fmt.Sprintf("%d", inst.ID)],
		}
	}
	return candidates
}

// filterByLimits removes instances that cannot accommodate the order:
//   - orderAmount outside single-transaction [min, max]
//   - daily remaining capacity (limit - used) < orderAmount
func filterByLimits(candidates []instanceCandidate, paymentType PaymentType, orderAmount float64, observers ...SelectionObserver) []instanceCandidate {
	observe := selectionObserver(observers)
	var result []instanceCandidate
	for _, c := range candidates {
		cl := getInstanceChannelLimits(c.inst, paymentType)

		if cl.SingleMin > 0 && orderAmount < cl.SingleMin {
			observe("info", "order below instance single min, skipping",
				"instance_id", c.inst.ID, "order", orderAmount, "min", cl.SingleMin)
			continue
		}
		if cl.SingleMax > 0 && orderAmount > cl.SingleMax {
			observe("info", "order above instance single max, skipping",
				"instance_id", c.inst.ID, "order", orderAmount, "max", cl.SingleMax)
			continue
		}
		if cl.DailyLimit > 0 && c.dailyUsed+orderAmount > cl.DailyLimit {
			observe("info", "instance daily remaining insufficient, skipping",
				"instance_id", c.inst.ID, "used", c.dailyUsed,
				"order", orderAmount, "limit", cl.DailyLimit)
			continue
		}

		result = append(result, c)
	}
	return result
}

// getInstanceChannelLimits returns the channel limits for a specific payment type.
func getInstanceChannelLimits(inst *ProviderInstance, paymentType PaymentType) ChannelLimits {
	if inst.Limits == "" {
		return ChannelLimits{}
	}
	var limits InstanceLimits
	if err := json.Unmarshal([]byte(inst.Limits), &limits); err != nil {
		return ChannelLimits{}
	}
	// For Stripe, limits are stored under the provider key "stripe".
	lookupKey := paymentType
	if inst.ProviderKey == "stripe" {
		lookupKey = "stripe"
	}
	if cl, ok := limits[lookupKey]; ok {
		return cl
	}
	if aliasKey := legacyVisibleMethodAlias(lookupKey); aliasKey != "" {
		if cl, ok := limits[aliasKey]; ok {
			return cl
		}
	}
	return ChannelLimits{}
}

// pickByStrategy selects one instance from the available candidates.
func (lb *DefaultLoadBalancer) pickByStrategy(candidates []instanceCandidate, strategy Strategy) instanceCandidate {
	if strategy == StrategyLeastAmount && len(candidates) > 1 {
		return pickLeastAmount(candidates)
	}
	// Default: round-robin.
	idx := lb.counter.Add(1) % uint64(len(candidates))
	return candidates[idx]
}

// pickLeastAmount selects the instance with the lowest daily usage.
// No extra DB queries — usage was pre-fetched in attachDailyUsage.
func pickLeastAmount(candidates []instanceCandidate) instanceCandidate {
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.dailyUsed < best.dailyUsed {
			best = c
		}
	}
	return best
}

func (lb *DefaultLoadBalancer) buildSelection(selected *ProviderInstance) (*InstanceSelection, error) {
	config, err := lb.decryptConfig(selected.Config)
	if err != nil {
		return nil, fmt.Errorf("decrypt instance %d config: %w", selected.ID, err)
	}
	if config == nil {
		config = map[string]string{}
	}

	if selected.PaymentMode != "" {
		config["paymentMode"] = selected.PaymentMode
	}

	return &InstanceSelection{
		InstanceID:     fmt.Sprintf("%d", selected.ID),
		ProviderKey:    selected.ProviderKey,
		Config:         config,
		SupportedTypes: selected.SupportedTypes,
		PaymentMode:    selected.PaymentMode,
	}, nil
}

// decryptConfig parses a stored provider config.
// New records are plaintext JSON; legacy records are AES-256-GCM ciphertext.
// Unreadable values (legacy ciphertext without a valid key, or malformed data)
// are treated as empty so the service keeps running while the admin re-enters
// the config via the UI.
//
// TODO(deprecated-legacy-ciphertext): The AES fallback branch below is a
// transitional compatibility shim for pre-plaintext records. Remove it (and
// the encryptionKey field + the Decrypt import) after a few releases once all
// live deployments have re-saved their provider configs through the UI.
func (lb *DefaultLoadBalancer) decryptConfig(stored string) (map[string]string, error) {
	if stored == "" {
		return nil, nil
	}
	var config map[string]string
	if err := json.Unmarshal([]byte(stored), &config); err == nil {
		return config, nil
	}
	// Deprecated: legacy AES-256-GCM ciphertext fallback — scheduled for removal.
	if len(lb.encryptionKey) == AES256KeySize {
		//nolint:staticcheck // SA1019: intentional legacy fallback, scheduled for removal
		if plaintext, err := Decrypt(stored, lb.encryptionKey); err == nil {
			if err := json.Unmarshal([]byte(plaintext), &config); err == nil {
				return config, nil
			}
		}
	}
	lb.observe("warn", "payment provider config unreadable, treating as empty for re-entry",
		"stored_len", len(stored))
	return nil, nil
}

// GetInstanceDailyAmount returns the total completed order amount for an instance today.
func (lb *DefaultLoadBalancer) GetInstanceDailyAmount(ctx context.Context, instanceID string) (float64, error) {
	todayStart := startOfDay(lb.runtime.Now())

	amount, err := lb.source.PaidDailyAmount(ctx, instanceID, todayStart)
	if err != nil {
		return 0, fmt.Errorf("query daily amount: %w", err)
	}
	return amount, nil
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// InstanceSupportsType checks if the given supported types string includes the target type.
// An empty supportedTypes string means all types are supported.
func InstanceSupportsType(supportedTypes string, target PaymentType) bool {
	if supportedTypes == "" {
		return true
	}
	normalizedTarget := normalizeVisibleMethodSupportType(target)
	for _, t := range strings.Split(supportedTypes, ",") {
		supported := strings.TrimSpace(t)
		if supported == target || normalizeVisibleMethodSupportType(supported) == normalizedTarget {
			return true
		}
	}
	return false
}

func normalizeVisibleMethodSupportType(paymentType PaymentType) PaymentType {
	switch strings.TrimSpace(paymentType) {
	case TypeAlipay, TypeAlipayDirect:
		return TypeAlipay
	case TypeWxpay, TypeWxpayDirect:
		return TypeWxpay
	default:
		return strings.TrimSpace(paymentType)
	}
}

func legacyVisibleMethodAlias(paymentType PaymentType) PaymentType {
	switch normalizeVisibleMethodSupportType(paymentType) {
	case TypeAlipay:
		return TypeAlipayDirect
	case TypeWxpay:
		return TypeWxpayDirect
	default:
		return ""
	}
}

func resolveWxpayJSAPIAppID(config map[string]string) string {
	if appID := strings.TrimSpace(config["mpAppId"]); appID != "" {
		return appID
	}
	return strings.TrimSpace(config["appId"])
}

// GetInstanceConfig 解密并返回 provider 实例配置；不可读配置会返回可写的空配置。
func (lb *DefaultLoadBalancer) GetInstanceConfig(ctx context.Context, instanceID int64) (map[string]string, error) {
	inst, err := lb.source.Instance(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("get instance %d: %w", instanceID, err)
	}
	config, err := lb.decryptConfig(inst.Config)
	if err != nil {
		return nil, err
	}
	if config == nil {
		config = map[string]string{}
	}
	return config, nil
}

// 观察端口不安装日志后端，app 保留原级别和字段。
func (lb *DefaultLoadBalancer) observe(level, message string, attrs ...any) {
	if lb.runtime.Observe != nil {
		lb.runtime.Observe(level, message, attrs...)
	}
}
func selectionObserver(values []SelectionObserver) SelectionObserver {
	if len(values) > 0 && values[0] != nil {
		return values[0]
	}
	return func(string, string, ...any) {}
}
