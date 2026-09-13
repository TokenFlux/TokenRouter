package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
)

// errorPolicyRecord 只借用即时只读的策略配置，不保留账号、凭据或请求缓存。
func errorPolicyRecord(value *Account) *account.Record {
	if value == nil {
		return nil
	}
	return &account.Record{ID: value.ID, Platform: value.Platform, Type: value.Type, Credentials: value.Credentials}
}
