// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	ent "github.com/TokenFlux/TokenRouter/ent"
	postgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// NewTLSFingerprintRouterRepository 委托所属模块的唯一实现。
func NewTLSFingerprintRouterRepository(client *ent.Client) service.TLSFingerprintRouterRepository {
	return postgres.NewTLSFingerprintRouterRepository(client)
}
