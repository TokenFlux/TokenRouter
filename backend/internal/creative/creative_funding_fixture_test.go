//go:build unit

package creative_test

import (
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"go.uber.org/zap"
)

func creativeLegacyObserve(event string, values ...any) {
	fields := make([]zap.Field, 0, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		key, _ := values[i].(string)
		fields = append(fields, zap.Any(key, values[i+1]))
	}
	logging.L().Warn(event, fields...)
}

// creativeFundingProjection 仅绑定原生资金替身，不复制预占或捕获规则。
func creativeFundingProjection(store creative.FundingStore) creative.Funding {
	return creative.Funding{Store: store, Observe: creativeLegacyObserve}
}
