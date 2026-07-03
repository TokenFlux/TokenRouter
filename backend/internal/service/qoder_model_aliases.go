package service

import "strings"

func lookupQoderModelAlias(model string) (qoderModelInfo, bool) {
	if info, ok := defaultQoderModelAliases[model]; ok {
		return info, true
	}
	info, ok := qoderCompatModelAliases[model]
	return info, ok
}

func isQoderAliasBillingModel(model string) bool {
	model = strings.TrimSpace(model)
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
		if strings.TrimSpace(info.Key) == model {
			return true
		}
	}
	return false
}

func QoderAliasDefaultBillingModel(model string) (string, bool) {
	if !isQoderAliasBillingModel(model) {
		return "", false
	}
	return qoderDefaultAliasFallbackBillingModel, true
}
