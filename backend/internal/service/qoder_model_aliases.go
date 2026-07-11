package service

import "strings"

func lookupQoderModelAlias(model string) (qoderModelInfo, bool) {
	model = normalizeQoderAliasModel(model)
	if info, ok := defaultQoderModelAliases[model]; ok {
		return info, true
	}
	info, ok := qoderCompatModelAliases[model]
	return info, ok
}

func isQoderAliasBillingModel(model string) bool {
	model = normalizeQoderAliasModel(model)
	if model == "" {
		return false
	}
	if _, ok := defaultQoderModelAliases[model]; ok {
		return true
	}
	if _, ok := qoderCompatModelAliases[model]; ok {
		return true
	}
	return isQoderAliasRouteKey(model, defaultQoderModelAliases) ||
		isQoderAliasRouteKey(model, qoderCompatModelAliases)
}

func isQoderAliasRouteKey(model string, aliases map[string]qoderModelInfo) bool {
	for _, info := range aliases {
		if normalizeQoderAliasModel(info.Key) == model {
			return true
		}
	}
	return false
}

func normalizeQoderAliasModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func QoderAliasRequiresManualPricing(model string) bool {
	return isQoderAliasBillingModel(model)
}

func qoderAliasRequiresManualPricingAny(models ...string) bool {
	for _, model := range models {
		if isQoderAliasBillingModel(model) {
			return true
		}
	}
	return false
}
