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
	_, ok := qoderCompatModelAliases[model]
	return ok
}
