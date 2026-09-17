// 支付配置与管理规则的唯一实现；存储、环境和渠道构造由端口提供。
package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/settings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func ConfigEnabledVisibleMethodsForProvider(providerKey, supportedTypes string) []string {
	methodSet := make(map[string]struct{}, 2)
	addMethod := func(method string) {
		method = NormalizeVisibleMethod(method)
		if method != "" {
			methodSet[method] = struct{}{}
		}
	}

	switch strings.TrimSpace(providerKey) {
	case TypeAlipay:
		if strings.TrimSpace(supportedTypes) == "" {
			addMethod(TypeAlipay)
			break
		}
		for _, supportedType := range ConfigSplitTypes(supportedTypes) {
			if NormalizeVisibleMethod(supportedType) == TypeAlipay {
				addMethod(TypeAlipay)
				break
			}
		}
	case TypeWxpay:
		if strings.TrimSpace(supportedTypes) == "" {
			addMethod(TypeWxpay)
			break
		}
		for _, supportedType := range ConfigSplitTypes(supportedTypes) {
			if NormalizeVisibleMethod(supportedType) == TypeWxpay {
				addMethod(TypeWxpay)
				break
			}
		}
	case TypeEasyPay:
		for _, supportedType := range ConfigSplitTypes(supportedTypes) {
			addMethod(supportedType)
		}
	}

	methods := make([]string, 0, len(methodSet))
	for _, method := range []string{TypeAlipay, TypeWxpay} {
		if _, ok := methodSet[method]; ok {
			methods = append(methods, method)
			delete(methodSet, method)
		}
	}
	for _, supportedType := range ConfigSplitTypes(supportedTypes) {
		method := NormalizeVisibleMethod(supportedType)
		if _, ok := methodSet[method]; ok {
			methods = append(methods, method)
			delete(methodSet, method)
		}
	}
	return methods
}

func ConfigProviderSupportsVisibleMethod(inst *ProviderInstance, method string) bool {
	if inst == nil || !inst.Enabled {
		return false
	}
	method = NormalizeVisibleMethod(method)
	for _, candidate := range ConfigEnabledVisibleMethodsForProvider(inst.ProviderKey, inst.SupportedTypes) {
		if candidate == method {
			return true
		}
	}
	return false
}

func ConfigFilterEnabledVisibleMethodInstances(instances []*ProviderInstance, method string) []*ProviderInstance {
	filtered := make([]*ProviderInstance, 0, len(instances))
	for _, inst := range instances {
		if ConfigProviderSupportsVisibleMethod(inst, method) {
			filtered = append(filtered, inst)
		}
	}
	return filtered
}

func ConfigFilterVisibleMethodInstancesByProviderKey(instances []*ProviderInstance, method string, providerKey string) []*ProviderInstance {
	filtered := make([]*ProviderInstance, 0, len(instances))
	for _, inst := range instances {
		if !ConfigProviderSupportsVisibleMethod(inst, method) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(inst.ProviderKey), strings.TrimSpace(providerKey)) {
			continue
		}
		filtered = append(filtered, inst)
	}
	return filtered
}

func ConfigDistinctVisibleMethodProviderKeys(instances []*ProviderInstance) []string {
	seen := make(map[string]struct{}, len(instances))
	keys := make([]string, 0, len(instances))
	for _, inst := range instances {
		if inst == nil {
			continue
		}
		key := strings.TrimSpace(inst.ProviderKey)
		if key == "" {
			continue
		}
		normalized := strings.ToLower(key)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func ConfigSelectVisibleMethodInstanceByProviderKey(instances []*ProviderInstance, providerKey string) *ProviderInstance {
	providerKey = strings.TrimSpace(providerKey)
	if providerKey == "" {
		return nil
	}
	for _, inst := range instances {
		if strings.EqualFold(strings.TrimSpace(inst.ProviderKey), providerKey) {
			return inst
		}
	}
	return nil
}

func (s *ConfigService) ConfigValidateVisibleMethodEnablementConflicts(
	ctx context.Context,
	excludeID int64,
	providerKey string,
	supportedTypes string,
	enabled bool,
) error {
	// Visible methods are selected by configured source (official/easypay),
	// so multiple enabled providers can intentionally claim the same user-facing
	// method. Order creation and limits will route through the configured source.
	_, _, _, _, _ = ctx, excludeID, providerKey, supportedTypes, enabled
	return nil
}

func (s *ConfigService) ConfigResolveVisibleMethodSourceProviderKey(ctx context.Context, method string) (string, error) {
	method = NormalizeVisibleMethod(method)
	sourceKey := ResumeVisibleMethodSourceSettingKey(method)
	rawSource := ""
	if s != nil && s.settingRepo != nil && sourceKey != "" {
		value, err := s.settingRepo.GetValue(ctx, sourceKey)
		if err != nil {
			if !errors.Is(err, settings.ErrSettingNotFound) {
				return "", fmt.Errorf("get %s: %w", sourceKey, err)
			}
		} else {
			rawSource = value
		}
	}

	normalizedSource, err := NormalizeVisibleMethodSettingSource(method, rawSource, true)
	if err != nil {
		return "", err
	}
	if normalizedSource == "" {
		return "", nil
	}
	providerKey, ok := VisibleMethodProviderKeyForSource(method, normalizedSource)
	if !ok {
		return "", infraerrors.BadRequest(
			"INVALID_PAYMENT_VISIBLE_METHOD_SOURCE",
			fmt.Sprintf("%s source must be one of the supported payment providers", method),
		)
	}
	return providerKey, nil
}

func (s *ConfigService) ConfigResolveVisibleMethodProviderKey(
	ctx context.Context,
	method string,
	matching []*ProviderInstance,
) (string, error) {
	switch providerKeys := ConfigDistinctVisibleMethodProviderKeys(matching); len(providerKeys) {
	case 0:
		return "", nil
	case 1:
		return strings.TrimSpace(providerKeys[0]), nil
	default:
		providerKey, err := s.ConfigResolveVisibleMethodSourceProviderKey(ctx, method)
		if err != nil {
			return "", err
		}
		if providerKey == "" {
			return "", nil
		}
		selected := ConfigSelectVisibleMethodInstanceByProviderKey(matching, providerKey)
		if selected == nil {
			return "", infraerrors.BadRequest(
				"INVALID_PAYMENT_VISIBLE_METHOD_SOURCE",
				fmt.Sprintf("%s source has no enabled provider instance", method),
			)
		}
		return strings.TrimSpace(selected.ProviderKey), nil
	}
}

func (s *ConfigService) ConfigResolveEnabledVisibleMethodInstance(
	ctx context.Context,
	method string,
) (*ProviderInstance, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}

	method = NormalizeVisibleMethod(method)
	if method == "" {
		return nil, nil
	}

	instances, err := s.store.ListInstances(ctx, InstanceFilter{EnabledOnly: true, SortByOrder: true})
	if err != nil {
		return nil, fmt.Errorf("query enabled payment providers: %w", err)
	}

	matching := ConfigFilterEnabledVisibleMethodInstances(instances, method)
	providerKey, err := s.ConfigResolveVisibleMethodProviderKey(ctx, method, matching)
	if err != nil {
		return nil, err
	}
	if providerKey == "" {
		if len(matching) == 0 {
			return nil, nil
		}
		return &ProviderInstance{ProviderKey: ""}, nil
	}
	return ConfigSelectVisibleMethodInstanceByProviderKey(matching, providerKey), nil
}

// UsesOfficialAlipayVisibleMethod 判断用户可见的支付宝方式是否解析到已启用的官方支付宝实例。
func (s *ConfigService) UsesOfficialAlipayVisibleMethod(ctx context.Context) (bool, error) {
	instance, err := s.ConfigResolveEnabledVisibleMethodInstance(ctx, TypeAlipay)
	if err != nil {
		return false, err
	}
	return ConfigIsOfficialAlipayProviderInstance(instance), nil
}

func ConfigIsOfficialAlipayProviderInstance(instance *ProviderInstance) bool {
	return instance != nil && strings.EqualFold(strings.TrimSpace(instance.ProviderKey), TypeAlipay)
}
