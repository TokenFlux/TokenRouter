package billing

import (
	"context"
	"strings"
)

// ReadBalanceUnitName 保留原单键读取、去空白和 USD 故障默认值。
func ReadBalanceUnitName(ctx context.Context, store interface {
	GetValue(context.Context, string) (string, error)
}) string {
	value, err := store.GetValue(ctx, SettingKeyBalanceUnitName)
	if err != nil {
		return "USD"
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "USD"
	}
	return value
}
