// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package repository

import (
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// NewUserPlatformQuotaServiceAdapter 保留旧 Wire 签名；数据类型已由 billing 唯一拥有。
func NewUserPlatformQuotaServiceAdapter(repo UserPlatformQuotaRepository) service.UserPlatformQuotaRepository {
	return repo
}
