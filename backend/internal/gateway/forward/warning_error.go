package forward

import (
	"errors"
)

// WarningFromError 从转发错误链中提取上游 cyber 风控警告。
func WarningFromError(err error) (*UpstreamWarning, bool) {
	if err == nil {
		return nil, false
	}
	var carrier UpstreamWarningCarrier
	if !errors.As(err, &carrier) || carrier == nil {
		return nil, false
	}
	warning := carrier.OpenAIUpstreamWarning()
	if warning == nil {
		return nil, false
	}
	return cloneWarning(warning), true
}

func cloneWarning(warning *UpstreamWarning) *UpstreamWarning {
	if warning == nil {
		return nil
	}
	cloned := *warning
	if warning.ResponseBody != nil {
		cloned.ResponseBody = append([]byte(nil), warning.ResponseBody...)
	}
	return &cloned
}
