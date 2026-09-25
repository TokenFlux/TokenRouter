//go:build unit

package provider_test

import (
	"reflect"

	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// 竞争替身显式比较与生产 writer 相同的身份字段；nil 凭据沿用旧快照的空对象语义。
func refreshFailureMatchesFixture(value *gatewayprovider.ExecutionAccount, version account.RefreshFailureVersion) bool {
	if value == nil {
		return false
	}
	credentials := value.Record.Credentials
	if credentials == nil {
		credentials = map[string]any{}
	}
	return value.Record.ID == version.ID && value.Record.Platform == version.Platform && value.Record.Type == version.Type && value.Record.Status == version.Status && value.Record.Schedulable == version.Schedulable && reflect.DeepEqual(credentials, version.Credentials) && reflect.DeepEqual(value.Record.ProxyID, version.ProxyID)
}
