//go:build unit

package service

import (
	"reflect"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// 竞争替身显式比较与生产 writer 相同的身份字段；nil 凭据沿用旧快照的空对象语义。
func refreshFailureMatchesFixture(value *Account, version account.RefreshFailureVersion) bool {
	if value == nil {
		return false
	}
	credentials := value.Credentials
	if credentials == nil {
		credentials = map[string]any{}
	}
	return value.ID == version.ID && value.Platform == version.Platform && value.Type == version.Type && value.Status == version.Status && value.Schedulable == version.Schedulable && reflect.DeepEqual(credentials, version.Credentials) && reflect.DeepEqual(value.ProxyID, version.ProxyID)
}
