package service

import "sync"

var qoderModelAliasesState = struct {
	sync.RWMutex
	aliases map[string]qoderModelInfo
}{}

func lookupQoderModelAlias(model string) (qoderModelInfo, bool) {
	qoderModelAliasesState.RLock()
	aliases := qoderModelAliasesState.aliases
	if aliases != nil {
		info, ok := aliases[model]
		qoderModelAliasesState.RUnlock()
		return info, ok
	}
	qoderModelAliasesState.RUnlock()

	qoderModelAliasesState.Lock()
	defer qoderModelAliasesState.Unlock()
	if qoderModelAliasesState.aliases == nil {
		qoderModelAliasesState.aliases = cloneQoderModelAliases(defaultQoderModelAliases)
	}
	info, ok := qoderModelAliasesState.aliases[model]
	return info, ok
}

func currentQoderModelAliases() map[string]qoderModelInfo {
	qoderModelAliasesState.RLock()
	aliases := qoderModelAliasesState.aliases
	qoderModelAliasesState.RUnlock()
	if aliases != nil {
		return cloneQoderModelAliases(aliases)
	}
	return cloneQoderModelAliases(defaultQoderModelAliases)
}

func applyQoderModelAliases(aliases map[string]qoderModelInfo) {
	qoderModelAliasesState.Lock()
	defer qoderModelAliasesState.Unlock()
	qoderModelAliasesState.aliases = cloneQoderModelAliases(aliases)
}

func resetQoderModelAliasesForTest() {
	applyQoderModelAliases(defaultQoderModelAliases)
}

func cloneQoderModelAliases(src map[string]qoderModelInfo) map[string]qoderModelInfo {
	out := make(map[string]qoderModelInfo, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}
