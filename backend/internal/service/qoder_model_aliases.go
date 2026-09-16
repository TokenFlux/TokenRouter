package service

import qoder "github.com/TokenFlux/TokenRouter/internal/upstream/qoder"

func lookupQoderModelAlias(model string) (qoderModelInfo, bool) {
	return qoder.LookupQoderModelAlias(model)
}
