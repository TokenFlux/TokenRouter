package account_test

import (
	"database/sql/driver"
	"encoding/json"
	"reflect"
)

type proxyAccountIDsPayloadMatcher struct {
	want []int64
}

func (m proxyAccountIDsPayloadMatcher) Match(value driver.Value) bool {
	raw, ok := value.([]byte)
	if !ok {
		return false
	}
	var payload struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	return reflect.DeepEqual(m.want, payload.AccountIDs)
}
