package admin

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/service"
)

// enrichShadowParentInfo 只投影旧父账号，字段赋值由新 HTTP 唯一实现。
func enrichShadowParentInfo(items []AccountWithConcurrency, parents map[int64]*service.Account) {
	values := make(map[int64]*account.Record, len(parents))
	for id, v := range parents {
		values[id] = service.AccountRecordView(v)
	}
	accounthttp.EnrichShadowParentInfo(items, values)
}
