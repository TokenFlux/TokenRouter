// 旧 HTTP 入口只委托新 Adapter，S15/S16 清理。
package admin

import (
	"encoding/json"

	native "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
)

func isPercentOrRateMetric(metricType string) bool {
	return native.CompatIsPercentOrRateMetric(metricType)
}
func validateOpsAlertRulePayload(raw map[string]json.RawMessage) (*opsAlertRuleValidatedInput, error) {
	return native.CompatValidateOpsAlertRulePayload(raw)
}

type opsAlertRuleValidatedInput = native.CompatOpsAlertRuleValidatedInput
