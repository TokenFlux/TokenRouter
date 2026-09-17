// 可见渠道来源规则由支付拥有，设置入口只做委托。
package payment

import (
	"fmt"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func NormalizeVisibleMethodSettingSource(method, source string, enabled bool) (string, error) {
	_ = enabled
	source = strings.TrimSpace(source)
	if source == "" {
		return "", nil
	}

	normalized := NormalizeVisibleMethodSource(method, source)
	if normalized == "" {
		return "", infraerrors.BadRequest(
			"INVALID_PAYMENT_VISIBLE_METHOD_SOURCE",
			fmt.Sprintf("%s source must be one of the supported payment providers", method),
		)
	}
	return normalized, nil
}
